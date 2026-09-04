package store

import "time"

// Abrechnungsmonat is a periods.monat value ("YYYY-MM-01") - see CONTEXT.md.
type Abrechnungsmonat string

// Jahr returns the calendar year, or ok=false if m isn't a valid "YYYY-MM-01"
// value (CreatePeriod never validates Monat, see PeriodMonatTooEarlyError).
func (m Abrechnungsmonat) Jahr() (jahr int, ok bool) {
	t, err := time.Parse("2006-01-02", string(m))
	if err != nil {
		return 0, false
	}
	return t.Year(), true
}
