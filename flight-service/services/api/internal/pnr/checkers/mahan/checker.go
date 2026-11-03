package mahan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/airbar/flight-service/services/api/internal/pnr"
	"github.com/airbar/flight-service/services/api/internal/pnr/checkers/mahan/upstream"
)

var ErrUpstream = errors.New("mahan upstream error")

type Checker struct {
	client   *upstream.Client
	location *time.Location
	now      func() time.Time
}

func NewChecker(client *upstream.Client, location *time.Location) *Checker {
	return &Checker{client: client, location: location, now: time.Now}
}

func (c *Checker) Airline() string { return "mahan" }

func (c *Checker) Check(ctx context.Context, req pnr.VerifyRequest) (pnr.CheckResult, error) {
	flightDate, err := time.Parse("2006-01-02", req.FlightDate)
	if err != nil {
		return pnr.CheckResult{}, fmt.Errorf("invalid flight date: %w", err)
	}
	upstreamRequest := upstream.PNRRequest{
		PNR:             req.PNR,
		ContactLastName: req.LastName,
		DepartureDate:   time.Date(flightDate.Year(), flightDate.Month(), flightDate.Day(), 0, 0, 0, 0, time.UTC).UnixMilli(),
	}
	response, err := c.client.Lookup(ctx, upstreamRequest)
	if err != nil {
		return pnr.CheckResult{}, err
	}
	return c.mapResult(req, response), nil
}

func (c *Checker) mapResult(request pnr.VerifyRequest, response upstream.PNRLookupResponse) pnr.CheckResult {
	result := pnr.CheckResult{
		Airline:    "mahan",
		PNR:        request.PNR,
		Source:     "mahan",
		CheckedAt:  c.now().UTC(),
		ReasonCode: "booking_not_found",
		Message:    "booking was not found for the supplied details",
	}
	if response.Content == nil {
		if len(response.Messages) > 0 {
			result.Message = strings.Join(response.Messages, "; ")
		}
		return result
	}

	content := response.Content
	if !content.Successed && strings.TrimSpace(content.PNR) == "" && len(content.Travelers) == 0 && len(content.OriginDestinations) == 0 {
		if len(response.Messages) > 0 {
			result.Message = strings.Join(response.Messages, "; ")
		}
		return result
	}

	result.BookingFound = true
	result.PNR = strings.ToUpper(strings.TrimSpace(content.PNR))
	if result.PNR == "" {
		result.PNR = request.PNR
	}

	requestedLastName := normalizeName(request.LastName)
	if content.ContactPerson != nil && normalizeName(content.ContactPerson.LastName) == requestedLastName {
		result.LastNameMatched = true
	}
	for _, traveler := range content.Travelers {
		if normalizeName(traveler.LastName) == requestedLastName {
			result.LastNameMatched = true
		}
		result.Passengers = append(result.Passengers, pnr.Passenger{
			FirstName: traveler.FirstName,
			LastName:  traveler.LastName,
			PaxType:   paxTypeName(traveler.PaxType),
		})
	}

	for _, od := range content.OriginDestinations {
		if len(od.FlightSegments) == 0 {
			result.Flights = append(result.Flights, pnr.Flight{
				Origin:        airportCode(od.Origin),
				Destination:   airportCode(od.Destination),
				DepartureTime: fromMillis(od.DepartureDateTime),
				ArrivalTime:   fromMillis(od.ArrivalDateTime),
				Status:        fmt.Sprintf("status_%d", od.Status),
			})
			continue
		}
		for _, segment := range od.FlightSegments {
			departureMillis := segment.DepartureDateTimeZulu
			if departureMillis == 0 {
				departureMillis = segment.DepartureDateTime
			}
			arrivalMillis := segment.ArrivalDateTimeZulu
			if arrivalMillis == 0 {
				arrivalMillis = segment.ArrivalDateTime
			}
			result.Flights = append(result.Flights, pnr.Flight{
				Origin:        airportCode(segment.Origin),
				Destination:   airportCode(segment.Destination),
				FlightNumber:  segment.FlightNumber,
				DepartureTime: fromMillis(departureMillis),
				ArrivalTime:   fromMillis(arrivalMillis),
				Status:        fmt.Sprintf("status_%d", segment.Status),
			})
		}
	}

	for _, f := range result.Flights {
		if sameDate(f.DepartureTime.In(c.location), request.FlightDate) || sameDate(f.DepartureTime.UTC(), request.FlightDate) {
			result.FlightDateMatched = true
			break
		}
	}

	result.Verified = result.BookingFound && result.LastNameMatched && result.FlightDateMatched
	switch {
	case result.Verified:
		result.ReasonCode = "verified"
		result.Message = "passenger booking verified"
	case !result.LastNameMatched:
		result.ReasonCode = "last_name_mismatch"
		result.Message = "booking was found but the passenger last name did not match"
	case !result.FlightDateMatched:
		result.ReasonCode = "flight_date_mismatch"
		result.Message = "booking was found but no itinerary segment matched the requested flight date"
	}
	return result
}

func IsUpstreamError(err error) bool {
	return errors.Is(err, ErrUpstream) || errors.Is(err, upstream.ErrUpstream)
}

func sameDate(value time.Time, expected string) bool {
	return !value.IsZero() && value.Format("2006-01-02") == expected
}

func normalizeName(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '\'' {
			return -1
		}
		return r
	}, value)
}

func airportCode(ref *upstream.AirportRef) string {
	if ref == nil {
		return ""
	}
	return ref.Code
}

func paxTypeName(value int) string {
	switch value {
	case 0:
		return "adult"
	case 1:
		return "child"
	case 2:
		return "infant"
	default:
		return fmt.Sprintf("type_%d", value)
	}
}

func fromMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
