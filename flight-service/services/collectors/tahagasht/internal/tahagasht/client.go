package tahagasht

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

const defaultBaseURL = "https://www.tahagasht.com"

type Options struct {
	Adult                   int
	FlightClass             string
	RPS                     float64
	Burst                   int
	HTTPTimeout             time.Duration
	UpstreamPollInterval    time.Duration
	MaxPolls                int
	CircuitBreakerThreshold int
	CircuitBreakerOpen      time.Duration
	BaseURL                 string // tests/local proxy; empty uses tahagasht.com
}

type Client struct {
	http         *http.Client
	baseURL      string
	adult        int
	class        string
	pollInterval time.Duration
	maxPolls     int
	limiter      *requestLimiter
	breaker      *circuitBreaker
}

func New(opts Options) *Client {
	if opts.Adult < 1 {
		opts.Adult = 1
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
	if opts.UpstreamPollInterval < 2500*time.Millisecond && opts.BaseURL == "" {
		opts.UpstreamPollInterval = 2500 * time.Millisecond
	}
	if opts.UpstreamPollInterval <= 0 {
		opts.UpstreamPollInterval = 2500 * time.Millisecond
	}
	if opts.MaxPolls < 1 {
		opts.MaxPolls = 20
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http: &http.Client{
			Timeout: opts.HTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        16,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		baseURL:      baseURL,
		adult:        opts.Adult,
		class:        normalizeCabin(opts.FlightClass),
		pollInterval: opts.UpstreamPollInterval,
		maxPolls:     opts.MaxPolls,
		limiter:      newRequestLimiter(opts.RPS, opts.Burst),
		breaker:      newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
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

type apiResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Items   struct {
		EchoToken    string `json:"echoToken"`
		UserID       string `json:"userId"`
		Success      bool   `json:"success"`
		SearchConfig struct {
			TransactionID     string `json:"transactionId"`
			Sequence          int    `json:"sequence"`
			TransactionStatus string `json:"transactionStatus"`
			FlightType        string `json:"flightType"`
		} `json:"searchConfig"`
		PricedItineraries []pricedItinerary `json:"pricedItineraries"`
	} `json:"items"`
}

type pricedItinerary struct {
	SequenceNumber           int                       `json:"sequenceNumber"`
	OriginDestinationOptions []originDestinationOption `json:"originDestinationOptions"`
	PricingInfo              pricingInfo               `json:"pricingInfo"`
	BookingData              bookingData               `json:"bookingData"`
	OfferLink                string                    `json:"-"`
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

type pricingInfo struct {
	ValidatingAirlineCode string `json:"validatingAirlineCode"`
	ItinTotalFare         struct {
		TotalFare moneyAmount `json:"totalFare"`
	} `json:"itinTotalFare"`
	PassengerFares []passengerFare `json:"passengerFares"`
}

type passengerFare struct {
	PassengerTypeCode string      `json:"passengerTypeCode"`
	TotalFare         moneyAmount `json:"totalFare"`
}

type moneyAmount struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type bookingData struct {
	AgencySupplierID string `json:"agencySupplierId"`
	SupplierName     string `json:"supplierName"`
	Data             string `json:"data"`
}

type originDestinationOption struct {
	FlightSegments []flightSegment `json:"flightSegments"`
}

type flightSegment struct {
	DepartureAirport airportRef `json:"departureAirport"`
	ArrivalAirport   airportRef `json:"arrivalAirport"`

	DepartureDateTime string `json:"departureDateTime"`
	ArrivalDateTime   string `json:"arrivalDateTime"`
	FlightNumber      string `json:"flightNumber"`
	NumberOfStops     int    `json:"numberOfStops"`
	Duration          string `json:"duration"`
	FareBasisCode     string `json:"fareBasisCode"`

	Airline struct {
		Marketing airlineRef `json:"marketing"`
		Operating airlineRef `json:"operating"`
	} `json:"airline"`

	FreeBaggage []struct {
		PassengerTypeCode string `json:"passengerTypeCode"`
		Amount            int    `json:"amount"`
		Unit              string `json:"unit"`
	} `json:"freeBaggage"`
}

type airportRef struct {
	Code string `json:"code"`
}

type airlineRef struct {
	Code  string     `json:"code"`
	Names []langName `json:"names"`
}

type langName struct {
	Language string `json:"language"`
	Name     string `json:"name"`
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
	return fmt.Sprintf("Tahagasht circuit breaker is open until %s (retry after %s)",
		e.OpenUntil.UTC().Format(time.RFC3339), e.RetryAfter.Round(time.Second))
}

func IsCircuitOpen(err error) bool {
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
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

// CollectDay keeps the job's route codes as origin_query/destination_query, but
// fans known city-group aliases out to concrete IATA airports before calling
// Tahagasht. Tahagasht's documented search accepts route+date, not flight number.
func (c *Client) CollectDay(ctx context.Context, originQ, destinationQ string, day time.Time) (string, []flight.Flight, error) {
	origins := expandQueryCode(originQ)
	destinations := expandQueryCode(destinationQ)

	var requestIDs []string
	var out []flight.Flight
	for _, origin := range origins {
		for _, destination := range destinations {
			if origin == destination {
				continue
			}
			rid, itineraries, err := c.collectConcreteDay(ctx, origin, destination, day)
			if rid != "" {
				requestIDs = append(requestIDs, rid)
			}
			if err != nil {
				return strings.Join(requestIDs, ","), nil, err
			}
			out = append(out, mapItineraries(originQ, destinationQ, day, itineraries)...)
		}
	}
	return strings.Join(requestIDs, ","), dedupeFlights(out), nil
}

func (c *Client) collectConcreteDay(ctx context.Context, origin, destination string, day time.Time) (string, []pricedItinerary, error) {
	transactionID, initial, status, err := c.createSearch(ctx, origin, destination, day)
	if err != nil {
		return "", nil, err
	}
	all := append([]pricedItinerary(nil), initial...)
	if strings.EqualFold(status, "End") {
		return transactionID, all, nil
	}
	polled, err := c.poll(ctx, transactionID, origin, destination, day)
	if err != nil {
		return transactionID, nil, err
	}
	all = append(all, polled...)
	return transactionID, all, nil
}

func (c *Client) createSearch(ctx context.Context, origin, destination string, day time.Time) (string, []pricedItinerary, string, error) {
	body := map[string]any{
		"cabinPref": []string{c.class},
		"originDestinationInformation": []map[string]any{{
			"departureDate":           day.Format("2006-01-02"),
			"origin":                  strings.ToUpper(origin),
			"destination":             strings.ToUpper(destination),
			"originMultiAirport":      false,
			"destinationMultiAirport": false,
		}},
		"searchConfig": map[string]any{
			"type":                 "Async",
			"reducedDataIndicator": true,
		},
		"travelers": []map[string]string{
			{"code": "ADT", "quantity": strconv.Itoa(c.adult)},
			{"code": "CHD", "quantity": "0"},
			{"code": "INF", "quantity": "0"},
		},
	}

	var resp apiResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/rest/airlowfaresearchasync", body, &resp); err != nil {
		return "", nil, "", err
	}
	if !strings.EqualFold(resp.Status, "success") || !resp.Items.Success {
		return "", nil, "", fmt.Errorf("Tahagasht search rejected: status=%q message=%q success=%v", resp.Status, resp.Message, resp.Items.Success)
	}
	tx := strings.TrimSpace(resp.Items.SearchConfig.TransactionID)
	if tx == "" {
		return "", nil, "", fmt.Errorf("Tahagasht returned empty transactionId")
	}
	return tx, resp.Items.PricedItineraries, resp.Items.SearchConfig.TransactionStatus, nil
}

func (c *Client) poll(ctx context.Context, transactionID, origin, destination string, day time.Time) ([]pricedItinerary, error) {
	var all []pricedItinerary
	for i := 0; i < c.maxPolls; i++ {
		if err := sleep(ctx, c.pollInterval); err != nil {
			return nil, err
		}
		body := map[string]any{
			"searchConfig": map[string]any{
				"type":                 "Async",
				"reducedDataIndicator": "true",
				"transactionId":        transactionID,
			},
			"fcMeta": map[string]any{
				"date":        day.Format("2006-01-02"),
				"date2":       nil,
				"source":      strings.ToUpper(origin),
				"destination": strings.ToUpper(destination),
				"roundtrip":   "F",
				"cabin":       c.class,
				"adult":       c.adult,
				"child":       0,
				"infant":      0,
			},
		}
		var resp apiResponse
		if err := c.doJSON(ctx, http.MethodPost, "/api/rest/airlowfaresearchasyncresult", body, &resp); err != nil {
			return nil, err
		}
		if !strings.EqualFold(resp.Status, "success") || !resp.Items.Success {
			return nil, fmt.Errorf("Tahagasht poll rejected: status=%q message=%q success=%v", resp.Status, resp.Message, resp.Items.Success)
		}
		all = append(all, resp.Items.PricedItineraries...)
		if strings.EqualFold(resp.Items.SearchConfig.TransactionStatus, "End") {
			return all, nil
		}
	}
	return all, fmt.Errorf("poll timeout for transaction %s", transactionID)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
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
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://www.tahagasht.com")
	req.Header.Set("Referer", "https://www.tahagasht.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")

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
			openUntil := c.breaker.recordRateLimit(retryAfter)
			if until := time.Until(openUntil); until > retryAfter {
				retryAfter = until
			}
		}
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       path,
			Body:       truncate(string(data), 400),
			RetryAfter: retryAfter,
		}
	}
	c.breaker.recordSuccess()
	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("empty response body from %s", path)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w; body=%s", path, err, truncate(string(data), 300))
	}
	return nil
}

