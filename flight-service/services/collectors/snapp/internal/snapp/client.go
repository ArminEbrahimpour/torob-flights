package snapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
)

const (
	defaultBaseURL   = "https://ift.snapptrip.com"
	defaultReferer   = "https://flights.snapp.ir/international/"
	defaultOrigin    = "https://flights.snapp.ir"
	searchPathOneWay = "/api/listing/v1/one-way/search"
	defaultSource    = "searchBox"
)

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
}

type Client struct {
	http          *http.Client
	baseURL       string
	referer       string
	originHeader  string
	requestSource string
	cookie        string
	adult         int
	cabinType     string
	limiter       *requestLimiter
	breaker       *circuitBreaker
}

func New(opts Options) *Client {
	if opts.Adult < 1 {
		opts.Adult = 1
	}
	if opts.RPS <= 0 {
		opts.RPS = 0.15
	}
	if opts.Burst < 1 {
		opts.Burst = 1
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 60 * time.Second
	}
	baseURL := strings.TrimRight(firstNonEmpty(opts.BaseURL, defaultBaseURL), "/")
	referer := firstNonEmpty(opts.Referer, defaultReferer)
	originHeader := firstNonEmpty(opts.OriginHeader, defaultOrigin)
	requestSource := firstNonEmpty(opts.RequestSource, defaultSource)
	return &Client{
		http: &http.Client{
			Timeout: opts.HTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		baseURL:       baseURL,
		referer:       referer,
		originHeader:  originHeader,
		requestSource: requestSource,
		cookie:        strings.TrimSpace(opts.Cookie),
		adult:         opts.Adult,
		cabinType:     normalizeCabin(opts.CabinType),
		limiter:       newRequestLimiter(opts.RPS, opts.Burst),
		breaker:       newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
	}
}

type searchEndpoint struct {
	code   string
	isCity bool
}

type searchRequest struct {
	Origin            string `json:"origin"`
	Destination       string `json:"destination"`
	OriginIsCity      bool   `json:"originIsCity"`
	DestinationIsCity bool   `json:"destinationIsCity"`
	AdultCount        int    `json:"adultCount"`
	ChildCount        int    `json:"childCount"`
	InfantCount       int    `json:"infantCount"`
	DepartureDate     string `json:"departureDate"`
	CabinType         string `json:"cabinType"`
}

type searchResponse struct {
	Airfares []airfare `json:"airfares"`
}

type airfare struct {
	ID      string  `json:"id"`
	Pricing pricing `json:"pricing"`
	Routes  []route `json:"routes"`
}

type pricing struct {
	PricingDetails []pricingDetail `json:"pricingDetails"`
}

type pricingDetail struct {
	PassengerType string `json:"passengerType"`
	PayablePrice  int64  `json:"payablePrice"`
	Quantity      int    `json:"quantity"`
}

type route struct {
	Segments                 []segment `json:"segments"`
	TotalJourneyDuration     int       `json:"totalJourneyDuration"`
	TotalConnectionsDuration int       `json:"totalConnectionsDuration"`
}

type segment struct {
	DepartureDateTime    string         `json:"departureDateTime"`
	ArrivalDateTime      string         `json:"arrivalDateTime"`
	FlightNumber         string         `json:"flightNumber"`
	NumberOfStops        int            `json:"numberOfStops"`
	JourneyDuration      int            `json:"journeyDuration"`
	IsCharter            bool           `json:"isCharter"`
	OperatorAirlineInfo  airlineInfo    `json:"operatorAirlineInfo"`
	CarrierAirlineInfo   airlineInfo    `json:"carrierAirlineInfo"`
	DepartureAirportInfo airportInfo    `json:"departureAirportInfo"`
	ArrivalAirportInfo   airportInfo    `json:"arrivalAirportInfo"`
	BaggageDetail        *baggageDetail `json:"baggageDetail"`
}

type airlineInfo struct {
	AirlineCode string `json:"airlineCode"`
	AirlineName string `json:"airlineName"`
}

type airportInfo struct {
	AirportCode string `json:"airportCode"`
	CityName    string `json:"cityName"`
}

type baggageDetail struct {
	BaggageInfoType string `json:"baggageInfoType"`
	Weight          int    `json:"weight"`
	Count           int    `json:"count"`
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
	return fmt.Sprintf("Snapp circuit breaker is open until %s (retry after %s)",
		e.OpenUntil.UTC().Format(time.RFC3339), e.RetryAfter.Round(time.Second))
}

func IsCircuitOpen(err error) bool {
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusConflict:
			return true
		}
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

func (c *Client) CollectDay(ctx context.Context, originQ, destinationQ string, day time.Time) (string, []flight.Flight, error) {
	origins := resolveSearchEndpoints(originQ)
	destinations := resolveSearchEndpoints(destinationQ)

	var requestIDs []string
	var out []flight.Flight
	for _, origin := range origins {
		for _, destination := range destinations {
			if origin.code == destination.code && !origin.isCity && !destination.isCity {
				continue
			}
			rid, airfares, err := c.searchDay(ctx, origin, destination, day)
			if rid != "" {
				requestIDs = append(requestIDs, rid)
			}
			if err != nil {
				return strings.Join(requestIDs, ","), nil, err
			}
			out = append(out, mapAirfares(originQ, destinationQ, day, airfares)...)
		}
	}
	return strings.Join(requestIDs, ","), dedupeFlights(out), nil
}

func (c *Client) searchDay(ctx context.Context, origin, destination searchEndpoint, day time.Time) (string, []airfare, error) {
	body := searchRequest{
		Origin:            strings.ToUpper(origin.code),
		Destination:       strings.ToUpper(destination.code),
		OriginIsCity:      origin.isCity,
		DestinationIsCity: destination.isCity,
		AdultCount:        c.adult,
		ChildCount:        0,
		InfantCount:       0,
		DepartureDate:     day.Format("2006-01-02"),
		CabinType:         c.cabinType,
	}

	var resp searchResponse
	path := searchPathOneWay + "?" + url.Values{"source": {c.requestSource}}.Encode()
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		return "", nil, err
	}
	requestID := fmt.Sprintf("%s-%s-%s", body.Origin, body.Destination, body.DepartureDate)
	return requestID, resp.Airfares, nil
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
	req.Header.Set("Origin", c.originHeader)
	req.Header.Set("Referer", c.referer)
	req.Header.Set("channel", "web")
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
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusConflict {
			openUntil := c.breaker.recordRateLimit(retryAfter)
			if until := time.Until(openUntil); until > retryAfter {
				retryAfter = until
			}
			if retryAfter <= 0 {
				retryAfter = 2 * time.Minute
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
	if err := decodeSearchResponse(data, out); err != nil {
		return fmt.Errorf("decode %s: %w; body=%s", path, err, truncate(string(data), 300))
	}
	return nil
}

func decodeSearchResponse(data []byte, out any) error {
	if err := json.Unmarshal(data, out); err == nil {
		if resp, ok := out.(*searchResponse); ok && len(resp.Airfares) > 0 {
			return nil
		}
		if resp, ok := out.(*searchResponse); ok && len(resp.Airfares) == 0 {
			var wrapped struct {
				Data searchResponse `json:"data"`
			}
			if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Data.Airfares) > 0 {
				*resp = wrapped.Data
				return nil
			}
		}
	}
	var wrapped struct {
		Data searchResponse `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	if resp, ok := out.(*searchResponse); ok {
		*resp = wrapped.Data
		return nil
	}
	return json.Unmarshal(data, out)
}

func mapAirfares(originQ, destQ string, day time.Time, airfares []airfare) []flight.Flight {
	var out []flight.Flight
	for _, fare := range airfares {
		priceRial, priceToman := adultPrice(fare.Pricing)
		for _, rt := range fare.Routes {
			for _, seg := range rt.Segments {
				if f, ok := mapSegment(originQ, destQ, day, fare.ID, priceRial, priceToman, rt, seg); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func mapSegment(originQ, destQ string, day time.Time, proposalID string, priceRial, priceToman int64, rt route, seg segment) (flight.Flight, bool) {
	departAt, err1 := parseAPITime(seg.DepartureDateTime)
	arriveAt, err2 := parseAPITime(seg.ArrivalDateTime)
	if err1 != nil || err2 != nil {
		return flight.Flight{}, false
	}

	code := strings.ToUpper(strings.TrimSpace(firstNonEmpty(
		seg.OperatorAirlineInfo.AirlineCode,
		seg.CarrierAirlineInfo.AirlineCode,
	)))
	name := firstNonEmpty(seg.OperatorAirlineInfo.AirlineName, seg.CarrierAirlineInfo.AirlineName)
	number := strings.TrimSpace(seg.FlightNumber)
	if number == "" {
		return flight.Flight{}, false
	}

	origin := strings.ToUpper(strings.TrimSpace(seg.DepartureAirportInfo.AirportCode))
	destination := strings.ToUpper(strings.TrimSpace(seg.ArrivalAirportInfo.AirportCode))
	if origin == "" || destination == "" {
		return flight.Flight{}, false
	}

	bagLabel, bagKG := baggage(seg.BaggageDetail)
	duration := segmentDuration(seg, rt)

	return flight.Flight{
		Origin:           origin,
		Destination:      destination,
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
		IsCharter:        seg.IsCharter,
		Stops:            seg.NumberOfStops,
		DurationMin:      duration,
		ProposalID:       proposalID,
	}, true
}

func adultPrice(p pricing) (rial int64, toman int64) {
	for _, d := range p.PricingDetails {
		if strings.EqualFold(d.PassengerType, "ADULT") && d.PayablePrice > 0 {
			rial = d.PayablePrice
			toman = d.PayablePrice / 10
			return rial, toman
		}
	}
	if len(p.PricingDetails) > 0 && p.PricingDetails[0].PayablePrice > 0 {
		rial = p.PricingDetails[0].PayablePrice
		toman = rial / 10
	}
	return rial, toman
}

func baggage(detail *baggageDetail) (string, *int) {
	if detail == nil || detail.Weight <= 0 {
		return "", nil
	}
	kg := detail.Weight
	return fmt.Sprintf("%d KG", kg), &kg
}

func segmentDuration(seg segment, rt route) *int {
	if seg.JourneyDuration > 0 {
		v := seg.JourneyDuration
		return &v
	}
	total := rt.TotalJourneyDuration + rt.TotalConnectionsDuration
	if total > 0 {
		return &total
	}
	return nil
}

func resolveSearchEndpoints(query string) []searchEndpoint {
	query = strings.ToUpper(strings.TrimSpace(query))
	switch query {
	case "THRALL":
		return []searchEndpoint{{code: "IKA", isCity: true}}
	case "ISTALL":
		return []searchEndpoint{{code: "IST", isCity: true}}
	default:
		expanded := expandQueryCode(query)
		out := make([]searchEndpoint, 0, len(expanded))
		for _, code := range expanded {
			out = append(out, searchEndpoint{code: code, isCity: false})
		}
		return out
	}
}

func expandQueryCode(code string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	switch code {
	case "THRALL":
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

func normalizeCabin(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "BUSINESS":
		return "BUSINESS"
	case "FIRST", "FIRSTCLASS", "FIRST_CLASS", "FIRST CLASS":
		return "FIRST"
	default:
		return "ECONOMY"
	}
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
		openFor = 15 * time.Minute
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
