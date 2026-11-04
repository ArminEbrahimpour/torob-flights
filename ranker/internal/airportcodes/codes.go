package airportcodes

import "strings"

// groups lists every code that refers to the same city. Ranked flights store
// the concrete airport (IKA, IST), not the city code (THR).
var groups = [][]string{
	{"THR", "IKA", "THRALL"},
	{"IST", "SAW", "ISTALL"},
	{"DXB", "DXBALL"},
	{"NJF", "NJFALL"},
	{"AYT", "AYTALL"},
	{"ANK", "ESB"},
	{"MOW", "SVO"},
	{"BJS", "PEK", "PKX"},
	{"SHA", "PVG"},
}

// Codes returns the airport, city, and collector codes that should match a
// stored flight. Unknown codes are returned unchanged.
func Codes(code string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	for _, group := range groups {
		for _, member := range group {
			if member == code {
				out := make([]string, len(group))
				copy(out, group)
				return out
			}
		}
	}
	return []string{code}
}
