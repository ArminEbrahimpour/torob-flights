package trip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/offerurl"
)

const (
	defaultBaseURL   = "https://gateway-v2.trip.ir/api"
	defaultSiteURL   = "https://www.trip.ir"
	defaultPageSize  = 30
	defaultRatePause = 5 * time.Minute
	accessPath       = "/v1/lowFareSearch/access"
	searchPath       = "/v1/lowFareSearch"
	contextPath      = "/v1/app/context"
)

type Options struct {
	Adult                   int
	CabinType               string
	PageSize                int
	RPS                     float64
	Burst                   int
	HTTPTimeout             time.Duration
	CircuitBreakerThreshold int
	CircuitBreakerOpen      time.Duration
	BaseURL                 string
	SiteURL                 string
	Cookie                  string
}

type Client struct {
	baseURL    string
	siteURL    string
	cookie     string
	pageSize   int
	http       *http.Client
	adult      int
	cabin      string
	limiter    *requestLimiter
	breaker    *circuitBreaker
	sessionMu  sync.Mutex
	sessionAt  time.Time
	sessionKey string
}

func New(opts Options) *Client {
	if opts.Adult < 1 {
		opts.Adult = 1
	}
	if opts.CabinType == "" {
		opts.CabinType = "ECONOMY"
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultPageSize
	}
	if opts.RPS <= 0 {
		opts.RPS = 0.15
	}
	if opts.Burst < 1 {
		opts.Burst = 1
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 90 * time.Second
	}
	baseURL := strings.TrimRight(firstNonEmpty(opts.BaseURL, defaultBaseURL), "/")
	siteURL := strings.TrimRight(firstNonEmpty(opts.SiteURL, defaultSiteURL), "/")
	jar, _ := cookiejar.New(nil)
	return &Client{
		baseURL:  baseURL,
		siteURL:  siteURL,
		cookie:   strings.TrimSpace(opts.Cookie),
		pageSize: opts.PageSize,
		http: &http.Client{
			Timeout: opts.HTTPTimeout,
			Jar:     jar,
			Transport: &http.Transport{
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		adult:   opts.Adult,
		cabin:   strings.ToUpper(strings.TrimSpace(opts.CabinType)),
		limiter: newRequestLimiter(opts.RPS, opts.Burst),
		breaker: newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
	}
}

type accessResponse struct {
	Access string `json:"access"`
}

type searchResponse struct {
	SearchID string         `json:"searchId"`
	Total    int            `json:"total"`
	Result   []flightResult `json:"result"`
}

type flightResult struct {
	Hash       int64      `json:"hash"`
	TripID     string     `json:"tripId"`
	OfferLink  string     `json:"-"`
	IsDomestic bool       `json:"isDomestic"`
	Fare       fareInfo   `json:"fare"`
	OutBound   tripLegSet `json:"outBound"`
}

func (item *flightResult) UnmarshalJSON(data []byte) error {
	type alias flightResult
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*item = flightResult(decoded)
	item.OfferLink = offerurl.FromJSON(data)
	return nil
}

type fareInfo struct {
	TotalPrice int64 `json:"totalPrice"`
	NetPrice   int64 `json:"netPrice"`
}

type tripLegSet struct {
	FlightType    string    `json:"flightType"`
	JourneyDur    string    `json:"journeyDuration"`
	Baggage       string    `json:"baggage"`
	RemainingSeat int       `json:"remainingSeats"`
	Legs          []tripLeg `json:"legs"`
}

type tripLeg struct {
	FlightNumber      string      `json:"flightNumber"`
	DepartureDateTime string      `json:"departureDateTime"`
	LandingDateTime   string      `json:"landingDateTime"`
	Origin            airportInfo `json:"origin"`
	Destination       airportInfo `json:"destination"`
	OperatingAirline  airlineInfo `json:"operatingAirline"`
	Airline           airlineInfo `json:"airline"`
}

type airportInfo struct {
	AirportIATA string `json:"airportIATA"`
	CityIATA    string `json:"cityIata"`
}

type airlineInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
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
	return fmt.Sprintf("Trip circuit breaker is open until %s (retry after %s)",
		e.OpenUntil.UTC().Format(time.RFC3339), e.RetryAfter.Round(time.Second))
}

func IsCircuitOpen(err error) bool {
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests
}

func IsUnauthorized(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusForbidden
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

func (c *Client) CollectDay(ctx context.Context, origin, destination string, day time.Time) (string, []flight.Flight, error) {
	if !IsInternationalRoute(origin, destination) {
		return "", nil, fmt.Errorf("trip collector only supports international routes: %s-%s", origin, destination)
	}
	pageOrigin := mapPageCode(origin)
	pageDest := mapPageCode(destination)
	bodyOrigin := mapBodyAirport(origin)
	bodyDest := mapBodyAirport(destination)
	referer := c.refererURL(pageOrigin, pageDest, day)
	if err := c.ensureSession(ctx, referer); err != nil {
		return "", nil, err
	}

	body := c.searchBody(bodyOrigin, bodyDest, day)

	searchID := ""
	skip := 0
	var all []flightResult
	for {
		apiQuery := url.Values{}
		apiQuery.Set("searchId", searchID)
		apiQuery.Set("skip", strconv.Itoa(skip))
		apiQuery.Set("take", strconv.Itoa(c.pageSize))

		access, err := c.getAccess(ctx, apiQuery, body, referer)
		if err != nil {
			if IsUnauthorized(err) {
				c.invalidateSession()
			}
			return searchID, nil, err
		}

		var out searchResponse
		if err := c.search(ctx, apiQuery, body, referer, access, &out); err != nil {
			if IsUnauthorized(err) {
				c.invalidateSession()
			}
			return searchID, nil, err
		}
		if out.SearchID != "" {
			searchID = out.SearchID
		}
		all = append(all, out.Result...)
		if len(out.Result) == 0 || skip+c.pageSize >= out.Total {
			break
		}
		skip += c.pageSize
	}

	flights := make([]flight.Flight, 0, len(all))
	for _, item := range all {
		if item.IsDomestic {
			continue
		}
		if mapped, ok := mapResult(origin, destination, day, item); ok {
			flights = append(flights, mapped)
		}
	}
	return searchID, flights, nil
}

func (c *Client) ensureSession(ctx context.Context, referer string) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionKey == referer && !c.sessionAt.IsZero() && time.Since(c.sessionAt) < 20*time.Minute {
		return nil
	}
	if err := c.warmup(ctx, referer); err != nil {
		return err
	}
	if err := c.doJSON(ctx, http.MethodGet, contextPath, nil, nil, nil, referer); err != nil {
		return err
	}
	c.sessionKey = referer
	c.sessionAt = time.Now()
	return nil
}

func (c *Client) invalidateSession() {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	c.sessionAt = time.Time{}
	c.sessionKey = ""
}

func (c *Client) warmup(ctx context.Context, searchURL string) error {
	if err := c.breaker.beforeRequest(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return err
	}
	c.setCommonHeaders(req, searchURL)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return &HTTPError{StatusCode: resp.StatusCode, Method: http.MethodGet, Path: searchURL, Body: "warmup failed"}
	}
	c.breaker.recordSuccess()
	return nil
}

