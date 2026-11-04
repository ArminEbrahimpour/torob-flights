package safarmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/offerurl"
)

const baseURL = "https://safarmarket.com/api/flight/v3/search"

// Options configures the Safarmarket client.
type Options struct {
	Adult                   int           // number of adult passengers
	FlightClass             string        // "economy", "business", or "" (both)
	RPS                     float64       // requests per second
	Burst                   int           // burst size for rate limiter
	HTTPTimeout             time.Duration // HTTP client timeout
	CircuitBreakerThreshold int           // number of consecutive 429s to open circuit
	CircuitBreakerOpen      time.Duration // duration to keep circuit open

	// Safarmarket-specific static fields (defaults match the Python example)
	Cookie         string
	Recaptcha      string
	Platform       string
	UID            string
	Limit          int
	Compress       bool
	ProductType    string
	SearchValidity int
	CID            int
	Checksum       int
	IP             string
	ISP            string
	City           string
	Country        string
}

// Client holds the HTTP client, rate limiter, circuit breaker, and static config.
type Client struct {
	http    *http.Client
	adult   int
	class   string
	limiter *requestLimiter
	breaker *circuitBreaker

	// safarmarket static fields
	cookie         string
	recaptcha      string
	platform       string
	uid            string
	limit          int
	compress       bool
	productType    string
	searchValidity int
	cid            int
	checksum       int
	ip             string
	isp            string
	city           string
	country        string
}

// New creates a new Safarmarket client with the given options.
func New(opts Options) *Client {
	// Defaults
	if opts.Adult < 1 {
		opts.Adult = 1
	}
	if opts.FlightClass == "" {
		opts.FlightClass = "economy"
	}
	if opts.RPS <= 0 {
		opts.RPS = 0.1
	}
	if opts.Burst < 1 {
		opts.Burst = 1
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 60 * time.Second
	}
	if opts.Cookie == "" {
		opts.Cookie = "_utmtracker=%7B%22utm_source%22%3A%22google.com%22%2C%22utm_medium%22%3A%22%22%2C%22utm_campaign%22%3A%22%22%7D; eventToken=f04b8d1bf0190520725ac3daa646932d; _ga=GA1.1.2020149628.1786393702; _clck=1dje2pk%5E2%5Eg8h%5E0%5E2413; _clsk=p9wv9b%5E1786393704399%5E1%5E1%5Eh.clarity.ms%2Fcollect; _gcl_au=1.1.2013946439.1786393702.-.-.1786394063.1382272309.1786394063.1786394063; returnURL=https%3A%2F%2Fsafarmarket.com%2Fflights%2FcMHD-cIST%2F2026-08-14%2F0%2Fallclasses%2F1adults%2F0children%2F0infants; _ga_56HSKT2CVLG-DJ32G60094=GS2.1.s1786393716$o1$g1$t1786396190$j60$l0$h0; flights_throttle=cllsTFZvMUViN0wyTEhBandNdzd0QT09; _ga_DJ32G60094=GS2.1.s1786393702$o1$g1$t1786396349$j59$l0$h0"
	}
	if opts.Recaptcha == "" {
		opts.Recaptcha = "EOafo8H1jAQ1BclsXeUvcqdIHiYI8fdbdgUv5gn3pgbwapUoJp"
	}
	if opts.Platform == "" {
		opts.Platform = "WEB_DESKTOP"
	}
	if opts.Limit == 0 {
		opts.Limit = 250
	}
	if opts.ProductType == "" {
		opts.ProductType = "LFLI"
	}
	if opts.SearchValidity == 0 {
		opts.SearchValidity = 2
	}
	if opts.CID == 0 {
		opts.CID = 1
	}
	if opts.Checksum == 0 {
		opts.Checksum = 1
	}
	if opts.IP == "" {
		opts.IP = "11.12.13.14"
	}
	if opts.ISP == "" {
		opts.ISP = "a"
	}
	if opts.City == "" {
		opts.City = "teh"
	}
	if opts.Country == "" {
		opts.Country = "ir"
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
		adult:          opts.Adult,
		class:          opts.FlightClass,
		limiter:        newRequestLimiter(opts.RPS, opts.Burst),
		breaker:        newCircuitBreaker(opts.CircuitBreakerThreshold, opts.CircuitBreakerOpen),
		cookie:         opts.Cookie,
		recaptcha:      opts.Recaptcha,
		platform:       opts.Platform,
		uid:            opts.UID,
		limit:          opts.Limit,
		compress:       opts.Compress,
		productType:    opts.ProductType,
		searchValidity: opts.SearchValidity,
		cid:            opts.CID,
		checksum:       opts.Checksum,
		ip:             opts.IP,
		isp:            opts.ISP,
		city:           opts.City,
		country:        opts.Country,
	}
}

