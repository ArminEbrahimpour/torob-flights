package upstream

type PNRRequest struct {
	PNR             string `json:"pnr"`
	ContactLastName string `json:"contactLastName"`
	DepartureDate   int64  `json:"departureDate"`
}

type PNRLookupResponse struct {
	Content  *PNRContent `json:"content"`
	Messages []string    `json:"messages"`
	Skip     int         `json:"skip"`
	Take     int         `json:"take"`
	Total    int         `json:"total"`
}

type PNRContent struct {
	PNR                string              `json:"pnr"`
	Status             int                 `json:"status"`
	ContactPerson      *ContactPerson      `json:"contactPerson"`
	Travelers          []Traveler          `json:"travelers"`
	OriginDestinations []OriginDestination `json:"originDestinations"`
	Successed          bool                `json:"successed"`
}
type ContactPerson struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type Traveler struct {
	PaxType   int      `json:"paxType"`
	Tickets   []Ticket `json:"tickets"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
}

type Ticket struct {
	TicketNo      string `json:"ticketNo"`
	TicketStatus  string `json:"ticketStatus"`
	PaxStatus     string `json:"paxStatus"`
	SegmentRph    string `json:"segmentRph"`
	CheckinStatus int    `json:"checkingStatus"`
}

type OriginDestination struct {
	Origin            *AirportRef     `json:"origin"`
	Destination       *AirportRef     `json:"destination"`
	DepartureDateTime int64           `json:"departureDateTime"`
	ArrivalDateTime   int64           `json:"arrivalDateTime"`
	FlightSegments    []FlightSegment `json:"flightSegments"`
	Status            int             `json:"status"`
	CheckinStatus     int             `json:"chekinStatus"`
}

type AirportRef struct {
	Code string `json:"code"`
}
type FlightSegment struct {
	Origin                *AirportRef `json:"origin"`
	Destination           *AirportRef `json:"destination"`
	DepartureDateTime     int64       `json:"departureDateTime"`
	DepartureDateTimeZulu int64       `json:"departureDateTimeZulu"`
	ArrivalDateTime       int64       `json:"arrivalDateTime"`
	ArrivalDateTimeZulu   int64       `json:"arrivalDateTimeZulu"`
	FlightNumber          string      `json:"flightNumber"`
	Owner                 string      `json:"owner"`
	Status                int         `json:"status"`
}
