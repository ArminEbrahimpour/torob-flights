package trip

import (
	"context"
	"testing"
	"time"
)

func TestParseRetryAfterSeconds(t *testing.T) {
	if got := parseRetryAfter("120", time.Now()); got != 2*time.Minute {
		t.Fatalf("got %s, want 2m", got)
	}
}

func TestNormalizeFlightNumber(t *testing.T) {
	if got := NormalizeFlightNumber("TK", "1234"); got != "TK1234" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeFlightNumber("TK", "TK1234"); got != "TK1234" {
		t.Fatalf("got %q", got)
	}
}

func TestMapPageCode(t *testing.T) {
	if got := mapPageCode("THRALL"); got != "THR" {
		t.Fatalf("THRALL=%q", got)
	}
	if got := mapPageCode("IKA"); got != "THR" {
		t.Fatalf("IKA=%q", got)
	}
	if got := mapPageCode("ISTALL"); got != "IST" {
		t.Fatalf("ISTALL=%q", got)
	}
}

func TestMapBodyAirport(t *testing.T) {
	if got := mapBodyAirport("IKA"); got != "IKA" {
		t.Fatalf("IKA=%q", got)
	}
	if got := mapBodyAirport("DXBALL"); got != "DXB" {
		t.Fatalf("DXBALL=%q", got)
	}
	if got := mapBodyAirport("THRALL"); got != "THR" {
		t.Fatalf("THRALL=%q", got)
	}
}

func TestIsInternationalRoute(t *testing.T) {
	if !IsInternationalRoute("IST", "IKA") {
		t.Fatal("IST-IKA should be international")
	}
	if !IsInternationalRoute("IKA", "DOH") {
		t.Fatal("IKA-DOH should be international")
	}
	if IsInternationalRoute("MHD", "IKA") {
		t.Fatal("MHD-IKA should be domestic")
	}
}

func TestCircuitBreakerOpensOnRateLimit(t *testing.T) {
	b := newCircuitBreaker(3, 10*time.Minute)
	until := b.recordRateLimit(0)
	if until.IsZero() || time.Until(until) < defaultRatePause {
		t.Fatalf("breaker did not open for default pause: %v", until)
	}
	if err := b.beforeRequest(); err == nil {
		t.Fatalf("expected circuit-open error")
	}
}

func TestRequestLimiterHonorsCancellation(t *testing.T) {
	l := newRequestLimiter(0.01, 1)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatalf("expected cancellation")
	}
}

func TestMapResultSkipsSoldOut(t *testing.T) {
	_, ok := mapResult("IST", "THR", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), flightResult{
		OutBound: tripLegSet{RemainingSeat: -1, Legs: []tripLeg{{DepartureDateTime: "2026-08-15T10:00:00"}}},
	})
	if ok {
		t.Fatal("sold out offer should be skipped")
	}
}

func TestMapResultCharterFlag(t *testing.T) {
	f, ok := mapResult("IST", "THR", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), flightResult{
		Hash: 123,
		Fare: fareInfo{TotalPrice: 12_500_000},
		OutBound: tripLegSet{
			FlightType: "CHARTER",
			Legs: []tripLeg{{
				DepartureDateTime: "2026-08-15T10:00:00",
				LandingDateTime:   "2026-08-15T14:30:00",
				FlightNumber:      "1234",
				OperatingAirline:  airlineInfo{Code: "TK", Name: "Turkish"},
				Origin:            airportInfo{AirportIATA: "IST"},
				Destination:       airportInfo{AirportIATA: "IKA"},
			}},
		},
	})
	if !ok {
		t.Fatal("expected mapped flight")
	}
	if !f.IsCharter {
		t.Fatal("expected charter")
	}
	if f.PriceToman != 12_500_000 {
		t.Fatalf("price=%d", f.PriceToman)
	}
}
