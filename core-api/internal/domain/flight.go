package domain

// SearchQuery is the product-facing flight search contract. The core API owns
// this contract so clients are not coupled to the flight-service internals.
type SearchQuery struct {
	Origin        string
	Destination   string
	Date          string
	Source        string
	Airline       string
	FlightNumber  string
	MinPriceToman string
	MaxPriceToman string
	Charter       string
	MaxStops      string
	Page          string
	PageSize      string
	Mode          string
}

type SearchResult struct {
	Origin      string `json:"origin,omitempty"`
	Destination string `json:"destination,omitempty"`
	FlightDate  string `json:"flight_date,omitempty"`
	Source      string `json:"source,omitempty"`
	Total       int    `json:"total"`
	Page        int    `json:"page"`
	PageSize    int    `json:"page_size"`
	Offers      []any  `json:"offers"`
}

type RankedResult struct {
	Items []any `json:"items"`
}
