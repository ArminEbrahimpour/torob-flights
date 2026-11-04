package offerurl

import (
	"strings"
	"testing"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/jalali"
)

func TestBuildUsesProviderSearchPage(t *testing.T) {
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	jalaliDay := jalali.Format(day)
	got := Build("alibaba", "THRALL", "ISTALL", day)
	want := "https://www.alibaba.ir/international/THRALL-ISTALL?adult=1&child=0&departing=" + jalaliDay + "&infant=0"
	if got != want {
		t.Fatalf("alibaba=%s", got)
	}
	if got := Build("flytoday", "IKA", "IST", day); got != "https://www.flytoday.ir/flight/search?departure=thr,1&arrival=ist,1&departureDate=2026-11-05&adt=1&chd=0&inf=0&cabin=1&isAnyWhere=false" {
		t.Fatalf("flytoday=%s", got)
	}
	if got := Build("safarmarket", "MHD", "IST", day); got != "https://safarmarket.com/flights/cMHD-cIST/2026-11-05/0/allclasses/1adults/0children/0infants" {
		t.Fatalf("safarmarket=%s", got)
	}
	if got := Build("snapp", "THRALL", "ISTALL", day); got != "https://www.snapptrip.com/inter-flights/THR_city/IST_city?departureDate=2026-11-05" {
		t.Fatalf("snapp=%s", got)
	}
	if got := Build("trip", "IKA", "IST", day); !strings.HasPrefix(got, "https://www.trip.ir/flight/booking/search?") || !strings.Contains(got, "outbound=2026-11-05") {
		t.Fatalf("trip=%s", got)
	}
	if got := Build("tahagasht", "IKA", "IST", day); !strings.Contains(got, "https://www.tahagasht.com/flights/result-async/s-") || !strings.Contains(got, "/THR-IST/2026-11-05/oneway-Economy-1-0-0") {
		t.Fatalf("tahagasht=%s", got)
	}
}

func TestForFlightReplacesTahagashtQueryLink(t *testing.T) {
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	f := flight.Flight{
		OfferURL:    "https://www.tahagasht.com/flight?origin=IKA&destination=IST&date=2026-11-12",
		Source:      "tahagasht",
		Origin:      "IKA",
		Destination: "IST",
		FlightDate:  day,
	}
	got := ForFlight("tahagasht", f)
	if !strings.Contains(got, "/flights/result-async/s-") || !strings.Contains(got, "/THR-IST/2026-11-12/oneway-Economy-1-0-0") {
		t.Fatalf("tahagasht=%s", got)
	}
}

func TestForFlightKeepsStoredLink(t *testing.T) {
	f := flight.Flight{OfferURL: "https://www.alibaba.ir/international/IKA-IST?adult=1", Source: "alibaba", Origin: "IKA", Destination: "IST", FlightDate: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)}
	if got := ForFlight("alibaba", f); got != "https://www.alibaba.ir/international/IKA-IST?adult=1" {
		t.Fatalf("stored=%s", got)
	}
}

func TestForFlightReplacesOtherSeller(t *testing.T) {
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	f := flight.Flight{
		OfferURL:    "https://ghasedak24.com/international/reservation/token?identifier=CR:abc&adl=1&chd=0&inf=0",
		Source:      "safarmarket",
		Origin:      "IKA",
		Destination: "IST",
		FlightDate:  day,
	}
	got := ForFlight("safarmarket", f)
	want := "https://safarmarket.com/flights/cIKA-cIST/2026-11-05/0/allclasses/1adults/0children/0infants"
	if got != want {
		t.Fatalf("safarmarket=%s", got)
	}
}
