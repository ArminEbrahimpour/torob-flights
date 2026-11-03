package pnr

import (
	"time"
)

type VerifyRequest struct {
	Airline    string `json:"airline"`
	PNR        string `json:"pnr"`
	LastName   string `json:"lastName"`
	FlightDate string `json:"flightDate"`
}

type Passenger struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	PaxType   string `json:"paxType,omitempty"`
}

type Flight struct {
	Origin        string    `json:"origin"`
	Destination   string    `json:"destination"`
	FlightNumber  string    `json:"flightNumber"`
	DepartureTime time.Time `json:"departureTime"`
	ArrivalTime   time.Time `json:"arrivalTime,omitempty"`
	Status        string    `json:"status,omitempty"`
}

type CheckResult struct {
	Airline           string      `json:"airline"`
	Verified          bool        `json:"verified"`
	BookingFound      bool        `json:"bookingFound"`
	LastNameMatched   bool        `json:"lastNameMatched"`
	FlightDateMatched bool        `json:"flightDateMatched"`
	PNR               string      `json:"pnr"`
	Passengers        []Passenger `json:"passengers,omitempty"`
	Flights           []Flight    `json:"flights,omitempty"`
	Source            string      `json:"source"`
	CheckedAt         time.Time   `json:"checkedAt"`
	ReasonCode        string      `json:"reasonCode,omitempty"`
	Message           string      `json:"message,omitempty"`
}

type CacheKey struct {
	Airline    string
	PNR        string
	LastName   string
	FlightDate string
}
