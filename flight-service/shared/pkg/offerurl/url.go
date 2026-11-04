package offerurl

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/jalali"
)

// ForFlight returns a stored booking link only when it is on that provider's
// own site. Safarmarket and the other search APIs also embed the next seller's
// reservation token (for example ghasedak24.com). Those tokens expire and open
// a different site from the one named on the card, so the buy link falls back
// to the provider's search page for the same airports and day.
func ForFlight(source string, f flight.Flight) string {
	source = strings.ToLower(strings.TrimSpace(first(source, f.Source)))
	if link := SameSource(source, f.OfferURL); link != "" {
		return link
	}
	day := f.FlightDate
	if day.IsZero() {
		day = f.DepartAt
	}
	return Build(source, f.Origin, f.Destination, day)
}

// SameSource keeps an absolute URL only when its host is the collector's site.
func SameSource(source, link string) string {
	link = HTTPLink(link)
	if link == "" || !belongsTo(source, link) {
		return ""
	}
	return link
}

func belongsTo(source, link string) bool {
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	expected, ok := sourceHosts[strings.ToLower(strings.TrimSpace(source))]
	return ok && (host == expected || strings.HasSuffix(host, "."+expected))
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var sourceHosts = map[string]string{
	"alibaba":     "alibaba.ir",
	"flytoday":    "flytoday.ir",
	"mrbilit":     "mrbilit.com",
	"snapp":       "snapptrip.com",
	"safarmarket": "safarmarket.com",
	"trip":        "trip.ir",
	"tahagasht":   "tahagasht.com",
}

// Build is the public search page each collector already uses for this route and day.
// Providers do not return a checkout link for one fare, so this is the page where
// the traveler continues the purchase.
func Build(source, origin, destination string, day time.Time) string {
	origin = strings.ToUpper(strings.TrimSpace(origin))
	destination = strings.ToUpper(strings.TrimSpace(destination))
	if origin == "" || destination == "" || day.IsZero() {
		return ""
	}
	date := day.UTC().Format("2006-01-02")
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "alibaba":
		q := url.Values{"adult": {"1"}, "child": {"0"}, "infant": {"0"}, "departing": {jalali.Format(day)}}
		return "https://www.alibaba.ir/international/" + origin + "-" + destination + "?" + q.Encode()
	case "flytoday":
		return fmt.Sprintf(
			"https://www.flytoday.ir/flight/search?departure=%s,1&arrival=%s,1&departureDate=%s&adt=1&chd=0&inf=0&cabin=1&isAnyWhere=false",
			cityCode(origin), cityCode(destination), date,
		)
	case "trip":
		q := url.Values{
			"origin": {origin}, "destination": {destination}, "outbound": {date},
			"adults": {"1"}, "children": {"0"}, "infants": {"0"}, "cabin": {"economy"},
		}
		return "https://www.trip.ir/flight/booking/search?" + q.Encode()
	case "safarmarket":
		return "https://safarmarket.com/flights/c" + origin + "-c" + destination + "/" + date + "/0/allclasses/1adults/0children/0infants"
	case "snapp":
		from, to := snappCity(origin), snappCity(destination)
		section := "inter-flights"
		if iranCity(from) && iranCity(to) {
			section = "flights"
		}
		return "https://www.snapptrip.com/" + section + "/" + from + "_city/" + to + "_city?departureDate=" + date
	case "mrbilit":
		q := url.Values{"departureDate": {date}, "adultCount": {"1"}, "childCount": {"0"}, "infantCount": {"0"}}
		return "https://mrbilit.com/flights/" + origin + "-" + destination + "?" + q.Encode()
	case "tahagasht":
		q := url.Values{"origin": {origin}, "destination": {destination}, "date": {date}, "adult": {"1"}, "child": {"0"}, "infant": {"0"}}
		return "https://www.tahagasht.com/flight?" + q.Encode()
	default:
		return ""
	}
}

func cityCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THRALL", "IKA", "THR":
		return "thr"
	case "ISTALL", "IST", "SAW":
		return "ist"
	case "DXBALL", "DXB":
		return "dxb"
	case "NJFALL", "NJF":
		return "njf"
	case "AYTALL", "AYT":
		return "ayt"
	case "ESB":
		return "ank"
	case "GYD":
		return "bak"
	case "PEK", "PKX":
		return "bjs"
	case "PVG":
		return "sha"
	case "SVO":
		return "mow"
	default:
		return strings.ToLower(strings.TrimSpace(code))
	}
}

func snappCity(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THRALL", "IKA", "THR":
		return "THR"
	case "ISTALL", "IST", "SAW":
		return "IST"
	case "DXBALL":
		return "DXB"
	case "NJFALL":
		return "NJF"
	case "AYTALL":
		return "AYT"
	default:
		return strings.ToUpper(strings.TrimSpace(code))
	}
}

func iranCity(code string) bool {
	switch code {
	case "THR", "MHD", "AWZ", "SYZ", "TBZ", "BND", "IFN", "KIH", "SRY", "RAS":
		return true
	default:
		return false
	}
}
