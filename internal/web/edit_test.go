package web

import "testing"

func TestParseDate(t *testing.T) {
	for in, want := range map[string]string{
		"02.04.2022": "2022-04-02", "2.4.2022": "2022-04-02", "04.2022": "2022-04", "2022": "2022",
		"29.02": "--02-29", "2022-04-02": "2022-04-02", "--09-16": "--09-16",
	} {
		if got, err := parseDate(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "31.02.2022", "вчера", "13.2022", "2022-4-2"} {
		if got, err := parseDate(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	for _, d := range [][2]string{{"2022-04-02", ""}, {"", "2022-04"}, {"", "2022"}, {"", "--09-16"}} {
		back, err := parseDate(typedDate(d[0], d[1]))
		if want := d[0] + d[1]; err != nil || back != want {
			t.Errorf("%v: typed %q back %q %v", d, typedDate(d[0], d[1]), back, err)
		}
	}
}
