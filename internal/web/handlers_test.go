package web

import "testing"

func TestGermanPeriodLabel(t *testing.T) {
	cases := []struct {
		readingDate string
		want        string
	}{
		{"2026-11-15", "November 2026"},
		{"2026-01-01", "Januar 2026"},
		{"2026-12-31", "Dezember 2026"},
		{"not-a-date", "not-a-date"},
	}

	for _, c := range cases {
		if got := germanPeriodLabel(c.readingDate); got != c.want {
			t.Errorf("germanPeriodLabel(%q) = %q, want %q", c.readingDate, got, c.want)
		}
	}
}