func mapItineraries(originQ, destQ string, day time.Time, itineraries []pricedItinerary) []flight.Flight {
	var out []flight.Flight
	for _, itin := range itineraries {
		priceRial := itineraryPriceRial(itin)
		priceToman := priceRial / 10
		proposalID := itineraryProposalID(itin)
		for _, odo := range itin.OriginDestinationOptions {
			for _, seg := range odo.FlightSegments {
				if f, ok := mapSegment(originQ, destQ, day, seg, priceRial, priceToman, proposalID, itin.OfferLink); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func itineraryPriceRial(itin pricedItinerary) int64 {
	for _, pf := range itin.PricingInfo.PassengerFares {
		if !strings.EqualFold(pf.PassengerTypeCode, "ADT") {
			continue
		}
		if price := moneyToRial(pf.TotalFare); price > 0 {
			return price
		}
	}
	return moneyToRial(itin.PricingInfo.ItinTotalFare.TotalFare)
}

func moneyToRial(m moneyAmount) int64 {
	if m.Amount <= 0 {
		return 0
	}
	return int64(m.Amount)
}

func itineraryProposalID(itin pricedItinerary) string {
	if data := strings.TrimSpace(itin.BookingData.Data); data != "" {
		return data
	}
	if itin.SequenceNumber > 0 {
		return strconv.Itoa(itin.SequenceNumber)
	}
	return ""
}

func mapSegment(originQ, destQ string, day time.Time, seg flightSegment, priceRial, priceToman int64, proposalID, offerLink string) (flight.Flight, bool) {
	departAt, err1 := parseAPITime(seg.DepartureDateTime)
	arriveAt, err2 := parseAPITime(seg.ArrivalDateTime)
	if err1 != nil || err2 != nil {
		return flight.Flight{}, false
	}

	code := strings.ToUpper(strings.TrimSpace(firstNonEmpty(seg.Airline.Marketing.Code, seg.Airline.Operating.Code)))
	number := strings.TrimSuffix(strings.TrimSpace(seg.FlightNumber), ".")
	if number == "" {
		return flight.Flight{}, false
	}
	name := airlineName(seg.Airline.Operating)
	if name == "" {
		name = airlineName(seg.Airline.Marketing)
	}
	bagLabel, bagKG := baggage(seg)
	duration := parseDurationMinutes(seg.Duration)

	return flight.Flight{
		Origin:           strings.ToUpper(strings.TrimSpace(seg.DepartureAirport.Code)),
		Destination:      strings.ToUpper(strings.TrimSpace(seg.ArrivalAirport.Code)),
		OriginQuery:      strings.ToUpper(strings.TrimSpace(originQ)),
		DestinationQuery: strings.ToUpper(strings.TrimSpace(destQ)),
		FlightDate:       day,
		AirlineCode:      code,
		AirlineName:      name,
		FlightNumber:     number,
		FlightNumberNorm: NormalizeFlightNumber(code, number),
		DepartAt:         departAt,
		ArriveAt:         arriveAt,
		DepartMinute:     departAt.Hour()*60 + departAt.Minute(),
		ArriveMinute:     arriveAt.Hour()*60 + arriveAt.Minute(),
		BaggageLabel:     bagLabel,
		BaggageKG:        bagKG,
		PriceRial:        priceRial,
		PriceToman:       priceToman,
		IsCharter:        false,
		Stops:            seg.NumberOfStops,
		DurationMin:      duration,
		ProposalID:       proposalID,
		OfferURL:         offerLink,
	}, true
}

func dedupeFlights(in []flight.Flight) []flight.Flight {
	seen := make(map[string]struct{}, len(in))
	out := make([]flight.Flight, 0, len(in))
	for _, f := range in {
		key := strings.Join([]string{
			f.Origin, f.Destination, f.FlightDate.Format("2006-01-02"),
			f.AirlineCode, f.FlightNumber, f.DepartAt.Format(time.RFC3339Nano), f.ArriveAt.Format(time.RFC3339Nano),
			strconv.FormatInt(f.PriceRial, 10), f.BaggageLabel, f.ProposalID, strconv.FormatBool(f.IsCharter),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

func expandQueryCode(code string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	switch code {
	case "THRALL":
		// Tahagasht's international search must use IKA, not THR.
		return []string{"IKA"}
	case "ISTALL":
		return []string{"IST", "SAW"}
	case "DXBALL":
		return []string{"DXB"}
	case "NJFALL":
		return []string{"NJF"}
	case "AYTALL":
		return []string{"AYT"}
	default:
		if code == "" {
			return nil
		}
		return []string{code}
	}
}

func normalizeCabin(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "business":
		return "Business"
	case "first", "firstclass", "first_class", "first class":
		return "First"
	default:
		return "Economy"
	}
}

func airlineName(ref airlineRef) string {
	for _, preferred := range []string{"en", "fa"} {
		for _, n := range ref.Names {
			if strings.EqualFold(n.Language, preferred) && strings.TrimSpace(n.Name) != "" {
				return strings.TrimSpace(n.Name)
			}
		}
	}
	for _, n := range ref.Names {
		if strings.TrimSpace(n.Name) != "" {
			return strings.TrimSpace(n.Name)
		}
	}
	return ""
}

func baggage(seg flightSegment) (string, *int) {
	for _, b := range seg.FreeBaggage {
		if !strings.EqualFold(b.PassengerTypeCode, "ADT") || b.Amount <= 0 {
			continue
		}
		kg := b.Amount
		unit := strings.ToUpper(strings.TrimSpace(b.Unit))
		if unit == "" {
			unit = "KG"
		}
		return fmt.Sprintf("%d %s", kg, unit), &kg
	}
	return "", nil
}

func parseDurationMinutes(v string) *int {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) < 2 || len(parts) > 3 {
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

var digitsOnly = regexp.MustCompile(`\D+`)

func NormalizeFlightNumber(airlineCode, flightNumber string) string {
	code := strings.ToUpper(strings.TrimSpace(airlineCode))
	raw := strings.ToUpper(strings.TrimSpace(strings.TrimSuffix(flightNumber, ".")))
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

func parseAPITime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		loc = time.FixedZone("IRST", 3*3600+30*60)
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05.0000000-07:00",
	}
	for _, layout := range layouts {
		if strings.Contains(layout, "Z07") || strings.Contains(layout, "-07") || layout == time.RFC3339 {
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
	return &CircuitOpenError{RetryAfter: time.Until(b.openUntil), OpenUntil: b.openUntil}
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

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
