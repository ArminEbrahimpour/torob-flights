package snapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveSearchEndpoints(t *testing.T) {
	thr := resolveSearchEndpoints("THRALL")
	if len(thr) != 1 || !thr[0].isCity || thr[0].code != "IKA" {
		t.Fatalf("THRALL=%+v", thr)
	}
	ist := resolveSearchEndpoints("ISTALL")
	if len(ist) != 1 || !ist[0].isCity || ist[0].code != "IST" {
		t.Fatalf("ISTALL=%+v", ist)
	}
	ika := resolveSearchEndpoints("IKA")
	if len(ika) != 1 || ika[0].isCity || ika[0].code != "IKA" {
		t.Fatalf("IKA=%+v", ika)
	}
}

func TestCollectDayMapsAirfares(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/listing/v1/one-way/search" {
			http.NotFound(w, r)
			return
		}
		var body searchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Origin != "IKA" || body.Destination != "IST" || !body.OriginIsCity || !body.DestinationIsCity {
			t.Fatalf("unexpected body: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"airfares": [{
				"id": "sol-1",
				"pricing": {
					"pricingDetails": [{"passengerType":"ADULT","payablePrice":123450000,"quantity":1}]
				},
				"routes": [{
					"totalJourneyDuration": 210,
					"totalConnectionsDuration": 0,
					"segments": [{
						"departureDateTime": "2026-08-15T08:30:00",
						"arrivalDateTime": "2026-08-15T11:00:00",
						"flightNumber": "TK872",
						"numberOfStops": 0,
						"journeyDuration": 210,
						"isCharter": false,
						"operatorAirlineInfo": {"airlineCode":"TK","airlineName":"Turkish Airlines"},
						"departureAirportInfo": {"airportCode":"IKA"},
						"arrivalAirportInfo": {"airportCode":"IST"},
						"baggageDetail": {"baggageInfoType":"CHECKED","weight":30,"count":1}
					}]
				}]
			}]
		}`))
	}))
	defer srv.Close()

	c := New(Options{
		Adult:       1,
		CabinType:   "economy",
		RPS:         1000,
		Burst:       10,
		HTTPTimeout: time.Second,
		BaseURL:     srv.URL,
	})

	day := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	rid, flights, err := c.CollectDay(context.Background(), "THRALL", "ISTALL", day)
	if err != nil {
		t.Fatal(err)
	}
	if rid == "" || len(flights) != 1 {
		t.Fatalf("rid=%q flights=%d", rid, len(flights))
	}
	f := flights[0]
	if f.Origin != "IKA" || f.Destination != "IST" {
		t.Fatalf("route=%s-%s", f.Origin, f.Destination)
	}
	if f.OriginQuery != "THRALL" || f.DestinationQuery != "ISTALL" {
		t.Fatalf("queries=%s-%s", f.OriginQuery, f.DestinationQuery)
	}
	if f.PriceRial != 123450000 || f.PriceToman != 12345000 {
		t.Fatalf("price rial=%d toman=%d", f.PriceRial, f.PriceToman)
	}
	if f.FlightNumberNorm != "TK872" {
		t.Fatalf("norm=%q", f.FlightNumberNorm)
	}
	if f.BaggageKG == nil || *f.BaggageKG != 30 {
		t.Fatalf("baggage=%v", f.BaggageKG)
	}
}

func TestIsRateLimitedIncludesConflict(t *testing.T) {
	if !IsRateLimited(&HTTPError{StatusCode: http.StatusConflict}) {
		t.Fatal("expected 409 to be rate limited")
	}
}

func TestCircuitBreakerOpensAfterThreshold(t *testing.T) {
	b := newCircuitBreaker(3, 10*time.Minute)
	_ = b.recordRateLimit(0)
	_ = b.recordRateLimit(0)
	until := b.recordRateLimit(0)
	if until.IsZero() {
		t.Fatalf("breaker did not open")
	}
	if err := b.beforeRequest(); err == nil {
		t.Fatalf("expected circuit-open error")
	}
}
