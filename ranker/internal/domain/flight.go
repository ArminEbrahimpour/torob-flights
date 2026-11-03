package domain

import "time"

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