// requestLimiter implements a token bucket rate limiter.
type requestLimiter struct {
	mu       sync.Mutex
	rps      float64
	burst    float64
	tokens   float64
	lastFill time.Time
}

func newRequestLimiter(rps float64, burst int) *requestLimiter {
	if rps <= 0 {
		rps = 0.1
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

// circuitBreaker protects against repeated failures.
type circuitBreaker struct {
	mu            sync.Mutex
	threshold     int
	openFor       time.Duration
	recent429     int
	lastRateLimit time.Time
	openUntil     time.Time
}

func (b *circuitBreaker) openUntilFunc(until time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if until.After(b.openUntil) {
		b.openUntil = until
	}

	b.recent429 = b.threshold
	b.lastRateLimit = time.Now()
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
	return &CircuitOpenError{OpenUntil: b.openUntil}
}

func (b *circuitBreaker) recordRateLimit() time.Time {
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
	b.openUntil = now.Add(b.openFor)
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

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode  int
	Method      string
	Path        string
	Body        string
	LockedUntil *time.Time
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d %s %s: %s", e.StatusCode, e.Method, e.Path, e.Body)
}

// CircuitOpenError indicates the circuit breaker is open.
type CircuitOpenError struct {
	OpenUntil time.Time
}

func (e *CircuitOpenError) Error() string {
	return fmt.Sprintf("Safarmarket circuit breaker is open until %s",
		e.OpenUntil.UTC().Format(time.RFC3339))
}

// IsCircuitOpen checks if the error is a CircuitOpenError.
func IsCircuitOpen(err error) bool {
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

// IsRateLimited checks if the error indicates rate limiting.
func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	var circuitErr *CircuitOpenError
	return errors.As(err, &circuitErr)
}

// getKeys returns the keys of a map for debugging
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// writeToFile writes data to a file
func writeToFile(filename string, data []byte) error {
	return os.WriteFile(filename, data, 0644)
}

// appendToFile appends data to a file
func appendToFile(filename string, data []byte) error {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// CollectDay performs a search on safarmarket.com for the given origin, destination, and day.
func (c *Client) CollectDay(
	ctx context.Context,
	origin string,
	destination string,
	day time.Time,
) (string, []flight.Flight, error) {
	// Build payload and headers
	payload := c.buildPayload(origin, destination, day)
	headers := c.buildHeaders(origin, destination, day)

	var respData map[string]interface{}
	if err := c.doRequest(ctx, http.MethodPost, baseURL, headers, payload, &respData); err != nil {
		return "", nil, err
	}

	// Save the full response to a file
	//fullResponseJSON, _ := json.MarshalIndent(respData, "", "  ")
	//writeToFile("full_response.json", fullResponseJSON)
	//appendToFile("debug_output.txt", []byte("=== FULL RESPONSE SAVED TO full_response.json ===\n"))

	var flights []flight.Flight

	// Navigate to the flights array
	if result, ok := respData["result"].(map[string]interface{}); ok {
		if flightsData, ok := result["flights"].([]interface{}); ok {
			debugMsg := fmt.Sprintf("Found %d flights in result.flights\n", len(flightsData))
			appendToFile("debug_output.txt", []byte(debugMsg))
			fmt.Print(debugMsg)

			// Parse each flight
			successCount := 0
			for i, flightData := range flightsData {
				if flightMap, ok := flightData.(map[string]interface{}); ok {
					// This now returns multiple flights (one per fare class)
					parsedFlights := parseFlightFromMap(origin, destination, day, flightMap, i)
					for _, f := range parsedFlights {
						if f != nil {
							f.Source = "safarmarket"
							f.CollectedAt = time.Now().UTC()
							flights = append(flights, *f)
							successCount++
						}
					}
				}
			}

			debugMsg = fmt.Sprintf("Successfully parsed %d flights\n", successCount)
			appendToFile("debug_output.txt", []byte(debugMsg))
			fmt.Print(debugMsg)
		}
	}

	// Save the parsed flights to a file
	//if len(flights) > 0 {
	//	flightsJSON, _ := json.MarshalIndent(flights, "", "  ")
	//	writeToFile("parsed_flights.json", flightsJSON)
	//	appendToFile("debug_output.txt", []byte("Parsed flights saved to parsed_flights.json\n"))
	//}

	return "", flights, nil
}

func parseLockedUntil(body string) (*time.Time, bool) {
	var payload struct {
		ErrorCode string `json:"errorCode"`
		LimitType string `json:"limitType"`
		Details   string `json:"details"`
	}

	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, false
	}

	if payload.ErrorCode != "IP_LOCKED" && payload.LimitType != "LOCKED" {
		return nil, false
	}

	const prefix = "Locked until:"
	if !strings.HasPrefix(strings.TrimSpace(payload.Details), prefix) {
		return nil, false
	}

	value := strings.TrimSpace(
		strings.TrimPrefix(strings.TrimSpace(payload.Details), prefix),
	)

	t, err := parseAPITime(value)
	if err != nil {
		return nil, false
	}

	return &t, true
}

// parseFlightFromMap extracts flight data from a map using the actual field names
func parseFlightFromMap(originQ, destQ string, day time.Time, data map[string]interface{}, index int) []*flight.Flight {
	// Helper to get string value from map
	getStr := func(m map[string]interface{}, key string) string {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	// Helper to get int value from map
	getInt := func(m map[string]interface{}, key string) int {
		if v, ok := m[key]; ok {
			switch val := v.(type) {
			case float64:
				return int(val)
			case int:
				return val
			}
		}
		return 0
	}

	// Helper to get float64 value from map
	getFloat := func(m map[string]interface{}, key string) float64 {
		if v, ok := m[key]; ok {
			switch val := v.(type) {
			case float64:
				return val
			case int:
				return float64(val)
			}
		}
		return 0
	}

	// Helper to get bool value from map
	getBool := func(m map[string]interface{}, key string) bool {
		if v, ok := m[key]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
		return false
	}

	// Get the leave field which contains flight details
	leaveMap, ok := data["leave"].(map[string]interface{})
	if !ok {
		debugMsg := fmt.Sprintf("Flight #%d: No 'leave' field found\n", index)
		appendToFile("debug_output.txt", []byte(debugMsg))
		return nil
	}

	// The legs array contains the actual flight segments
	legs, ok := leaveMap["legs"].([]interface{})
	if !ok || len(legs) == 0 {
		debugMsg := fmt.Sprintf("Flight #%d: No 'legs' found in leave field\n", index)
		appendToFile("debug_output.txt", []byte(debugMsg))
		return nil
	}

	// Get the first leg (assuming it's the outbound flight)
	firstLeg, ok := legs[0].(map[string]interface{})
	if !ok {
		debugMsg := fmt.Sprintf("Flight #%d: Invalid leg data\n", index)
		appendToFile("debug_output.txt", []byte(debugMsg))
		return nil
	}

	// Extract common flight details (same for all fare classes)
	origin := getStr(firstLeg, "departureAirportCode")
	if origin == "" {
		origin = getStr(firstLeg, "origin")
	}
	if origin == "" {
		origin = getStr(firstLeg, "Origin")
	}

	destination := getStr(firstLeg, "arrivalAirportCode")
	if destination == "" {
		destination = getStr(firstLeg, "destination")
	}
	if destination == "" {
		destination = getStr(firstLeg, "Destination")
	}

	airlineCode := getStr(firstLeg, "airlineCode")
	if airlineCode == "" {
		airlineCode = getStr(leaveMap, "airlineCode")
	}
	if airlineCode == "" {
		airlineCode = getStr(leaveMap, "AirlineCode")
	}

	airlineName := getStr(firstLeg, "airlineName")
	if airlineName == "" {
		airlineName = getStr(leaveMap, "airlineName")
	}
	if airlineName == "" {
		airlineName = getStr(leaveMap, "AirlineName")
	}
	if airlineName == "" {
		airlineName = getStr(firstLeg, "airlineNameFa")
	}

	flightNumber := getStr(firstLeg, "flightNo")
	if flightNumber == "" {
		flightNumber = getStr(leaveMap, "flightNo")
	}
	if flightNumber == "" {
		flightNumber = getStr(leaveMap, "flightNumber")
	}

	departureDateTime := getStr(firstLeg, "departureTime")
	if departureDateTime == "" {
		departureDateTime = getStr(firstLeg, "departureDateTime")
	}
	if departureDateTime == "" {
		departureDateTime = getStr(leaveMap, "departureTime")
	}

	arrivalDateTime := getStr(firstLeg, "arrivalTime")
	if arrivalDateTime == "" {
		arrivalDateTime = getStr(firstLeg, "arrivalDateTime")
	}
	if arrivalDateTime == "" {
		arrivalDateTime = getStr(leaveMap, "arrivalTime")
	}

	// Get stops count
	stops := 0
	if s := getInt(leaveMap, "stopsCount"); s > 0 {
		stops = s
	}
	if stops == 0 {
		if s := getInt(leaveMap, "stops"); s > 0 {
			stops = s
		}
	}
	if stops == 0 {
		if s := getInt(firstLeg, "stops"); s > 0 {
			stops = s
		}
	}

	// Get duration (flightDuration from the leg)
	duration := getInt(firstLeg, "flightDuration")
	if duration == 0 {
		if d := getInt(leaveMap, "duration"); d > 0 {
			duration = d
		}
	}
	if duration == 0 {
		if d := getInt(leaveMap, "Duration"); d > 0 {
			duration = d
		}
	}

	// Get charter status
	isCharter := getBool(leaveMap, "charter")
	if !isCharter {
		isCharter = getBool(leaveMap, "isCharter")
	}

	// Validate required fields
	if origin == "" || destination == "" || departureDateTime == "" || arrivalDateTime == "" {
		debugMsg := fmt.Sprintf("Flight #%d missing required fields: origin=%q, destination=%q, depart=%q, arrive=%q\n",
			index, origin, destination, departureDateTime, arrivalDateTime)
		appendToFile("debug_output.txt", []byte(debugMsg))
		return nil
	}

	// Parse times (do this once)
	departAt, err1 := parseAPITime(departureDateTime)
	arriveAt, err2 := parseAPITime(arrivalDateTime)
	if err1 != nil || err2 != nil {
		debugMsg := fmt.Sprintf("Flight #%d time parsing error: depart=%q (%v), arrive=%q (%v)\n",
			index, departureDateTime, err1, arrivalDateTime, err2)
		appendToFile("debug_output.txt", []byte(debugMsg))
		return nil
	}

	// Normalize flight number
	code := strings.ToUpper(strings.TrimSpace(airlineCode))
	num := strings.TrimSpace(flightNumber)
	fnNorm := NormalizeFlightNumber(code, num)

	// Duration
	var dur *int
	if duration > 0 {
		dur = &duration
	}

	var flights []*flight.Flight

	// Get the flight class from the root data
	flightClass := "ECONOMY"
	if fc, ok := data["flightClass"].(string); ok && fc != "" {
		flightClass = fc
	}

	// Also check in leaveMap
	if flightClass == "ECONOMY" {
		if fc, ok := leaveMap["flightType"].(string); ok && fc != "" {
			flightClass = fc
		}
	}

	// Try to get price
	price := 0.0

	// First try from priceTypes (adultPrice)
	if priceTypes, ok := leaveMap["priceTypes"].(map[string]interface{}); ok {
		if adultPrice, ok := priceTypes["adultPrice"].(float64); ok && adultPrice > 0 {
			price = adultPrice
		}
		if price == 0 {
			if p, ok := priceTypes["price"].(float64); ok && p > 0 {
				price = p
			}
		}
		if price == 0 {
			if p, ok := priceTypes["totalPrice"].(float64); ok && p > 0 {
				price = p
			}
		}
	}

	// If no price found, try minPrice from root
	if price == 0 {
		if minPrice, ok := data["minPrice"].(float64); ok && minPrice > 0 {
			price = minPrice
		}
	}

	// Try other locations
	if price == 0 {
		if p := getFloat(leaveMap, "price"); p > 0 {
			price = p
		}
	}
	if price == 0 {
		if p := getFloat(leaveMap, "Price"); p > 0 {
			price = p
		}
	}
	if price == 0 {
		if p := getFloat(firstLeg, "price"); p > 0 {
			price = p
		}
	}

	baggage := ""
	var kgForClass *int

	if baggageList, ok := leaveMap["baggageList"].([]interface{}); ok && len(baggageList) > 0 {
		if baggageMap, ok := baggageList[0].(map[string]interface{}); ok {
			if weightKg, ok := baggageMap["weightKg"]; ok {
				baggage = fmt.Sprintf("%v kg", weightKg)

				switch v := weightKg.(type) {
				case string:
					if kg, err := strconv.Atoi(v); err == nil {
						kgForClass = &kg
					}
				case float64:
					kg := int(v)
					kgForClass = &kg
				}
			}
		}
	}

	if price > 0 {
		flight := &flight.Flight{
			Origin:           origin,
			Destination:      destination,
			OriginQuery:      strings.ToUpper(originQ),
			DestinationQuery: strings.ToUpper(destQ),
			FlightDate:       day,
			AirlineCode:      code,
			AirlineName:      airlineName + " (" + flightClass + ")",
			FlightNumber:     num,
			FlightNumberNorm: fnNorm,
			DepartAt:         departAt,
			ArriveAt:         arriveAt,
			DepartMinute:     departAt.Hour()*60 + departAt.Minute(),
			ArriveMinute:     arriveAt.Hour()*60 + arriveAt.Minute(),
			BaggageLabel:     baggage,
			BaggageKG:        kgForClass,
			PriceRial:        int64(price),
			PriceToman:       int64(price) / 10,
			IsCharter:        isCharter,
			Stops:            stops,
			DurationMin:      dur,
			ProposalID:       "",
			OfferURL:         offerurl.FromValue(data),
		}
		flights = append(flights, flight)
	}

	debugMsg := fmt.Sprintf("Flight #%d: Created %d flight entry (class: %s, price: %f, airline: %s)\n",
		index, len(flights), flightClass, price, airlineName)
	appendToFile("debug_output.txt", []byte(debugMsg))

	return flights
}

// CircuitState returns the circuit breaker state.
func (c *Client) CircuitState() map[string]any {
	return c.breaker.state()
}

// --- Safarmarket-specific types ---

type searchPayload struct {
	Platform       string       `json:"platform"`
	UID            string       `json:"uid"`
	Limit          int          `json:"limit"`
	Compress       bool         `json:"compress"`
	ProductType    string       `json:"productType"`
	SearchValidity int          `json:"searchValidity"`
	CID            int          `json:"cid"`
	Checksum       int          `json:"checksum"`
	IPInfo         ipInfo       `json:"IPInfo"`
	SearchFilter   searchFilter `json:"searchFilter"`
}

type ipInfo struct {
	IP      string `json:"ip"`
	ISP     string `json:"isp"`
	City    string `json:"city"`
	Country string `json:"country"`
}

type searchFilter struct {
	SourceAirportCode string  `json:"sourceAirportCode"`
	TargetAirportCode string  `json:"targetAirportCode"`
	SourceIsCity      bool    `json:"sourceIsCity"`
	TargetIsCity      bool    `json:"targetIsCity"`
	LeaveDate         string  `json:"leaveDate"`
	ReturnDate        *string `json:"returnDate"`
	AdultCount        int     `json:"adultCount"`
	ChildCount        int     `json:"childCount"`
	InfantCount       int     `json:"infantCount"`
	Economy           bool    `json:"economy"`
	Business          bool    `json:"business"`
}

// buildPayload constructs the JSON body for the POST request.
func (c *Client) buildPayload(origin, destination string, day time.Time) *searchPayload {

	if origin == "ISTALL" {
		origin = "IST"
	}

	if destination == "ISTALL" {
		destination = "IST"
	}

	if origin == "DXBALL" {
		origin = "DXB"
	}

	if destination == "DXBALL" {
		destination = "DXB"
	}

	if origin == "NJFALL" {
		origin = "NJF"
	}

	if destination == "NJFALL" {
		destination = "NJF"
	}

	if origin == "THRALL" {
		origin = "THR"
	}

	if destination == "THRALL" {
		destination = "THR"
	}

	if origin == "AYTALL" {
		origin = "AYT"
	}

	if destination == "AYTALL" {
		destination = "AYT"
	}

	economy := true
	business := true
	switch strings.ToLower(c.class) {
	case "economy":
		business = false
	case "business":
		economy = false
	}

	return &searchPayload{
		Platform:       c.platform,
		UID:            c.uid,
		Limit:          c.limit,
		Compress:       c.compress,
		ProductType:    c.productType,
		SearchValidity: c.searchValidity,
		CID:            c.cid,
		Checksum:       c.checksum,
		IPInfo: ipInfo{
			IP:      c.ip,
			ISP:     c.isp,
			City:    c.city,
			Country: c.country,
		},
		SearchFilter: searchFilter{
			SourceAirportCode: strings.ToUpper(origin),
			TargetAirportCode: strings.ToUpper(destination),
			SourceIsCity:      true,
			TargetIsCity:      true,
			LeaveDate:         day.Format("2006-01-02"),
			ReturnDate:        nil,
			AdultCount:        c.adult,
			ChildCount:        0,
			InfantCount:       0,
			Economy:           economy,
			Business:          business,
		},
	}
}

// buildHeaders constructs the HTTP headers for the request.
func (c *Client) buildHeaders(origin, destination string, day time.Time) map[string]string {
	ref := fmt.Sprintf("https://safarmarket.com/flights/c%s-c%s/%s/0/allclasses/%dadults/0children/0infants",
		strings.ToUpper(origin), strings.ToUpper(destination),
		day.Format("2006-01-02"), c.adult)

	return map[string]string{
		"Accept":             "*/*",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Accept-Language":    "en-US,en;q=0.9,fa;q=0.8",
		"Connection":         "keep-alive",
		"Content-Type":       "application/json;charset=UTF-8",
		"Cookie":             c.cookie,
		"Host":               "safarmarket.com",
		"Origin":             "https://safarmarket.com",
		"Referer":            ref,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"User-Agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
		"X-Auth-Token":       "null",
		"X-Requested-With":   "XMLHttpRequest",
		"recaptcha":          c.recaptcha,
		"sec-ch-ua":          `"Not=A?Brand";v="99", "Google Chrome";v="151", "Chromium";v="151"`,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
	}
}

// doRequest sends an HTTP request with rate limiting and circuit breaker protection.
func (c *Client) doRequest(ctx context.Context, method, url string, headers map[string]string, payload any, out any) error {
	var bodyBytes []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		bodyBytes = b
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

	var bodyReader io.Reader
	if len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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
		var lockedUntil *time.Time

		if resp.StatusCode == http.StatusTooManyRequests {
			lockedUntil, _ = parseLockedUntil(string(data))

			if lockedUntil != nil {
				c.breaker.openUntilFunc(*lockedUntil)
			} else {
				c.breaker.recordRateLimit()
			}
		}

		return &HTTPError{
			StatusCode:  resp.StatusCode,
			Method:      method,
			Path:        url,
			Body:        truncate(string(data), 400),
			LockedUntil: lockedUntil,
		}
	}

	c.breaker.recordSuccess()
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Helper functions

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

	// Try different layouts - order matters (most specific first)
	layouts := []string{
		"2006-01-02 15:04:05", // "2026-09-10 17:30:00"
		"2006-01-02T15:04:05", // "2026-09-10T17:30:00"
		"2006-01-02 15:04",    // "2026-09-10 17:30" <- THIS IS THE FORMAT WE NEED
		time.RFC3339,          // "2026-09-10T17:30:00Z"
		"2006-01-02T15:04:05.0000000-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.000",
	}

	for _, layout := range layouts {
		// Check if it's a timezone layout or not
		if strings.Contains(layout, "Z07") || strings.Contains(layout, "-07") ||
			strings.Contains(layout, "Z") || strings.Contains(layout, "-07:00") {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}

	// If all else fails, try to parse just the date and add time
	if len(s) >= 10 {
		if t, err := time.ParseInLocation("2006-01-02", s[:10], loc); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("bad time %q", s)
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
