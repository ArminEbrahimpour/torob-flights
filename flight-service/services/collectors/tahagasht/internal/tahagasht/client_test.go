package tahagasht

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeCabin(t *testing.T) {
	cases := map[string]string{
		"economy":  "Economy",
		"Business": "Business",
		"first":    "First",
		"unknown":  "Economy",
	}
	for in, want := range cases {
		if got := normalizeCabin(in); got != want {
			t.Fatalf("normalizeCabin(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExpandQueryCode(t *testing.T) {
	got := expandQueryCode("ISTALL")
	if len(got) != 2 || got[0] != "IST" || got[1] != "SAW" {
		t.Fatalf("ISTALL expansion=%v", got)
	}
	got = expandQueryCode("THRALL")
	if len(got) != 1 || got[0] != "IKA" {
		t.Fatalf("THRALL expansion=%v", got)
	}
}

func TestCollectDayAsyncFlowAndMapping(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rest/airlowfaresearchasync":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"status":"success","items":{"success":true,"searchConfig":{"transactionId":"tx-1","transactionStatus":"Continuation"},"pricedItineraries":[]}}`))
		case "/api/rest/airlowfaresearchasyncresult":
			polls++
			_, _ = w.Write([]byte(`{
				"status":"success",
				"items":{
					"success":true,
					"searchConfig":{"transactionId":"tx-1","transactionStatus":"End"},
					"pricedItineraries":[{
						"sequenceNumber": 1,
						"originDestinationOptions":[{
							"flightSegments":[{
								"departureAirport":{"code":"IKA"},
								"arrivalAirport":{"code":"EVN"},
								"departureDateTime":"2026-08-05T16:30:00",
								"arrivalDateTime":"2026-08-05T18:30:00",
								"flightNumber":"7953.",
								"airline":{"marketing":{"code":"IV","names":[]},"operating":{"code":"IV","names":[{"language":"en","name":"Caspian Airlines"}]}},
								"numberOfStops":0,
								"duration":"1:30:00",
								"freeBaggage":[{"passengerTypeCode":"ADT","amount":25,"unit":"KG"}]
							}]
						}],
						"pricingInfo":{
							"validatingAirlineCode":"IV",
							"itinTotalFare":{"totalFare":{"amount":220000000,"currency":"IRR"}},
							"passengerFares":[{"passengerTypeCode":"ADT","totalFare":{"amount":220000000,"currency":"IRR"}}]
						},
						"bookingData":{"agencySupplierId":"76","supplierName":"Caspian","data":"offer-token-123"}
					}]
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(Options{
		Adult:                1,
		FlightClass:          "economy",
		RPS:                  1000,
		Burst:                10,
		HTTPTimeout:          time.Second,
		UpstreamPollInterval: time.Millisecond,
		MaxPolls:             2,
		BaseURL:              srv.URL,
	})

	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	rid, flights, err := c.CollectDay(context.Background(), "IKA", "EVN", day)
	if err != nil {
		t.Fatal(err)
	}
	if rid != "tx-1" || polls != 1 {
		t.Fatalf("rid=%q polls=%d", rid, polls)
	}
	if len(flights) != 1 {
		t.Fatalf("flights=%d", len(flights))
	}
	f := flights[0]
	if f.FlightNumber != "7953" || f.FlightNumberNorm != "IV7953" {
		t.Fatalf("flight numbers raw=%q norm=%q", f.FlightNumber, f.FlightNumberNorm)
	}
	if f.AirlineCode != "IV" || f.AirlineName != "Caspian Airlines" {
		t.Fatalf("airline=%q %q", f.AirlineCode, f.AirlineName)
	}
	if f.BaggageKG == nil || *f.BaggageKG != 25 {
		t.Fatalf("baggage=%v", f.BaggageKG)
	}
	if f.DurationMin == nil || *f.DurationMin != 90 {
		t.Fatalf("duration=%v", f.DurationMin)
	}
	if f.PriceRial != 220000000 || f.PriceToman != 22000000 {
		t.Fatalf("price rial=%d toman=%d", f.PriceRial, f.PriceToman)
	}
	if f.ProposalID != "offer-token-123" {
		t.Fatalf("proposal_id=%q", f.ProposalID)
	}
}

func TestItineraryPriceAndProposalFallbacks(t *testing.T) {
	itin := pricedItinerary{
		SequenceNumber: 7,
		PricingInfo: pricingInfo{
			ItinTotalFare: struct {
				TotalFare moneyAmount `json:"totalFare"`
			}{TotalFare: moneyAmount{Amount: 1500000, Currency: "IRR"}},
		},
	}
	if got := itineraryPriceRial(itin); got != 1500000 {
		t.Fatalf("price=%d", got)
	}
	if got := itineraryProposalID(pricedItinerary{SequenceNumber: 7}); got != "7" {
		t.Fatalf("proposal fallback=%q", got)
	}
	if got := itineraryProposalID(pricedItinerary{
		BookingData: bookingData{Data: "token-abc"},
	}); got != "token-abc" {
		t.Fatalf("proposal booking data=%q", got)
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	if got := parseRetryAfter("120", time.Now()); got != 2*time.Minute {
		t.Fatalf("got %s, want 2m", got)
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
