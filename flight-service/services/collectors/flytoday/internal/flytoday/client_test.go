package flytoday

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
	if got := NormalizeFlightNumber("SV", "551"); got != "SV551" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeFlightNumber("SV", "SV551"); got != "SV551" {
		t.Fatalf("got %q", got)
	}
}

func TestAirlineNameMap(t *testing.T) {
	m := airlineNameMap(&additionalData{
		Airlines: []airlineInfo{{Iata: "sv", Name: "Saudi Arabian", NameLocal: "سعودی"}},
	})
	if m["SV"] != "Saudi Arabian" {
		t.Fatalf("got %q", m["SV"])
	}
}

func TestCircuitBreakerOpensOnRateLimit(t *testing.T) {
	b := newCircuitBreaker(3, 10*time.Minute)
	for i := 0; i < 2; i++ {
		until := b.recordRateLimit(0)
		if !until.IsZero() {
			t.Fatalf("breaker opened early on attempt %d: %v", i+1, until)
		}
		if err := b.beforeRequest(); err != nil {
			t.Fatalf("circuit should stay closed on attempt %d: %v", i+1, err)
		}
	}
	until := b.recordRateLimit(0)
	if until.IsZero() || time.Until(until) < defaultRateLimitPause {
		t.Fatalf("breaker did not open after threshold: %v", until)
	}
	if err := b.beforeRequest(); err == nil {
		t.Fatalf("expected circuit-open error")
	}
}

func TestMapSearchCityCode(t *testing.T) {
	cases := map[string]string{
		"IKA": "thr", "THRALL": "thr", "THR": "thr",
		"ISTALL": "ist", "IST": "ist", "SAW": "ist",
		"DXBALL": "dxb", "DXB": "dxb",
		"NJFALL": "njf", "NJF": "njf",
		"AYTALL": "ayt", "AYT": "ayt",
		"ESB": "ank", "GYD": "bak", "PEK": "bjs", "PKX": "bjs", "PVG": "sha", "SVO": "mow",
		"MHD": "mhd", "DOH": "doh", "COV": "cov",
	}
	for in, want := range cases {
		if got := mapSearchCityCode(in); got != want {
			t.Fatalf("%s: got %q, want %q", in, got, want)
		}
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
