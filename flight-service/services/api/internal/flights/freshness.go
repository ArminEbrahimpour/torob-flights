package flights

import (
	"strings"
	"time"
)

const (
	// Collectors refresh a route every 30–180 minutes. A 5-minute window makes
	// every dated search empty as soon as that job finishes.
	FreshnessNearDeparture = 6 * time.Hour
	FreshnessThisWeek      = 24 * time.Hour
	FreshnessLater         = 72 * time.Hour
	FreshnessUnknown       = 72 * time.Hour
)

// freshnessWindow keeps near-term fares short-lived while allowing less
// frequent refreshes for flights farther in the future.
func freshnessWindow(date string, now time.Time) time.Duration {
	day, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return FreshnessUnknown
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	days := int(day.Sub(today).Hours() / 24)
	if days <= 1 {
		return FreshnessNearDeparture
	}
	if days <= 7 {
		return FreshnessThisWeek
	}
	return FreshnessLater
}