func (c *Client) getAccess(ctx context.Context, apiQuery url.Values, body map[string]any, referer string) (string, error) {
	var out accessResponse
	if err := c.doJSON(ctx, http.MethodPost, accessPath, apiQuery, body, &out, referer); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Access) == "" {
		return "", fmt.Errorf("trip access token missing")
	}
	return out.Access, nil
}

func (c *Client) search(ctx context.Context, apiQuery url.Values, body map[string]any, referer, access string, out *searchResponse) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(searchPath, apiQuery), mustJSON(body))
	if err != nil {
		return err
	}
	c.setCommonHeaders(req, referer)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-Search-Access", access)
	return c.roundTrip(ctx, req, out)
}

func (c *Client) searchBody(origin, destination string, day time.Time) map[string]any {
	return map[string]any{
		"flightInformation": []map[string]string{
			{
				"departDate":    day.Format("2006-01-02"),
				"originAirport": origin,
				"destAirport":   destination,
			},
		},
		"noOfAdults":     c.adult,
		"noOfChildren":   0,
		"noOfInfants":    0,
		"cabinClassType": c.cabin,
	}
}

func (c *Client) pageQuery(origin, destination string, day time.Time) url.Values {
	q := url.Values{}
	q.Set("origin", origin)
	q.Set("destination", destination)
	q.Set("outbound", day.Format("2006-01-02"))
	q.Set("adults", strconv.Itoa(c.adult))
	q.Set("children", "0")
	q.Set("infants", "0")
	q.Set("cabin", strings.ToLower(c.cabin))
	return q
}

func (c *Client) refererURL(origin, destination string, day time.Time) string {
	return fmt.Sprintf("%s/flight/booking/search?%s", c.siteURL, c.pageQuery(origin, destination, day).Encode())
}

func (c *Client) apiURL(path string, query url.Values) string {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return target
}

func mustJSON(v any) io.Reader {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return bytes.NewReader(b)
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any, referer string) error {
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
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path, query), rdr)
	if err != nil {
		return err
	}
	if referer == "" {
		referer = c.siteURL + "/flight/booking/search"
	}
	c.setCommonHeaders(req, referer)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}

	return c.roundTrip(ctx, req, out)
}

