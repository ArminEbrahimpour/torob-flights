package airportcodes

import "testing"

func TestCodesTehranIncludesImamKhomeini(t *testing.T) {
	got := Codes("THR")
	found := false
	for _, code := range got {
		if code == "IKA" {
			found = true
		}
	}
	if !found {
		t.Fatalf("THR codes=%v", got)
	}
}

func TestCodesIstanbulIncludesBothAirports(t *testing.T) {
	got := Codes("IST")
	has := map[string]bool{}
	for _, code := range got {
		has[code] = true
	}
	if !has["IST"] || !has["SAW"] || !has["ISTALL"] {
		t.Fatalf("IST codes=%v", got)
	}
}
