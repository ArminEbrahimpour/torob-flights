package alibaba

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

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	got := parseRetryAfter("Sun, 19 Jul 2026 10:03:00 GMT", now)
	if got != 3*time.Minute {
		t.Fatalf("got %s, want 3m", got)
	}
}

func TestCircuitBreakerOpensAfterThreshold(t *testing.T) {
	b := newCircuitBreaker(3, 10*time.Minute)
	if until := b.recordRateLimit(0); !until.IsZero() {
		t.Fatalf("opened after first failure")
	}
	if until := b.recordRateLimit(0); !until.IsZero() {
		t.Fatalf("opened after second failure")
	}
	until := b.recordRateLimit(2 * time.Minute)
	if until.IsZero() || time.Until(until) < 9*time.Minute {
		t.Fatalf("breaker did not open for configured duration: %v", until)
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
