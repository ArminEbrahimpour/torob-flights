package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type Flight struct {
	ID           int64     `json:"id"`
	Origin       string    `json:"origin"`
	Destination  string    `json:"destination"`
	FlightDate   time.Time `json:"flight_date"`
	AirlineCode  string    `json:"airline_code"`
	AirlineName  string    `json:"airline_name"`
	FlightNumber string    `json:"flight_number"`
	DepartAt     time.Time `json:"depart_at"`
	ArriveAt     time.Time `json:"arrive_at"`
	DepartMinute int       `json:"depart_minute"`
	DurationMin  *int      `json:"duration_min,omitempty"`
	BaggageLabel string    `json:"baggage_label"`
	BaggageKG    *int      `json:"baggage_kg,omitempty"`
	PriceRial    int64     `json:"price_rial"`
	PriceToman   int64     `json:"price_toman"`
	IsCharter    bool      `json:"is_charter"`
	Stops        int       `json:"stops"`
	Source       string    `json:"source"`
	CollectedAt  time.Time `json:"collected_at"`
}

// UnmarshalJSON accepts PostgreSQL's DATE representation (YYYY-MM-DD) as
// well as the RFC3339 timestamps used by the HTTP/event APIs.
func (f *Flight) UnmarshalJSON(data []byte) error {
	type plain Flight
	var wire struct {
		*plain
		FlightDate json.RawMessage `json:"flight_date"`
	}
	wire.plain = (*plain)(f)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	raw := bytes.TrimSpace(wire.FlightDate)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("flight_date: %w", err)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			f.FlightDate = parsed
			return nil
		}
	}
	return fmt.Errorf("flight_date: unsupported value %q", value)
}

type RankedFlight struct {
	Flight             Flight  `json:"flight"`
	Rank               int     `json:"rank"`
	Score              float64 `json:"score"`
	FreshnessSeconds   int64   `json:"freshness_seconds"`
	PriceTypicalToman  int64   `json:"price_typical_toman,omitempty"`
	PriceDeltaPercent  float64 `json:"price_delta_percent,omitempty"`
	DealLabel          string  `json:"deal_label"`
	VerificationStatus string  `json:"verification_status"`
	ProviderURL        string  `json:"provider_url,omitempty"`
	Recommendation     string  `json:"recommendation"`
}
type SearchQuery struct {
	Origin, Destination, Date, Mode, MaxStops string
	Limit                                     int
}