func (c *Client) roundTrip(ctx context.Context, req *http.Request, out any) error {
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
		if resp.StatusCode == http.StatusTooManyRequests {
			if retryAfter <= 0 {
				retryAfter = defaultRatePause
			}
			openUntil := c.breaker.recordRateLimit(retryAfter)
			if until := time.Until(openUntil); until > retryAfter {
				retryAfter = until
			}
		}
		path := req.URL.Path
		if req.URL.RawQuery != "" {
			path += "?" + req.URL.RawQuery
		}
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     req.Method,
			Path:       path,
			Body:       truncateBody(string(data)),
			RetryAfter: retryAfter,
		}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", req.Method, req.URL.Path, err)
		}
	}
	c.breaker.recordSuccess()
	return nil
}

func (c *Client) setCommonHeaders(req *http.Request, referer string) {
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("Accept-Language", "fa,en;q=0.8")
	req.Header.Set("Origin", c.siteURL)
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("X-TWA-User", "false")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
}

func mapResult(originQ, destQ string, day time.Time, item flightResult) (flight.Flight, bool) {
	if len(item.OutBound.Legs) == 0 || item.OutBound.RemainingSeat == -1 {
		return flight.Flight{}, false
	}
	legs := item.OutBound.Legs
	first := legs[0]
	last := legs[len(legs)-1]

	departAt, err1 := parseAPITime(firstNonEmpty(first.DepartureDateTime))
	arriveAt, err2 := parseAPITime(firstNonEmpty(last.LandingDateTime))
	if err1 != nil || err2 != nil {
		return flight.Flight{}, false
	}

	airline := first.OperatingAirline
	if strings.TrimSpace(airline.Code) == "" {
		airline = first.Airline
	}
	code := strings.ToUpper(strings.TrimSpace(airline.Code))
	name := firstNonEmpty(airline.Name, code)
	num := strings.TrimSpace(first.FlightNumber)

	priceToman := item.Fare.TotalPrice
	if priceToman <= 0 {
		priceToman = item.Fare.NetPrice
	}

	stops := len(legs) - 1
	dur := parseDurationMinutes(item.OutBound.JourneyDur)
	label, kg := baggageFromLabel(item.OutBound.Baggage)

	origin := strings.ToUpper(firstNonEmpty(first.Origin.AirportIATA, first.Origin.CityIATA))
	dest := strings.ToUpper(firstNonEmpty(last.Destination.AirportIATA, last.Destination.CityIATA))

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
		PriceRial:        priceToman * 10,
		PriceToman:       priceToman,
		IsCharter:        strings.EqualFold(item.OutBound.FlightType, "CHARTER"),
		Stops:            stops,
		DurationMin:      dur,
		ProposalID:       firstNonEmpty(strconv.FormatInt(item.Hash, 10), item.TripID),
		OfferURL:         item.OfferLink,
	}, true
}

var iranAirports = map[string]struct{}{
	"IKA": {}, "MHD": {}, "AWZ": {}, "SYZ": {}, "TBZ": {}, "BND": {}, "IFN": {},
	"ZBR": {}, "GBT": {}, "THR": {}, "THRALL": {}, "KIH": {}, "NSH": {}, "RAS": {},
}

func IsInternationalRoute(origin, destination string) bool {
	origin = strings.ToUpper(strings.TrimSpace(origin))
	destination = strings.ToUpper(strings.TrimSpace(destination))
	_, originIR := iranAirports[origin]
	_, destIR := iranAirports[destination]
	return originIR != destIR
}

func mapPageCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THRALL", "IKA", "THR":
		return "THR"
	case "ISTALL":
		return "IST"
	case "DXBALL":
		return "DXB"
	case "NJFALL":
		return "NJF"
	case "AYTALL":
		return "AYT"
	default:
		return strings.ToUpper(strings.TrimSpace(code))
	}
}

func mapBodyAirport(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THRALL", "THR":
		return "THR"
	case "ISTALL":
		return "IST"
	case "DXBALL":
		return "DXB"
	case "NJFALL":
		return "NJF"
	case "AYTALL":
		return "AYT"
	default:
		return strings.ToUpper(strings.TrimSpace(code))
	}
}

func baggageFromLabel(label string) (string, *int) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", nil
	}
	return label, extractInt(label)
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

func parseDurationMinutes(v string) *int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ":")
	if len(parts) < 2 {
		return nil
	}
	hours, err1 := strconv.Atoi(parts[0])
	minutes, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hours < 0 || minutes < 0 || minutes > 59 {
		return nil
	}
	total := hours*60 + minutes
	return &total
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
		rps = 0.15
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
	pause := b.openFor
	if retryAfter > pause {
		pause = retryAfter
	}
	if pause < defaultRatePause {
		pause = defaultRatePause
	}
	candidate := now.Add(pause)
	if candidate.After(b.openUntil) {
		b.openUntil = candidate
	}
	return b.openUntil
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
