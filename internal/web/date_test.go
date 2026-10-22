package web

import "testing"

func TestShownDate(t *testing.T) {
	for _, c := range [][3]string{
		{"2026-09-16", "", "16.09.2026"},
		{"", "2026-09", "09.2026"},
		{"", "2026", "2026 г."},
		{"", "--09-16", "16.09, год неизвестен"},
		{"", "", "без даты"},
	} {
		if got := shownDate(c[0], c[1]); got != c[2] {
			t.Errorf("shownDate(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
