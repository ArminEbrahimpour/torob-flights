package flytoday

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/offerurl"
)

const defaultBaseURL = "https://www.flytoday.ir/api/gateway/V1"
const defaultRateLimitPause = 5 * time.Minute

type Options struct {
	Adult                   int
	CabinType               string
	RPS                     float64
	Burst                   int
	HTTPTimeout             time.Duration
	CircuitBreakerThreshold int
	CircuitBreakerOpen      time.Duration
	BaseURL                 string
	Referer                 string
	OriginHeader            string
	RequestSource           string
	Cookie                  string
	PollInterval            time.Duration
	MaxPolls                int
}

type Client struct {
	baseURL       string
	referer       string
	originHeader  string
	requestSource string
	cookie        string
	pollInterval  time.Duration
	maxPolls      int
	http          *http.Client
	adult         int
	cabin         string
	limiter       *requestLimiter
	breaker       *circuitBreaker
}

func New(opts Options) *Client {
	if opts.Adult < 1 {
		opts.Adult = 1
	}
	if opts.CabinType == "" {
		opts.CabinType = "Y"
	}
	if opts.RPS <= 0 {
		opts.RPS = 0.2
	}
	if opts.Burst < 1 {
		opts.Burst = 1
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 60 * time.Second
	}
	if opts.BaseURL == "" {
		opts.BaseURL = defaultBaseURL
	}
	if opts.Referer == "" {
		opts.Referer = "https://www.flytoday.ir"
	}
	if opts.OriginHeader == "" {
		opts.OriginHeader = "https://www.flytoday.ir"
	}
	if opts.RequestSource == "" {
		opts.RequestSource = "www.flytoday.ir"
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.MaxPolls <= 0 {
		opts.MaxPolls = 20
	}
	return &Client{
		baseURL:       strings.TrimRight(opts.BaseURL, "/"),
		referer:       opts.Referer,
		originHeader:  opts.OriginHeader,
		requestSource: opts.RequestSource,
		cookie:        strings.TrimSpace(opts.Cookie),
		pollInterval:  opts.PollInterval,
		maxPolls:      opts.MaxPolls,
		http: &http.Client{
			Timeout: opts.HTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        16,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		adult:   opts.Adult,
		cabin:   strings.ToUpper(opts.CabinType),
		limiter: newRequestLimiter(opts.RPS, opts.Burst),
		breaker: newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
	}
}

type searchResponse struct {
	SearchID          int64             `json:"searchId"`
	PricedItineraries []pricedItinerary `json:"pricedItineraries"`
	AdditionalData    *additionalData   `json:"additionalData"`
}

type additionalData struct {
	Airlines []airlineInfo `json:"airlines"`
}

type airlineInfo struct {
	Iata      string `json:"iata"`
	Name      string `json:"name"`
	NameLocal string `json:"nameLocal"`
}

type pricedItinerary struct {
	FareSourceCode          string `json:"fareSourceCode"`
	OfferLink               string `json:"-"`
	ValidatingAirlineCode   string `json:"validatingAirlineCode"`
	IsCharter               bool   `json:"isCharter"`
	AirItineraryPricingInfo struct {
		ItinTotalFare struct {
			TotalFare float64 `json:"totalFare"`
		} `json:"itinTotalFare"`
	} `json:"airItineraryPricingInfo"`
	OriginDestinationOptions []originDestOption `json:"originDestinationOptions"`
	RouteInfo                struct {
		DepartureDateTime       string `json:"departureDateTime"`
		OriginLocationCode      string `json:"originLocationCode"`
		DestinationLocationCode string `json:"destinationLocationCode"`
	} `json:"routeInfo"`
}

func (it *pricedItinerary) UnmarshalJSON(data []byte) error {
	type alias pricedItinerary
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*it = pricedItinerary(decoded)
	it.OfferLink = offerurl.FromJSON(data)
	return nil
}

type originDestOption struct {
	JourneyDurationPerMinute int             `json:"journeyDurationPerMinute"`
	FlightSegments           []flightSegment `json:"flightSegments"`
}

type flightSegment struct {
	DepartureDateTime            string `json:"departureDateTime"`
	ArrivalDateTime              string `json:"arrivalDateTime"`
	DepartureAirportLocationCode string `json:"departureAirportLocationCode"`
	ArrivalAirportLocationCode   string `json:"arrivalAirportLocationCode"`
	MarketingAirlineCode         string `json:"marketingAirlineCode"`
	FlightNumber                 string `json:"flightNumber"`
	Baggage                      string `json:"baggage"`
	BaggageLocal                 string `json:"baggageLocal"`
	StopQuantity                 int    `json:"stopQuantity"`
}

type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("HTTP %d %s %s (retry after %s): %s", e.StatusCode, e.Method, e.Path, e.RetryAfter.Round(time.Second), e.Body)
	}
	return fmt.Sprintf("HTTP %d %s %s: %s", e.StatusCode, e.Method, e.Path, e.Body)
}

