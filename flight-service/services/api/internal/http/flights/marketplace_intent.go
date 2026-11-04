package flights

import (
	"strings"

	flightdomain "github.com/airbar/flight-service/shared/domain/flight"
)

// Names people type. The code is the airport stored on flight rows: Tehran
// international flights are IKA, not the city code THR. Search still matches
// the other airports in that city.
var marketplaceCities = []struct {
	term string
	code string
}{
	{"امام خمینی", "IKA"},
	{"مهرآباد", "THR"},
	{"تهران", "IKA"},
	{"tehran", "IKA"},
	{"استانبول", "IST"},
	{"اسلامبول", "IST"},
	{"istanbul", "IST"},
	{"صبیحه", "IST"},
	{"آنکارا", "ESB"},
	{"ankara", "ESB"},
	{"آنتالیا", "AYT"},
	{"antalya", "AYT"},
	{"دبی", "DXB"},
	{"دوبی", "DXB"},
	{"dubai", "DXB"},
	{"نجف", "NJF"},
	{"najaf", "NJF"},
	{"بغداد", "BGW"},
	{"baghdad", "BGW"},
	{"اربیل", "EBL"},
	{"erbil", "EBL"},
	{"سلیمانیه", "ISU"},
	{"دوحه", "DOH"},
	{"doha", "DOH"},
	{"کویت", "KWI"},
	{"kuwait", "KWI"},
	{"مسقط", "MCT"},
	{"muscat", "MCT"},
	{"باکو", "GYD"},
	{"baku", "GYD"},
	{"ایروان", "EVN"},
	{"yerevan", "EVN"},
	{"وین", "VIE"},
	{"vienna", "VIE"},
	{"فرانکفورت", "FRA"},
	{"frankfurt", "FRA"},
	{"مسکو", "SVO"},
	{"moscow", "SVO"},
	{"دهلی نو", "DEL"},
	{"دهلی", "DEL"},
	{"delhi", "DEL"},
	{"لاهور", "LHE"},
	{"lahore", "LHE"},
	{"پوکت", "HKT"},
	{"phuket", "HKT"},
	{"آکتائو", "SCO"},
	{"aktau", "SCO"},
	{"پکن", "PEK"},
	{"beijing", "PEK"},
	{"گوانگژو", "CAN"},
	{"guangzhou", "CAN"},
	{"شانگهای", "PVG"},
	{"shanghai", "PVG"},
	{"شنژن", "SZX"},
	{"shenzhen", "SZX"},
	{"اورومچی", "URC"},
	{"urumqi", "URC"},
	{"مشهد", "MHD"},
	{"mashhad", "MHD"},
	{"شیراز", "SYZ"},
	{"shiraz", "SYZ"},
	{"اصفهان", "IFN"},
	{"isfahan", "IFN"},
	{"تبریز", "TBZ"},
	{"tabriz", "TBZ"},
	{"اهواز", "AWZ"},
	{"ahwaz", "AWZ"},
	{"بندرعباس", "BND"},
	{"چابهار", "ZBR"},
	{"گرگان", "GBT"},
	{"قشم", "GSM"},
	{"qeshm", "GSM"},
	{"کیش", "KIH"},
	{"kish", "KIH"},
	{"ساری", "SRY"},
	{"آبادان", "ABD"},
	{"اردبیل", "ADU"},
	{"یزد", "AZD"},
	{"بوشهر", "BUZ"},
	{"دزفول", "DEF"},
	{"گچساران", "GCH"},
	{"ایلام", "IIL"},
	{"کرمانشاه", "KSH"},
	{"کرمان", "KER"},
	{"ارومیه", "OMH"},
	{"عسلویه", "PGU"},
	{"اراک", "AJK"},
	{"کاشان", "KKS"},
	{"خارک", "KHK"},
	{"جاسک", "JSK"},
	{"نوشهر", "NSH"},
	{"رفسنجان", "RFS"},
	{"رامسر", "RZR"},
	{"رشت", "RAS"},
	{"سیرجان", "SYJ"},
	{"طبس", "TCX"},
	{"زاهدان", "ZAH"},
}

var marketplaceAirlines = []struct {
	term string
	code string
}{
	{"ایران ایر", "IR"},
	{"قشم ایر", "QB"},
	{"ماهان", "W5"},
	{"وارش", "VR"},
	{"کاسپین", "IV"},
	{"زاگرس", "ZV"},
	{"آسمان", "EP"},
}

func routePhrase(query string) bool {
	return strings.Contains(query, "به") || strings.Contains(query, "از")
}

func marketplaceIntent(query string) flightdomain.MarketplaceIntent {
	normalized := strings.NewReplacer("ي", "ی", "ك", "ک", "‌", " ").Replace(strings.ToLower(query))
	parts := strings.Split(normalized, "به")
	intent := flightdomain.MarketplaceIntent{}
	if len(parts) >= 2 {
		intent.Origin, intent.Destination = findCity(parts[0]), findCity(parts[1])
	} else {
		intent.Origin = findCity(normalized)
	}
	if intent.Destination == "" {
		for _, city := range marketplaceCities {
			if strings.Contains(normalized, city.term) && city.code != intent.Origin {
				intent.Destination = city.code
				break
			}
		}
	}
	for _, airline := range marketplaceAirlines {
		if strings.Contains(normalized, airline.term) {
			intent.Airline = airline.code
			break
		}
	}
	return intent
}

func placeCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if code := findCity(strings.NewReplacer("ي", "ی", "ك", "ک", "‌", " ").Replace(strings.ToLower(value))); code != "" {
		return code
	}
	return strings.ToUpper(value)
}

func findCity(text string) string {
	bestLen := 0
	best := ""
	for _, city := range marketplaceCities {
		if strings.Contains(text, city.term) && len(city.term) > bestLen {
			best = city.code
			bestLen = len(city.term)
		}
	}
	return best
}
