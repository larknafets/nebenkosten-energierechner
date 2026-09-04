package web

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var germanMonths = [...]string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// germanPeriodLabel renders a period's ReadingDate ("YYYY-MM-DD") as its
// German month name and year (e.g. "November 2026"), for the Dashboard
// heading (Ticket #17 Nachtrag - no "Dashboard -" prefix). Falls back to the
// raw string if it isn't a parseable date.
func germanPeriodLabel(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return fmt.Sprintf("%s %d", germanMonths[t.Month()-1], t.Year())
}

var germanMonthsShort = [...]string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

// germanPeriodLabelShort is germanPeriodLabel's abbreviated form (e.g. "Nov
// 2026"), for the Verlauf month labels (Ticket #19) where every row needs
// to fit next to a bar.
func germanPeriodLabelShort(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return fmt.Sprintf("%s %d", germanMonthsShort[t.Month()-1], t.Year())
}

// groupThousandsDE inserts "." as the German thousands separator into a
// German-formatted decimal string's integer part (comma already the
// decimal separator, e.g. "2345,43" -> "2.345,43"; "-12345" -> "-12.345").
// Shared by every formatDecimalDE*/formatEuroDE variant below.
func groupThousandsDE(s string) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, dec, hasDec := strings.Cut(s, ",")

	var grouped strings.Builder
	n := len(intPart)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteByte(intPart[i])
	}

	out := grouped.String()
	if hasDec {
		out += "," + dec
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatDecimalDE renders a float64 the way germanPeriodLabel renders a
// date: German convention, decimal comma instead of point (Ticket #36),
// rounded to at most 2 decimal places (Ticket #37 - raw calc.go values
// like WWAnteil/WaermeMWh carry floating-point noise past 2 places, e.g.
// "25,333333333333332"). Not padded - 'f'/-1 on the rounded value keeps
// "0,7" as "0,7", not "0,70"; use formatEuroDE where padding to exactly 2
// places is wanted (EUR amounts). Thousands grouped with "." (e.g.
// "2.345,43").
func formatDecimalDE(x float64) string {
	rounded := math.Round(x*100) / 100
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(rounded, 'f', -1, 64), ".", ","))
}

// formatEuroDE renders a float64 as a German-formatted EUR amount, always
// padded to exactly 2 decimal places (Ticket #40 - a currency amount reads
// as "45,00", not "45"). EUR amounts are already Round2'd (kaufmännisch,
// Issue #8) before reaching here, so the fixed 2-place formatting doesn't
// change the value, only pads its display. Thousands grouped with "."
// (e.g. "2.345,00").
func formatEuroDE(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 2, 64), ".", ","))
}

// formatDecimalDE0 renders a float64 rounded to a whole number, no decimal
// places at all (preview: Jahressummen-Karte's Wohnungsgröße/
// Flurstücksgröße-Badges). Thousands grouped with "." (e.g. "2.345").
func formatDecimalDE0(x float64) string {
	return groupThousandsDE(strconv.FormatFloat(math.Round(x), 'f', 0, 64))
}

// formatDecimalDE1 renders a float64 German-formatted, always padded to
// exactly 1 decimal place (Ticket #75 - the Personen-Schnitt reads as
// "2,5", not "2" or "2,50"). Thousands grouped with "." (e.g. "2.345,0").
func formatDecimalDE1(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 1, 64), ".", ","))
}

// formatDecimalDE2 renders a float64 German-formatted, always padded to
// exactly 2 decimal places (Ticket #76 - a displayed Verbrauchswert reads as
// "0,70"/"107,00", not "0,7"/"107", analog to formatEuroDE but without a
// currency unit). Thousands grouped with "." (e.g. "2.345,43").
func formatDecimalDE2(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 2, 64), ".", ","))
}

// formatDatumDE renders a period's ReadingDate ("YYYY-MM-DD") in the German
// DD.MM.YYYY form (Ticket #36). Falls back to the raw string if it isn't a
// parseable date, same convention as germanPeriodLabel.
func formatDatumDE(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return t.Format("02.01.2006")
}

// formatDatumZeitDE renders a build timestamp (RFC3339, as set via -ldflags
// at build time - Ticket #48) in the German DD.MM.YYYY, HH:MM form. Falls
// back to the raw string if it isn't a parseable RFC3339 timestamp.
func formatDatumZeitDE(buildDate string) string {
	t, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		return buildDate
	}
	return t.Local().Format("02.01.2006, 15:04")
}

// parseDecimalDE is formatDecimalDE's inverse: German decimal-comma input
// ("25,33") to float64. CSV cells always use this convention (Ticket #54).
// parseDecimalDE is formatDecimalDE/groupThousandsDE's inverse: strips the
// "." thousands separator from the integer part (Issue #87 - a value ≥
// 1000 like "1.000" was misparsed as 1.0, treating the grouping dot as a
// decimal point), then converts the "," decimal separator to ".".
func parseDecimalDE(s string) (float64, error) {
	s = strings.TrimSpace(s)
	intPart, dec, hasDec := strings.Cut(s, ",")
	intPart = strings.ReplaceAll(intPart, ".", "")
	if hasDec {
		s = intPart + "." + dec
	} else {
		s = intPart
	}
	return strconv.ParseFloat(s, 64)
}