type CircuitOpenError struct {
	RetryAfter time.Duration
	OpenUntil  time.Time
}

func (e *CircuitOpenError) Error() string {
	return fmt.Sprintf("FlyToday circuit breaker is open until %s (retry after %s)",
		e.OpenUntil.UTC().Format(time.RFC3339), e.RetryAfter.Round(time.Second))
}

func IsCircuitOpen(err error) bool {
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusConflict)
}

func RetryAfter(err error) time.Duration {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.RetryAfter
	}
	var circuitErr *CircuitOpenError
	if errors.As(err, &circuitErr) {
		return circuitErr.RetryAfter
	}
	return 0
}

func (c *Client) CircuitState() map[string]any {
	return c.breaker.state()
}

func (c *Client) CircuitOpenRemaining() time.Duration {
	return c.breaker.openRemaining()
}

func (c *Client) CollectDay(ctx context.Context, origin, destination string, day time.Time) (string, []flight.Flight, error) {
	searchID, itineraries, airlineNames, err := c.search(ctx, origin, destination, day)
	if err != nil {
		return "", nil, err
	}
	if len(itineraries) == 0 && searchID != "" {
		itineraries, err = c.pollSearch(ctx, searchID)
		if err != nil {
			return searchID, nil, err
		}
	}
	flights := make([]flight.Flight, 0, len(itineraries))
	for _, it := range itineraries {
		f, ok := mapItinerary(origin, destination, day, it, airlineNames)
		if ok {
			flights = append(flights, f)
		}
	}
	return searchID, flights, nil
}

func (c *Client) search(ctx context.Context, origin, destination string, day time.Time) (string, []pricedItinerary, map[string]string, error) {
	originCode := mapSearchCityCode(origin)
	destCode := mapSearchCityCode(destination)
	body := map[string]any{
		"TravelPreference": map[string]string{"cabinType": c.cabin},
		"OriginDestinationInformations": []map[string]string{
			{
				"departureDateTime":       day.Format("2006-01-02") + "T00:00:00",
				"destinationLocationCode": destCode,
				"destinationType":         "City",
				"originLocationCode":      originCode,
				"originType":              "City",
			},
		},
		"adultCount":  c.adult,
		"childCount":  0,
		"infantCount": 0,
	}
	var out searchResponse
	searchPath := c.searchPagePath(originCode, destCode, day)
	if err := c.doJSON(ctx, http.MethodPost, "/flight/search", body, &out, searchPath); err != nil {
		return "", nil, nil, err
	}
	searchID := ""
	if out.SearchID > 0 {
		searchID = strconv.FormatInt(out.SearchID, 10)
	}
	airlineNames := airlineNameMap(out.AdditionalData)
	return searchID, out.PricedItineraries, airlineNames, nil
}

func airlineNameMap(data *additionalData) map[string]string {
	out := map[string]string{}
	if data == nil {
		return out
	}
	for _, a := range data.Airlines {
		code := strings.ToUpper(strings.TrimSpace(a.Iata))
		if code == "" {
			continue
		}
		name := firstNonEmpty(a.Name, a.NameLocal, code)
		out[code] = name
	}
	return out
}

func (c *Client) pollSearch(ctx context.Context, searchID string) ([]pricedItinerary, error) {
	path := "/flight/search/" + searchID
	for i := 0; i < c.maxPolls; i++ {
		var out searchResponse
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &out, c.referer); err != nil {
			return nil, err
		}
		if len(out.PricedItineraries) > 0 {
			return out.PricedItineraries, nil
		}
		if err := sleep(ctx, c.pollInterval); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("poll timeout for search %s", searchID)
}

func (c *Client) searchPagePath(origin, destination string, day time.Time) string {
	return fmt.Sprintf(
		"%s/flight/search?departure=%s,1&arrival=%s,1&departureDate=%s&adt=%d&chd=0&inf=0&cabin=1&isAnyWhere=false",
		strings.TrimRight(c.referer, "/"),
		mapSearchCityCode(origin), mapSearchCityCode(destination), day.Format("2006-01-02"), c.adult,
	)
}

