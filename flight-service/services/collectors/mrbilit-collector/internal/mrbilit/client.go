package mrbilit

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
)

const Source = "mrbilit"

var digitsOnly = regexp.MustCompile(`\D+`)

type Options struct {
	Adult                                                 int
	CabinType                                             string
	RPS                                                   float64
	Burst                                                 int
	HTTPTimeout, CircuitBreakerOpen                       time.Duration
	CircuitBreakerThreshold                               int
	BaseURL, Referer, OriginHeader, RequestSource, Cookie string
	PollInterval                                          time.Duration
}
type Client struct {
	baseURL, referer, origin, cookie string
	adult                            int
	http                             *http.Client
	limiter                          *limiter
	breaker                          *breaker
}

func New(o Options) *Client {
	if o.Adult < 1 {
		o.Adult = 1
	}
	if o.RPS <= 0 {
		o.RPS = .1
	}
	if o.Burst < 1 {
		o.Burst = 1
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 60 * time.Second
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://flight.atighgasht.com"
	}
	if o.Referer == "" {
		o.Referer = "https://mrbilit.com"
	}
	if o.CircuitBreakerThreshold < 1 {
		o.CircuitBreakerThreshold = 3
	}
	if o.CircuitBreakerOpen <= 0 {
		o.CircuitBreakerOpen = 10 * time.Minute
	}
	return &Client{baseURL: strings.TrimRight(o.BaseURL, "/"), referer: o.Referer, origin: o.OriginHeader, cookie: o.Cookie, adult: o.Adult, http: &http.Client{Timeout: o.HTTPTimeout}, limiter: newLimiter(o.RPS, o.Burst), breaker: newBreaker(o.CircuitBreakerThreshold, o.CircuitBreakerOpen)}
}

type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("mrbilit HTTP %d (retry after %s): %s", e.StatusCode, e.RetryAfter.Round(time.Second), truncate(e.Body, 300))
}

type circuitError struct{ until time.Time }

func (e *circuitError) Error() string {
	return fmt.Sprintf("mrbilit circuit open until %s", e.until.Format(time.RFC3339))
}
func IsRateLimited(err error) bool {
	var e *HTTPError
	return errors.As(err, &e) && (e.StatusCode == 429 || e.StatusCode == 403 || e.StatusCode == 409)
}
func IsCircuitOpen(err error) bool { var e *circuitError; return errors.As(err, &e) }
func RetryAfter(err error) time.Duration {
	var e *HTTPError
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	var c *circuitError
	if errors.As(err, &c) {
		return time.Until(c.until)
	}
	return 0
}
func (c *Client) CircuitOpenRemaining() time.Duration { return c.breaker.remaining() }
func (c *Client) CircuitState() map[string]any {
	return map[string]any{"open": c.CircuitOpenRemaining() > 0, "open_remaining": c.CircuitOpenRemaining().String()}
}

func (c *Client) CollectDay(ctx context.Context, oq, dq string, day time.Time) (string, []flight.Flight, error) {
	var ids []string
	var out []flight.Flight
	for _, o := range expand(oq) {
		for _, d := range expand(dq) {
			if o == d {
				continue
			}
			id, fs, e := c.search(ctx, oq, dq, o, d, day)
			if e != nil {
				return strings.Join(ids, ","), nil, e
			}
			ids = append(ids, id)
			out = append(out, fs...)
		}
	}
	return strings.Join(ids, ","), dedupe(out), nil
}
func expand(s string) []string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "THRALL", "THR", "IKA":
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
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []string{strings.ToUpper(strings.TrimSpace(s))}
	}
}

func (c *Client) search(ctx context.Context, oq, dq, o, d string, day time.Time) (string, []flight.Flight, error) {
	payload := map[string]any{"AdultCount": c.adult, "ChildCount": 0, "InfantCount": 0, "CabinClass": "All", "Routes": []any{map[string]any{"OriginCode": o, "DestinationCode": d, "DepartureDate": day.Format("2006-01-02")}}, "Baggage": true, "IncludeFlightsWithHigherCapacity": false}
	var raw map[string]any
	if err := c.post(ctx, "/api/Flights", payload, &raw); err != nil {
		return "", nil, err
	}
	items, _ := raw["Flights"].([]any)
	flights := make([]flight.Flight, 0, len(items))
	for _, v := range items {
		if f, ok := mapFlight(v, oq, dq, day); ok {
			flights = append(flights, f)
		}
	}
	return fmt.Sprintf("mrbilit_%s_%s_%s", o, d, day.Format("2006-01-02")), flights, nil
}

