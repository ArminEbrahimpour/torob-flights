package flights

import (
	"testing"
	"time"
)

func TestFreshnessWindow(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct { date string; want time.Duration }{
		{"2026-11-04", FreshnessNearDeparture},
		{"2026-11-05", FreshnessNearDeparture},
		{"2026-11-10", FreshnessThisWeek},
		{"2026-11-20", FreshnessLater},
		{"", FreshnessUnknown},
	}
	for _, tt := range tests {
		if got := freshnessWindow(tt.date, now); got != tt.want { t.Errorf("freshnessWindow(%q) = %s, want %s", tt.date, got, tt.want) }
	}
}