// mapSearchCityCode translates internal route query codes to FlyToday city
// location codes used with originType/destinationType = "City".
func mapSearchCityCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THRALL", "IKA", "THR":
		return "thr"
	case "ISTALL", "IST", "SAW":
		return "ist"
	case "DXBALL", "DXB":
		return "dxb"
	case "NJFALL", "NJF":
		return "njf"
	case "AYTALL", "AYT":
		return "ayt"
	case "ESB":
		return "ank"
	case "GYD":
		return "bak"
	case "PEK", "PKX":
		return "bjs"
	case "PVG":
		return "sha"
	case "SVO":
		return "mow"
	default:
		return strings.ToLower(strings.TrimSpace(code))
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any, xPath string) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = b
	}

	if err := c.breaker.beforeRequest(); err != nil {
		return err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	if err := c.breaker.beforeRequest(); err != nil {
		return err
	}

	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "fa")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.originHeader)
	req.Header.Set("Referer", c.referer)
	if xPath != "" {
		req.Header.Set("x-path", xPath)
	}
	req.Header.Set("x-app", c.requestSource)
	req.Header.Set("x-currency", "IRR")
	req.Header.Set("x-origin", c.originHeader)
	req.Header.Set("x-proxy-cache", "no-store")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusConflict {
			if retryAfter <= 0 {
				retryAfter = defaultRateLimitPause
			}
			openUntil := c.breaker.recordRateLimit(retryAfter)
			if until := time.Until(openUntil); until > retryAfter {
				retryAfter = until
			}
		}
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       path,
			Body:       truncateBody(string(data)),
			RetryAfter: retryAfter,
		}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	c.breaker.recordSuccess()
	return nil
}

func mapItinerary(originQ, destQ string, day time.Time, it pricedItinerary, airlineNames map[string]string) (flight.Flight, bool) {
	if len(it.OriginDestinationOptions) == 0 || len(it.OriginDestinationOptions[0].FlightSegments) == 0 {
		return flight.Flight{}, false
	}
	odo := it.OriginDestinationOptions[0]
	segments := odo.FlightSegments
	first := segments[0]
	last := segments[len(segments)-1]

	departAt, err1 := parseAPITime(firstNonEmpty(first.DepartureDateTime, it.RouteInfo.DepartureDateTime))
	arriveAt, err2 := parseAPITime(last.ArrivalDateTime)
	if err1 != nil || err2 != nil {
		return flight.Flight{}, false
	}

	code := strings.ToUpper(strings.TrimSpace(firstNonEmpty(it.ValidatingAirlineCode, first.MarketingAirlineCode)))
	num := strings.TrimSpace(first.FlightNumber)
	name := airlineNames[code]
	if name == "" {
		name = code
	}

	label, kg := baggageFromSegment(first)
	stops := len(segments) - 1
	for _, seg := range segments {
		stops += seg.StopQuantity
	}

	price := int64(it.AirItineraryPricingInfo.ItinTotalFare.TotalFare)
	var dur *int
	if odo.JourneyDurationPerMinute > 0 {
		v := odo.JourneyDurationPerMinute
		dur = &v
	}

	origin := strings.ToUpper(firstNonEmpty(first.DepartureAirportLocationCode, it.RouteInfo.OriginLocationCode))
	dest := strings.ToUpper(firstNonEmpty(last.ArrivalAirportLocationCode, it.RouteInfo.DestinationLocationCode))

	return flight.Flight{
		Origin:           origin,
		Destination:      dest,
		OriginQuery:      strings.ToUpper(originQ),
		DestinationQuery: strings.ToUpper(destQ),
		FlightDate:       day,
		AirlineCode:      code,
		AirlineName:      name,
		FlightNumber:     num,
		FlightNumberNorm: NormalizeFlightNumber(code, num),
		DepartAt:         departAt,
		ArriveAt:         arriveAt,
		DepartMinute:     departAt.Hour()*60 + departAt.Minute(),
		ArriveMinute:     arriveAt.Hour()*60 + arriveAt.Minute(),
		BaggageLabel:     label,
		BaggageKG:        kg,
		PriceRial:        price,
		PriceToman:       price / 10,
		IsCharter:        it.IsCharter,
		Stops:            stops,
		DurationMin:      dur,
		ProposalID:       it.FareSourceCode,
		OfferURL:         it.OfferLink,
	}, true
}