func mapFlight(v any, oq, dq string, day time.Time) (flight.Flight, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return flight.Flight{}, false
	}
	segs, _ := m["Segments"].([]any)
	prices, _ := m["Prices"].([]any)
	if len(segs) == 0 || len(prices) == 0 {
		return flight.Flight{}, false
	}
	seg, _ := segs[0].(map[string]any)
	legs, _ := seg["Legs"].([]any)
	price, _ := prices[0].(map[string]any)
	if len(legs) == 0 {
		return flight.Flight{}, false
	}
	leg, _ := legs[0].(map[string]any)
	dep := parseTime(str(leg["DepartureTime"]))
	arr := parseTime(str(leg["ArrivalTime"]))
	code := strings.ToUpper(str(leg["AirlineCode"]))
	num := str(leg["FlightNumber"])
	airline := ""
	if a, ok := leg["Airline"].(map[string]any); ok {
		airline = str(a["EnglishTitle"])
	}
	fares, _ := price["PassengerFares"].([]any)
	fare := map[string]any{}
	if len(fares) > 0 {
		fare, _ = fares[0].(map[string]any)
		for _, x := range fares {
			if q, ok := x.(map[string]any); ok && str(q["PaxType"]) == "ADL" {
				fare = q
				break
			}
		}
	}
	rial := int64(numVal(fare["TotalFare"]))
	baggage := numVal(price["Baggage"])
	stops := int(numVal(leg["Stops"]))
	if ts, ok := leg["TechnicalStops"].([]any); ok && len(ts) > stops {
		stops = len(ts)
	}
	dur := duration(str(seg["TotalTime"]))
	origin := strings.ToUpper(strDefault(leg["OriginCode"], oq))
	dest := strings.ToUpper(strDefault(leg["DestinationCode"], dq))
	id := fmt.Sprintf("mrbilit_%s", str(m["Id"]))
	var bag *int
	if baggage > 0 {
		x := int(baggage)
		bag = &x
	}
	var dm *int
	if dur > 0 {
		x := dur
		dm = &x
	}
	return flight.Flight{Origin: origin, Destination: dest, OriginQuery: strings.ToUpper(oq), DestinationQuery: strings.ToUpper(dq), FlightDate: day, AirlineCode: code, AirlineName: airline, FlightNumber: num, FlightNumberNorm: NormalizeFlightNumber(code, num), DepartAt: dep, ArriveAt: arr, DepartMinute: dep.Hour()*60 + dep.Minute(), ArriveMinute: arr.Hour()*60 + arr.Minute(), BaggageLabel: func() string {
		if baggage > 0 {
			return strconv.Itoa(int(baggage)) + " KG"
		}
		return ""
	}(), BaggageKG: bag, PriceRial: rial, PriceToman: rial / 10, IsCharter: boolVal(price["IsCharter"]), Stops: stops, DurationMin: dm, ProposalID: id, Source: Source}, true
}
func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}
func strDefault(v any, d string) string {
	if x := str(v); x != "" {
		return x
	}
	return d
}
func numVal(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}
func boolVal(v any) bool { b, _ := v.(bool); return b }
func parseTime(s string) time.Time {
	for _, f := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, e := time.Parse(f, s); e == nil {
			return t
		}
	}
	return time.Time{}
}
func duration(s string) int {
	re := regexp.MustCompile(`(?i)(\d+)\s*(h|hour|ساعت|m|minute|دقیقه)`)
	n := 0
	for _, x := range re.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.Atoi(x[1])
		if strings.HasPrefix(strings.ToLower(x[2]), "h") || x[2] == "ساعت" {
			n += v * 60
		} else {
			n += v
		}
	}
	return n
}
func NormalizeFlightNumber(code, num string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	raw := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(strings.TrimSuffix(num, "."))), " ", "")
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
func dedupe(in []flight.Flight) []flight.Flight {
	seen := map[string]bool{}
	out := make([]flight.Flight, 0, len(in))
	for _, f := range in {
		k := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s", f.Origin, f.Destination, f.FlightDate.Format("2006-01-02"), f.AirlineCode, f.FlightNumber, f.DepartAt.Format(time.RFC3339Nano), f.PriceRial, f.ProposalID)
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	if e := c.breaker.before(); e != nil {
		return e
	}
	if e := c.limiter.wait(ctx); e != nil {
		return e
	}
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json-patch+json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "fa-IR,fa;q=0.9,en;q=0.8")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Referer", c.referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/131 Safari/537.36")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		d := 2 * time.Minute
		if h := resp.Header.Get("Retry-After"); h != "" {
			if n, e := strconv.Atoi(h); e == nil {
				d = time.Duration(n) * time.Second
			}
		}
		if resp.StatusCode == 429 || resp.StatusCode == 403 || resp.StatusCode == 409 {
			c.breaker.rate(d)
		}
		return &HTTPError{StatusCode: resp.StatusCode, RetryAfter: d, Body: string(data)}
	}
	c.breaker.success()
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type limiter struct {
	mu                 sync.Mutex
	rps, burst, tokens float64
	last               time.Time
}

func newLimiter(r float64, b int) *limiter {
	now := time.Now()
	return &limiter{rps: r, burst: float64(b), tokens: float64(b), last: now}
}
func (l *limiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		l.tokens += now.Sub(l.last).Seconds() * l.rps
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		d := time.Duration((1 - l.tokens) / l.rps * float64(time.Second))
		l.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

type breaker struct {
	mu               sync.Mutex
	threshold, count int
	open             time.Time
	duration         time.Duration
}

func newBreaker(t int, d time.Duration) *breaker { return &breaker{threshold: t, duration: d} }
func (b *breaker) before() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open.IsZero() && time.Now().Before(b.open) {
		return &circuitError{b.open}
	}
	if !b.open.IsZero() {
		b.open = time.Time{}
		b.count = 0
	}
	return nil
}
func (b *breaker) rate(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if b.count >= b.threshold {
		if d < b.duration {
			d = b.duration
		}
		b.open = time.Now().Add(d)
	}
}
func (b *breaker) success() { b.mu.Lock(); b.count = 0; b.mu.Unlock() }
func (b *breaker) remaining() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open.IsZero() {
		return 0
	}
	d := time.Until(b.open)
	if d < 0 {
		return 0
	}
	return d
}
