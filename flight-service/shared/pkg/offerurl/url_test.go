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
}

func TestForFlightKeepsStoredLink(t *testing.T) {
	f := flight.Flight{OfferURL: "https://example.test/ticket", Source: "alibaba", Origin: "IKA", Destination: "IST", FlightDate: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)}
	if got := ForFlight("alibaba", f); got != "https://example.test/ticket" {
		t.Fatalf("stored=%s", got)
	}
}
