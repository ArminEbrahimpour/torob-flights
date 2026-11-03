package mrbilit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMapFlightKeepsFareIdentityAndFields(t *testing.T) {
	var raw any
	if err := json.Unmarshal([]byte(`{
		"Id": 42,
		"Segments": [{"TotalTime":"2h 10m","Legs":[{"OriginCode":"IKA","DestinationCode":"IST","DepartureTime":"2026-12-01T08:00:00","ArrivalTime":"2026-12-01T10:10:00","AirlineCode":"TK","FlightNumber":"872","Stops":0,"Airline":{"EnglishTitle":"Turkish Airlines"}}]}],
		"Prices": [{"Baggage":30,"IsCharter":false,"PassengerFares":[{"PaxType":"ADL","TotalFare":123000000}] }]
	}`), &raw); err != nil {
		t.Fatal(err)
	}
	got, ok := mapFlight(raw, "THRALL", "ISTALL", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected flight to be mapped")
	}
	if got.ProposalID != "mrbilit_42" || got.PriceRial != 123000000 || got.PriceToman != 12300000 {
		t.Fatalf("unexpected identity or price: %+v", got)
	}
	if got.BaggageKG == nil || *got.BaggageKG != 30 || got.DurationMin == nil || *got.DurationMin != 130 {
		t.Fatalf("unexpected baggage or duration: %+v", got)
	}
	if got.FlightNumberNorm != "TK872" || got.AirlineName != "Turkish Airlines" {
		t.Fatalf("unexpected flight metadata: %+v", got)
	}
}
