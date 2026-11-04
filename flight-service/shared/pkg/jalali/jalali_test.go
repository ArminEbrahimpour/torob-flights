package jalali

import "testing"

func TestFormatRoundTrip(t *testing.T) {
	for _, jalali := range []string{"1401-06-06", "1403-01-01", "1405-08-14"} {
		day, err := Parse(jalali)
		if err != nil {
			t.Fatal(err)
		}
		if got := Format(day); got != jalali {
			t.Fatalf("Format(Parse(%s))=%s", jalali, got)
		}
	}
}
