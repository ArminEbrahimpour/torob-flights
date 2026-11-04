package alibaba

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/airbar/flight-service/shared/pkg/offerurl"
)

const defaultBaseURL = "https://ws.alibaba.ir/api"

type Options struct {
	// BaseURL defaults to the Alibaba API. Tests point it at a local server.
	BaseURL string
	// AcceptExpiredCert forgives an expired server certificate and nothing else. See verifyServerCert.
	AcceptExpiredCert bool
	// RootCAs replaces the system roots. Tests use it. Nil means the system roots.
	RootCAs                 *x509.CertPool
	Adult                   int
	FlightClass             string
	RPS                     float64
	Burst                   int
	HTTPTimeout             time.Duration
	CircuitBreakerThreshold int
	CircuitBreakerOpen      time.Duration
}

type Client struct {
	baseURL string
	http    *http.Client
	adult   int
	class   string
	limiter *requestLimiter
	breaker *circuitBreaker
}

func New(opts Options) *Client {
	if opts.Adult < 1 {
		opts.Adult = 1
	}
	if opts.FlightClass == "" {
		opts.FlightClass = "economy"
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
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	switch {
	case opts.AcceptExpiredCert:
		if u, err := url.Parse(opts.BaseURL); err == nil && u.Hostname() != "" {
			transport.TLSClientConfig = expiredCertTLS(u.Hostname(), opts.RootCAs)
			transport.ForceAttemptHTTP2 = true
		}
	case opts.RootCAs != nil:
		transport.TLSClientConfig = &tls.Config{RootCAs: opts.RootCAs, MinVersion: tls.VersionTLS12}
		transport.ForceAttemptHTTP2 = true
	}
	return &Client{
		baseURL: opts.BaseURL,
		http: &http.Client{
			Timeout:   opts.HTTPTimeout,
			Transport: transport,
		},
		adult:   opts.Adult,
		class:   opts.FlightClass,
		limiter: newRequestLimiter(opts.RPS, opts.Burst),
		breaker: newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
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

type createResp struct {
	Result struct {
		RequestID            string `json:"requestId"`
		NextRequestThreshold int    `json:"nextRequestThreshold"`
	} `json:"result"`
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

type pollResp struct {
	Result struct {
		Proposals            []proposal `json:"proposals"`
		IsCompleted          bool       `json:"isCompleted"`
		NextRequestThreshold int        `json:"nextRequestThreshold"`
		RequestID            string     `json:"requestId"`
	} `json:"result"`
	Success bool `json:"success"`
}

type proposal struct {
	ProposalID string `json:"proposalId"`
	OfferLink  string `json:"-"`
	IsCharter  bool   `json:"isCharter"`
	Prices     []struct {
		PerPassenger float64 `json:"perPassenger"`
		Total        float64 `json:"total"`
	} `json:"prices"`
	LeavingFlightGroup *flightGroup `json:"leavingFlightGroup"`
}

func (p *proposal) UnmarshalJSON(data []byte) error {
	type alias proposal
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = proposal(decoded)
	p.OfferLink = offerurl.FromJSON(data)
	return nil
}

type flightGroup struct {
	AirlineName         string         `json:"airlineName"`
	Origin              string         `json:"origin"`
	Destination         string         `json:"destination"`
	NumberOfStop        int            `json:"numberOfStop"`
	DurationMin         int            `json:"durationMin"`
	LowestBaggageAmount string         `json:"lowestBaggageAmount"`
	DepartureDateTime   string         `json:"departureDateTime"`
	ArrivalDateTime     string         `json:"arrivalDateTime"`
	FlightDetails       []flightDetail `json:"flightDetails"`
	Baggage             []baggageInfo  `json:"baggage"`
}

type flightDetail struct {
	Origin            string   `json:"origin"`
	Destination       string   `json:"destination"`
	MarketingCarrier  string   `json:"marketingCarrier"`
	FlightNumber      string   `json:"flightNumber"`
	AirlineName       string   `json:"airlineName"`
	DepartureDateTime string   `json:"departureDateTime"`
	ArrivalDateTime   string   `json:"arrivalDateTime"`
	Baggage           []string `json:"baggage"`
}

type baggageInfo struct {
	BaggageAllowanceAmount int    `json:"baggageAllowanceAmount"`
	BaggageUnit            string `json:"baggageUnit"`
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
	return fmt.Sprintf("Alibaba circuit breaker is open until %s (retry after %s)",
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

func (c *Client) CollectDay(ctx context.Context, origin, destination string, day time.Time) (string, []flight.Flight, error) {
	rid, err := c.createSearch(ctx, origin, destination, day)
	if err != nil {
		return "", nil, err
	}
	proposals, err := c.poll(ctx, rid)
	if err != nil {
		return rid, nil, err
	}
	flights := make([]flight.Flight, 0, len(proposals))
	for _, p := range proposals {
		f, ok := mapProposal(origin, destination, day, p)
		if ok {
			flights = append(flights, f)
		}
	}
	return rid, flights, nil
}

func (c *Client) createSearch(ctx context.Context, origin, destination string, day time.Time) (string, error) {
	body := map[string]any{
		"origin":           strings.ToUpper(origin),
		"destination":      strings.ToUpper(destination),
		"departureDate":    day.Format("2006-01-02"),
		"adult":            c.adult,
		"child":            0,
		"infant":           0,
		"flightClass":      c.class,
		"isReIssueRequest": false,
	}
	var out createResp
	if err := c.doJSON(ctx, http.MethodPost, "/v1/flights/international/proposal-requests", body, &out); err != nil {
		return "", err
	}
	if out.Result.RequestID == "" {
		return "", fmt.Errorf("empty requestId: success=%v error=%v", out.Success, out.Error)
	}
	return out.Result.RequestID, nil
}

func (c *Client) poll(ctx context.Context, requestID string) ([]proposal, error) {
	var all []proposal
	for i := 0; i < 40; i++ {
		var out pollResp
		path := "/v1/flights/international/proposal-requests/" + requestID
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Result.Proposals...)
		if out.Result.IsCompleted {
			return all, nil
		}
		wait := time.Duration(out.Result.NextRequestThreshold) * time.Millisecond
		if wait <= 0 {
			wait = 500 * time.Millisecond
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	return all, fmt.Errorf("poll timeout for %s", requestID)
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

	// Every Alibaba request—both POST and GET—passes through the same
	// process-wide circuit breaker and token bucket.
	if err := c.breaker.beforeRequest(); err != nil {
		return err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	// The circuit may have opened while this goroutine was waiting for a token.
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
	req.Header.Set("Origin", "https://www.alibaba.ir")
	req.Header.Set("Referer", "https://www.alibaba.ir/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("ab-channel", "WEB-NEW,PRODUCTION,CSR,www.alibaba.ir,N,Chrome,120.0.0.0,N,N,Linux")

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
	return json.Unmarshal(data, out)
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
	// Count repeated 429s in a rolling window. A successful POST followed by a
	// blocked poll must not erase the evidence that the remote side is throttling us.
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

func mapProposal(originQ, destQ string, day time.Time, p proposal) (flight.Flight, bool) {
	g := p.LeavingFlightGroup
	if g == nil || len(g.FlightDetails) == 0 {
		return flight.Flight{}, false
	}
	d0 := g.FlightDetails[0]
	departAt, err1 := parseAPITime(firstNonEmpty(d0.DepartureDateTime, g.DepartureDateTime))
	arriveAt, err2 := parseAPITime(firstNonEmpty(d0.ArrivalDateTime, g.ArrivalDateTime))
	if err1 != nil || err2 != nil {
		return flight.Flight{}, false
	}
	code := strings.ToUpper(strings.TrimSpace(d0.MarketingCarrier))
	num := strings.TrimSpace(d0.FlightNumber)
	label, kg := baggage(g, d0)
	var price int64
	if len(p.Prices) > 0 {
		price = int64(p.Prices[0].PerPassenger)
	}
	var dur *int
	if g.DurationMin > 0 {
		v := g.DurationMin
		dur = &v
	}
	return flight.Flight{
		Origin:           firstNonEmpty(g.Origin, d0.Origin),
		Destination:      firstNonEmpty(g.Destination, d0.Destination),
		OriginQuery:      strings.ToUpper(originQ),
		DestinationQuery: strings.ToUpper(destQ),
		FlightDate:       day,
		AirlineCode:      code,
		AirlineName:      firstNonEmpty(g.AirlineName, d0.AirlineName),
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
		IsCharter:        p.IsCharter,
		Stops:            g.NumberOfStop,
		DurationMin:      dur,
		ProposalID:       p.ProposalID,
		OfferURL:         offerurl.SameSource("alibaba", p.OfferLink),
	}, true
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

func baggage(g *flightGroup, d flightDetail) (string, *int) {
	if g.LowestBaggageAmount != "" {
		kg := extractInt(g.LowestBaggageAmount)
		return g.LowestBaggageAmount, kg
	}
	if len(g.Baggage) > 0 && g.Baggage[0].BaggageAllowanceAmount > 0 {
		kg := g.Baggage[0].BaggageAllowanceAmount
		return fmt.Sprintf("%d کیلوگرم", kg), &kg
	}
	if len(d.Baggage) > 0 {
		kg := extractInt(d.Baggage[0])
		return d.Baggage[0], kg
	}
	return "", nil
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
