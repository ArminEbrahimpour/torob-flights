package flight

import (
	"encoding/json"
	"time"
)

type Route struct {
	ID             int64
	Origin         string
	Destination    string
	Enabled        bool
	Priority       int
	RefreshMinutes int
	WindowDays     int
	LastEnqueuedAt *time.Time
}

type JobKind string

const (
	JobSeedRoute  JobKind = "seed_route"
	JobCollectDay JobKind = "collect_day"
)

type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
	JobDead    JobStatus = "dead"
)

type Job struct {
	ID          int64
	Kind        JobKind
	Status      JobStatus
	Priority    int
	RouteID     *int64
	Origin      string
	Destination string
	FlightDate  *time.Time
	Attempts    int
	MaxAttempts int
	RunAfter    time.Time
	LockedBy    *string
	LastError   *string
	Payload     json.RawMessage
}

type Flight struct {
	Origin           string    `json:"origin"`
	Destination      string    `json:"destination"`
	OriginCity       string    `json:"origin_city,omitempty"`
	DestinationCity  string    `json:"destination_city,omitempty"`
	OriginQuery      string    `json:"origin_query"`
	DestinationQuery string    `json:"destination_query"`
	FlightDate       time.Time `json:"flight_date"`
	AirlineCode      string    `json:"airline_code"`
	AirlineName      string    `json:"airline_name"`
	FlightNumber     string    `json:"flight_number"`
	FlightNumberNorm string    `json:"flight_number_norm"`
	DepartAt         time.Time `json:"depart_at"`
	ArriveAt         time.Time `json:"arrive_at"`
	DepartMinute     int       `json:"depart_minute"`
	ArriveMinute     int       `json:"arrive_minute"`
	BaggageLabel     string    `json:"baggage_label"`
	BaggageKG        *int      `json:"baggage_kg,omitempty"`
	PriceRial        int64     `json:"price_rial"`
	PriceToman       int64     `json:"price_toman"`
	IsCharter        bool      `json:"is_charter"`
	Stops            int       `json:"stops"`
	DurationMin      *int      `json:"duration_min,omitempty"`
	ProposalID       string    `json:"proposal_id"`
	OfferURL         string    `json:"offer_url,omitempty"`
	Source           string    `json:"source"`
	CollectedAt      time.Time `json:"collected_at"`
}

type ValidateRequest struct {
	Origin        string `json:"origin"`
	Destination   string `json:"destination"`
	Date          string `json:"date"`
	FlightNumber  string `json:"flight_number"`
	AirlineCode   string `json:"airline_code,omitempty"`
	DepartTime    string `json:"depart_time,omitempty"`
	TimeTolerance int    `json:"time_tolerance_min,omitempty"`
}

type ValidateMatch struct {
	Origin       string    `json:"origin"`
	Destination  string    `json:"destination"`
	AirlineCode  string    `json:"airline_code"`
	AirlineName  string    `json:"airline_name"`
	FlightNumber string    `json:"flight_number"`
	DepartAt     time.Time `json:"depart_at"`
	ArriveAt     time.Time `json:"arrive_at"`
	Baggage      string    `json:"baggage"`
	BaggageKG    *int      `json:"baggage_kg,omitempty"`
	PriceToman   int64     `json:"price_toman"`
	IsCharter    bool      `json:"is_charter"`
	CollectedAt  time.Time `json:"collected_at"`
}

type ValidateResponse struct {
	Valid   bool            `json:"valid"`
	Matches []ValidateMatch `json:"matches"`
	Reason  string          `json:"reason,omitempty"`
}

type SearchQuery struct {
	Origin        string
	Destination   string
	Date          string
	Source        string
	Airline       string
	FlightNumber  string
	MinPriceToman *int64
	MaxPriceToman *int64
	Charter       *bool
	MaxStops      *int
	Page          int
	PageSize      int
}

type SearchResult struct {
	Origin                 string    `json:"origin,omitempty"`
	Destination            string    `json:"destination,omitempty"`
	FlightDate             string    `json:"flight_date,omitempty"`
	Source                 string    `json:"source,omitempty"`
	Total                  int       `json:"total"`
	Page                   int       `json:"page"`
	PageSize               int       `json:"page_size"`
	Offers                 []Flight  `json:"offers"`
	FreshnessMaxAgeSeconds int       `json:"freshness_max_age_seconds"`
	FreshnessAsOf          time.Time `json:"freshness_as_of"`
	RefreshRequested       bool      `json:"refresh_requested"`
}

type MarketplaceIntent struct {
	Origin      string `json:"origin,omitempty"`
	Destination string `json:"destination,omitempty"`
	Airline     string `json:"airline,omitempty"`
}

type MarketplaceSearchResult struct {
	Query         string            `json:"query"`
	Intent        MarketplaceIntent `json:"intent"`
	Result        SearchResult      `json:"result"`
	Clarification string            `json:"clarification,omitempty"`
}

type UniqueFutureFlightsQuery struct {
	FlightNumber string
	Page         int
	PageSize     int
}

type UniqueFutureFlightsResult struct {
	Origin           string    `json:"origin"`
	Destination      string    `json:"destination"`
	FlightDate       string    `json:"flight_date"`
	AirlineCode      string    `json:"airline_code"`
	AirlineName      string    `json:"airline_name"`
	FlightNumber     string    `json:"flight_number"`
	FlightNumberNorm string    `json:"flight_number_norm"`
	DepartAt         time.Time `json:"depart_at"`
	ArriveAt         time.Time `json:"arrive_at"`
	BaggageKG        *int      `json:"baggage_kg,omitempty"`
	Stops            int       `json:"stops"`
	IsCharter        bool      `json:"is_charter"`
	PriceToman       int64     `json:"price_toman"`
}

type UniqueFutureFlightsListResult struct {
	FlightNumber string                      `json:"flight_number,omitempty"`
	Total        int                         `json:"total"`
	Page         int                         `json:"page"`
	PageSize     int                         `json:"page_size"`
	Flights      []UniqueFutureFlightsResult `json:"flights"`
}

type FlightByDateQuery struct {
	FlightNumber string
	Date         string
}

type FlightByDateResult struct {
	Found  bool    `json:"found"`
	Flight *Flight `json:"flight,omitempty"`
}
