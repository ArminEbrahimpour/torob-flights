package flights

import (
	"strings"
	"time"
)

const (
	FreshnessNearDeparture = 5 * time.Minute
	FreshnessThisWeek      = 15 * time.Minute
	FreshnessLater         = 60 * time.Minute
	FreshnessUnknown       = FreshnessThisWeek
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
