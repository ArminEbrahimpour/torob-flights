package jalali

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse accepts Gregorian or Jalali YYYY-MM-DD (Jalali if year in [1300,1699]).
func Parse(value string) (time.Time, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "/", "-"))
	parts := strings.Split(value, "-")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid date %q", value)
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	d, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, fmt.Errorf("invalid date %q", value)
	}
	if y >= 1300 && y < 1700 {
		g := toGregorian(y, m, d)
		return time.Date(g.Year(), g.Month(), g.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC), nil
}

func toGregorian(jy, jm, jd int) time.Time {
	jy -= 979
	days := 365*jy + (jy/33)*8 + ((jy%33)+3)/4 + 78 + jd
	if jm < 7 {
		days += (jm - 1) * 31
	} else {
		days += (jm-7)*30 + 186
	}
	gy := 1600 + 400*(days/146097)
	days %= 146097
	leap := true
	if days >= 36525 {
		days--
		gy += 100 * (days / 36524)
		days %= 36524
		if days >= 365 {
			days++
		} else {
			leap = false
		}
	}
	gy += 4 * (days / 1461)
	days %= 1461
	if days >= 366 {
		leap = false
		days--
		gy += days / 365
		days %= 365
	}
	gd := days + 1
	sal := []int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if leap {
		sal[2] = 29
	}
	gm := 1
	for gm = 1; gm <= 12; gm++ {
		v := sal[gm]
		if gd <= v {
			break
		}
		gd -= v
	}
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, time.UTC)
}

// Format prints the Jalali calendar date of t as YYYY-MM-DD.
func Format(t time.Time) string {
	day := time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
	year := day.Year() - 621
	for _, jy := range []int{year - 1, year, year + 1} {
		for jm := 1; jm <= 12; jm++ {
			last := 31
			if jm > 6 && jm < 12 {
				last = 30
			}
			if jm == 12 {
				last = 30
			}
			for jd := 1; jd <= last; jd++ {
				if jm == 12 && jd == 30 && toGregorian(jy, 12, 30).Equal(toGregorian(jy+1, 1, 1)) {
					continue
				}
				if toGregorian(jy, jm, jd).Equal(day) {
					return fmt.Sprintf("%04d-%02d-%02d", jy, jm, jd)
				}
			}
		}
	}
	return day.Format("2006-01-02")
}