func baggageFromSegment(seg flightSegment) (string, *int) {
	label := firstNonEmpty(seg.BaggageLocal, seg.Baggage)
	if label == "" {
		return "", nil
	}
	kg := extractInt(label)
	return label, kg
}

var digitsOnly = regexp.MustCompile(`\D+`)

func NormalizeFlightNumber(airlineCode, flightNumber string) string {
	code := strings.ToUpper(strings.TrimSpace(airlineCode))
	raw := strings.ToUpper(strings.TrimSpace(flightNumber))
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return ""
	}
	if code != "" && strings.HasPrefix(raw, code) {
		return raw
	}
	digits := digitsOnly.ReplaceAllString(raw, "")
	if code != "" && digits != "" {
		return code + digits
	}
	return raw
}

func extractInt(s string) *int {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	n, err := strconv.Atoi(b.String())
	if err != nil {
		return nil
	}
	return &n
}

func parseAPITime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		loc = time.FixedZone("IRST", 3*3600+30*60)
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if strings.Contains(layout, "Z07") || layout == time.RFC3339 {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if len(s) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02T15:04:05", s[:19], loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncateBody(s string) string {
	if len(s) > 512 {
		return s[:512]
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type requestLimiter struct {
	mu       sync.Mutex
	rps      float64
	burst    float64
	tokens   float64
	lastFill time.Time
}

func newRequestLimiter(rps float64, burst int) *requestLimiter {
	if rps <= 0 {
		rps = 0.2
	}
	if burst < 1 {
		burst = 1
	}
	now := time.Now()
	return &requestLimiter{rps: rps, burst: float64(burst), tokens: float64(burst), lastFill: now}
}

func (l *requestLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(l.lastFill).Seconds()
		if elapsed > 0 {
			l.tokens += elapsed * l.rps
			if l.tokens > l.burst {
				l.tokens = l.burst
			}
			l.lastFill = now
		}
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration(((1 - l.tokens) / l.rps) * float64(time.Second))
		l.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

type circuitBreaker struct {
	mu            sync.Mutex
	threshold     int
	openFor       time.Duration
	recent429     int
	lastRateLimit time.Time
	openUntil     time.Time
}

func newCircuitBreaker(threshold int, openFor time.Duration) *circuitBreaker {
	if threshold < 1 {
		threshold = 3
	}
	if openFor <= 0 {
		openFor = 10 * time.Minute
	}
	return &circuitBreaker{threshold: threshold, openFor: openFor}
}

func (b *circuitBreaker) beforeRequest() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.openUntil.IsZero() || !now.Before(b.openUntil) {
		if !b.openUntil.IsZero() {
			b.openUntil = time.Time{}
			b.recent429 = 0
			b.lastRateLimit = time.Time{}
		}
		return nil
	}
	return &CircuitOpenError{
		RetryAfter: time.Until(b.openUntil),
		OpenUntil:  b.openUntil,
	}
}

func (b *circuitBreaker) recordRateLimit(retryAfter time.Duration) time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if !b.lastRateLimit.IsZero() && now.Sub(b.lastRateLimit) > b.openFor {
		b.recent429 = 0
	}
	b.lastRateLimit = now
	b.recent429++
	if b.recent429 < b.threshold {
		return b.openUntil
	}
	pause := b.openFor
	if retryAfter > pause {
		pause = retryAfter
	}
	if pause < defaultRateLimitPause {
		pause = defaultRateLimitPause
	}
	candidate := now.Add(pause)
	if candidate.After(b.openUntil) {
		b.openUntil = candidate
	}
	return b.openUntil
}

func (b *circuitBreaker) openRemaining() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() || !time.Now().Before(b.openUntil) {
		return 0
	}
	return time.Until(b.openUntil)
}

func (b *circuitBreaker) recordSuccess() {
	b.mu.Lock()
	if !b.lastRateLimit.IsZero() && time.Since(b.lastRateLimit) > b.openFor {
		b.recent429 = 0
		b.lastRateLimit = time.Time{}
	}
	b.mu.Unlock()
}

func (b *circuitBreaker) state() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := map[string]any{
		"threshold":  b.threshold,
		"recent_429": b.recent429,
		"open":       false,
	}
	if !b.openUntil.IsZero() && time.Now().Before(b.openUntil) {
		state["open"] = true
		state["open_until"] = b.openUntil.UTC().Format(time.RFC3339Nano)
		state["remaining"] = time.Until(b.openUntil).Round(time.Second).String()
	}
	return state
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
