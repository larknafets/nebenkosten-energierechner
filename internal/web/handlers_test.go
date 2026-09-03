package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func TestKategorien(t *testing.T) {
	k := kosten{
		Strom: &calc.StromErgebnis{
			W2AnteilKWh: 305,
			KostenW2:    67.20,
		},
		Wasser: &calc.WasserErgebnis{
			FrischwasserW1:       20,
			FrischwasserW2:       15,
			KostenFrischwasserW1: 29.20,
			KostenFrischwasserW2: 21.90,
			KostenAbwasserW1:     97.40,
			KostenAbwasserW2:     73.05,
		},
		Heizung: &calc.HeizungErgebnis{
			WaermeW1MWh:     14.2,
			WaermeW2MWh:     9.6,
			KostenHeizungW1: 112.40,
			KostenHeizungW2: 84.10,
		},
	}

	t.Run("Wohnung 1 has no eigene Strom-Position", func(t *testing.T) {
		kats := kategorien(1, k)
		for _, kat := range kats {
			if kat.Label == "Strom" {
				t.Fatalf("Wohnung 1 sollte keine Strom-Kategorie haben, got %+v", kat)
			}
		}
		if len(kats) != 2 {
			t.Fatalf("want 2 Kategorien (Heizung, Wasser), got %d: %+v", len(kats), kats)
		}
	})

	t.Run("Wohnung 2 kombiniert Frischwasser+Abwasser zu einer Wasser-Kategorie", func(t *testing.T) {
		kats := kategorien(2, k)
		if len(kats) != 3 {
			t.Fatalf("want 3 Kategorien, got %d: %+v", len(kats), kats)
		}
		var wasser *kategorie
		for i := range kats {
			if kats[i].Label == "Wasser" {
				wasser = &kats[i]
			}
		}
		if wasser == nil {
			t.Fatal("keine Wasser-Kategorie gefunden")
		}
		if want := 21.90 + 73.05; wasser.Kosten != want {
			t.Errorf("Wasser.Kosten = %v, want %v", wasser.Kosten, want)
		}
		if wasser.Verbrauch != 15 {
			t.Errorf("Wasser.Verbrauch = %v, want 15", wasser.Verbrauch)
		}
	})

	t.Run("Prozentanteile summieren sich auf 100", func(t *testing.T) {
		kats := kategorien(2, k)
		var sum float64
		for _, kat := range kats {
			sum += kat.ProzentGesamt
		}
		if sum < 99.9 || sum > 100.1 {
			t.Errorf("Summe der Prozentanteile = %v, want ~100", sum)
		}
	})

	t.Run("Gesamtbetrag 0 erzeugt kein NaN", func(t *testing.T) {
		zero := kosten{
			Strom:   &calc.StromErgebnis{},
			Wasser:  &calc.WasserErgebnis{},
			Heizung: &calc.HeizungErgebnis{},
		}
		for _, kat := range kategorien(1, zero) {
			if kat.ProzentGesamt != 0 {
				t.Errorf("ProzentGesamt = %v, want 0 for zero total", kat.ProzentGesamt)
			}
		}
	})
}

func TestGermanPeriodLabelShort(t *testing.T) {
	cases := []struct {
		readingDate string
		want        string
	}{
		{"2026-11-15", "Nov 2026"},
		{"2026-03-01", "Mär 2026"},
		{"not-a-date", "not-a-date"},
	}
	for _, c := range cases {
		if got := germanPeriodLabelShort(c.readingDate); got != c.want {
			t.Errorf("germanPeriodLabelShort(%q) = %q, want %q", c.readingDate, got, c.want)
		}
	}
}

func TestVerlaufMonate(t *testing.T) {
	// Neueste Periode (index 0) hat den kleineren Gesamtbetrag - eine
	// aeltere, teurere Periode muss ueber 100% hinauslaufen (Ticket #19:
	// Skala ist relativ zum neuesten Monat, nicht gestaucht).
	neu := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 20, W2AnteilKWh: 123},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 5, KostenAbwasserW2: 5},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 10},
	}
	alt := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 40},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 10, KostenAbwasserW2: 10},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 20},
	}
	periods := []periodKosten{
		{ReadingDate: "2026-11-15", K: neu},
		{ReadingDate: "2026-10-01", K: alt},
	}

	monate := verlaufMonate(2, periods)
	if len(monate) != 2 {
		t.Fatalf("want 2 Monate, got %d", len(monate))
	}
	if monate[0].Label != "Nov 2026" || monate[1].Label != "Okt 2026" {
		t.Errorf("Label = %q / %q, want \"Nov 2026\" / \"Okt 2026\"", monate[0].Label, monate[1].Label)
	}
	if !monate[0].IsCurrent || monate[1].IsCurrent {
		t.Errorf("nur der erste (neueste) Monat soll IsCurrent sein, got %+v / %+v", monate[0], monate[1])
	}
	if monate[0].Gesamtbetrag != 40 {
		t.Errorf("neuester Gesamtbetrag = %v, want 40", monate[0].Gesamtbetrag)
	}
	if monate[1].Gesamtbetrag != 80 {
		t.Errorf("aelterer Gesamtbetrag = %v, want 80", monate[1].Gesamtbetrag)
	}

	// Segmente tragen Label/Verbrauch/Einheit fuer die Verbrauchs-Ansicht
	// (Ticket #39) - Strom ist bei Wohnung 2 immer das erste Segment.
	strom := monate[0].Segmente[0]
	if strom.Label != "Strom" || strom.Verbrauch != 123 || strom.Einheit != "kWh" {
		t.Errorf("Strom-Segment = %+v, want Label=Strom Verbrauch=123 Einheit=kWh", strom)
	}

	// Skala = neuester Gesamtbetrag (40). Der aeltere Monat kostet doppelt
	// so viel -> jedes Segment soll auf 200% seines eigenen Kosten-Anteils
	// an 40 EUR kommen, nicht auf 100% gestaucht werden.
	var altSum float64
	for _, seg := range monate[1].Segmente {
		altSum += seg.ProzentNeuestesGesamt
	}
	if altSum < 199 || altSum > 201 {
		t.Errorf("Summe der Prozentanteile des aelteren Monats = %v, want ~200 (laeuft ueber den Rand)", altSum)
	}

	t.Run("leere Periodenliste", func(t *testing.T) {
		if got := verlaufMonate(2, nil); got != nil {
			t.Errorf("want nil for empty input, got %+v", got)
		}
	})

	t.Run("neuester Gesamtbetrag 0 erzeugt kein NaN", func(t *testing.T) {
		zero := kosten{Strom: &calc.StromErgebnis{}, Wasser: &calc.WasserErgebnis{}, Heizung: &calc.HeizungErgebnis{}}
		monate := verlaufMonate(2, []periodKosten{{ReadingDate: "2026-11-15", K: zero}})
		for _, seg := range monate[0].Segmente {
			if seg.ProzentNeuestesGesamt != 0 {
				t.Errorf("ProzentNeuestesGesamt = %v, want 0", seg.ProzentNeuestesGesamt)
			}
		}
	})
}

func TestMitJahrestrennern(t *testing.T) {
	leer := kosten{Strom: &calc.StromErgebnis{}, Wasser: &calc.WasserErgebnis{KostenFrischwasserW2: 10}, Heizung: &calc.HeizungErgebnis{}}

	t.Run("Dezember-Eintrag bekommt davor einen Jahrestrenner mit Jahreswert", func(t *testing.T) {
		periods := []periodKosten{
			{ReadingDate: "2026-01-15", K: leer}, // 10
			{ReadingDate: "2025-12-15", K: leer}, // 10 - Dezember 2025
			{ReadingDate: "2025-11-15", K: leer}, // 10
		}
		monate := verlaufMonate(2, periods)
		eintraege := mitJahrestrennern(monate, periods)

		if len(eintraege) != 4 {
			t.Fatalf("want 4 Eintraege (3 Monate + 1 Trenner), got %d: %+v", len(eintraege), eintraege)
		}
		if eintraege[0].Monat == nil || eintraege[0].Jahrestrenner != nil {
			t.Errorf("Eintrag 0 soll Jan 2026 (Monat) sein, got %+v", eintraege[0])
		}
		if eintraege[1].Jahrestrenner == nil {
			t.Fatalf("Eintrag 1 soll der Jahrestrenner vor Dezember 2025 sein, got %+v", eintraege[1])
		}
		if eintraege[1].Jahrestrenner.Jahr != 2025 {
			t.Errorf("Jahrestrenner.Jahr = %d, want 2025", eintraege[1].Jahrestrenner.Jahr)
		}
		// Jahreswert 2025 = Dez (10) + Nov (10) = 20 - Januar 2026 gehoert
		// nicht zu Kalenderjahr 2025.
		if eintraege[1].Jahrestrenner.Jahreswert != 20 {
			t.Errorf("Jahrestrenner.Jahreswert = %v, want 20", eintraege[1].Jahrestrenner.Jahreswert)
		}
		if eintraege[2].Monat == nil || eintraege[2].Monat.Label != "Dez 2025" {
			t.Errorf("Eintrag 2 soll Dez 2025 (Monat) sein, got %+v", eintraege[2])
		}
		if eintraege[3].Monat == nil || eintraege[3].Monat.Label != "Nov 2025" {
			t.Errorf("Eintrag 3 soll Nov 2025 (Monat) sein, got %+v", eintraege[3])
		}
	})

	t.Run("kein Dezember im Jahr -> kein Trenner", func(t *testing.T) {
		periods := []periodKosten{
			{ReadingDate: "2026-02-15", K: leer},
			{ReadingDate: "2026-01-15", K: leer},
		}
		monate := verlaufMonate(2, periods)
		eintraege := mitJahrestrennern(monate, periods)
		if len(eintraege) != 2 {
			t.Fatalf("want 2 Eintraege, keine Trenner, got %d: %+v", len(eintraege), eintraege)
		}
		for _, e := range eintraege {
			if e.Jahrestrenner != nil {
				t.Errorf("kein Dezember vorhanden -> es sollte kein Jahrestrenner erscheinen, got %+v", e)
			}
		}
	})

	t.Run("leere Liste", func(t *testing.T) {
		if got := mitJahrestrennern(nil, nil); got != nil {
			t.Errorf("want nil, got %+v", got)
		}
	})
}

func TestParseHeizungGewichtung(t *testing.T) {
	cases := []struct {
		raw     string
		want    float64
		wantErr bool
	}{
		{"0.7", 0.7, false},
		{"0.6", 0.6, false},
		{"0.5", 0.5, false},
		{"0.8", 0, true},
		{"70", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := parseHeizungGewichtung(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseHeizungGewichtung(%q) = %v, want error", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHeizungGewichtung(%q) unexpected error: %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("parseHeizungGewichtung(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// TestParsePeriodInput_NoQMFields verifies the Ablesung-Formular (Issue #61)
// no longer needs qm_1/qm_2 form fields - parsePeriodInput must succeed
// without them, since Wohnungsgröße is edited on /stammdaten now.
func TestParsePeriodInput_NoQMFields(t *testing.T) {
	apartments := []store.Apartment{{ID: 1, Name: "Wohnung 1"}, {ID: 2, Name: "Wohnung 2"}}

	form := url.Values{
		"reading_date":       {"2026-11-01"},
		"strompreis":         {"0.22"},
		"frischwasser_preis": {"1.46"},
		"abwasser_preis":     {"4.87"},
		"einspeisung_preis":  {"0.08"},
		"heizung_gewichtung": {"0.7"},
		"personen_1":         {"2"},
		"personen_2":         {"1"},
	}
	for _, key := range store.MeterKeys {
		form.Set(key, "0")
	}

	r, err := http.NewRequest(http.MethodPost, "/ablesungen", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	in, err := parsePeriodInput(r, apartments)
	if err != nil {
		t.Fatalf("parsePeriodInput without qm_1/qm_2: %v", err)
	}
	if in.Personen[1] != 2 || in.Personen[2] != 1 {
		t.Errorf("Personen = %v, want {1:2, 2:1}", in.Personen)
	}
}

// TestCSVHeader_NoQMColumns verifies Issue #61's hard cut: the CSV
// export/import format no longer has qm_1/qm_2 columns.
func TestCSVHeader_NoQMColumns(t *testing.T) {
	for _, col := range csvHeader {
		if col == "qm_1" || col == "qm_2" {
			t.Errorf("csvHeader contains %q, want no qm_1/qm_2 columns (Issue #61 moved Wohnungsgröße to /stammdaten)", col)
		}
	}
	last := csvHeader[len(csvHeader)-1]
	if last != "personen_2" {
		t.Errorf("csvHeader last column = %q, want personen_2", last)
	}
}

func TestParseDecimalDE(t *testing.T) {
	cases := []struct {
		raw     string
		want    float64
		wantErr bool
	}{
		{"25,33", 25.33, false},
		{"0", 0, false},
		{" 116,23 ", 116.23, false},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := parseDecimalDE(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseDecimalDE(%q): want error, got %v", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDecimalDE(%q): unexpected error: %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("parseDecimalDE(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// csvRow builds one CSV data row (csvHeader order) with the given
// reading_date/strom_gesamt, everything else at a fixed valid default -
// the exact values elsewhere don't matter for the tests using this.
func csvRow(readingDate string, stromGesamt string) string {
	return strings.Join([]string{
		readingDate, stromGesamt, "0", "0", "0", "0", "0", "0", "0", "0", "0",
		"0,22", "1,46", "4,87", "0,7", "0,08",
		"2", "1",
	}, ";")
}

func TestParseImportCSV_RoundTrip(t *testing.T) {
	csvText := strings.Join(csvHeader, ";") + "\n" +
		csvRow("2026-06-01", "100") + "\n" +
		csvRow("2026-07-01", "210") + "\n"

	rows, err := parseImportCSV(strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("parseImportCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].line != 2 || rows[1].line != 3 {
		t.Errorf("line numbers = [%d, %d], want [2, 3]", rows[0].line, rows[1].line)
	}
	if rows[1].input.Readings["strom_gesamt"] != 210 {
		t.Errorf("Readings[strom_gesamt] = %v, want 210", rows[1].input.Readings["strom_gesamt"])
	}
	if rows[1].input.Strompreis != 0.22 {
		t.Errorf("Strompreis = %v, want 0.22", rows[1].input.Strompreis)
	}
	if rows[1].input.HeizungWaermeGewichtung != 0.7 {
		t.Errorf("HeizungWaermeGewichtung = %v, want 0.7", rows[1].input.HeizungWaermeGewichtung)
	}
	if rows[1].input.Personen[1] != 2 || rows[1].input.Personen[2] != 1 {
		t.Errorf("Personen = %v, want {1:2, 2:1}", rows[1].input.Personen)
	}
}

func TestParseImportCSV_WithBOM(t *testing.T) {
	csvText := "\xEF\xBB\xBF" + strings.Join(csvHeader, ";") + "\n" + csvRow("2026-06-01", "100") + "\n"
	rows, err := parseImportCSV(strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("parseImportCSV: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
}

func TestParseImportCSV_MissingColumn(t *testing.T) {
	header := strings.Join(csvHeader[:len(csvHeader)-1], ";") // drop personen_2
	csvText := header + "\n"
	if _, err := parseImportCSV(strings.NewReader(csvText)); err == nil {
		t.Fatal("parseImportCSV: want error for missing column, got nil")
	}
}

func TestParseImportCSV_HardErrorHasLineNumber(t *testing.T) {
	csvText := strings.Join(csvHeader, ";") + "\n" +
		csvRow("2026-06-01", "100") + "\n" +
		csvRow("2026-07-01", "nicht-numerisch") + "\n"

	_, err := parseImportCSV(strings.NewReader(csvText))
	if err == nil {
		t.Fatal("parseImportCSV: want error for non-numeric Zählerstand, got nil")
	}
	if !strings.Contains(err.Error(), "Zeile 3") {
		t.Errorf("error = %q, want it to mention Zeile 3", err.Error())
	}
}

func TestImportWarnings_NegativeAndOutlier(t *testing.T) {
	rows := []importRow{
		{line: 2, input: store.PeriodInput{ReadingDate: "2026-01-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 1000})}},
		{line: 3, input: store.PeriodInput{ReadingDate: "2026-02-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 1100})}},
		{line: 4, input: store.PeriodInput{ReadingDate: "2026-03-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 1200})}},
		{line: 5, input: store.PeriodInput{ReadingDate: "2026-04-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 1300})}},
		// consumption 1050 vs. previous 3 avg 100 -> Ausreißer.
		{line: 6, input: store.PeriodInput{ReadingDate: "2026-05-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 2350})}},
		// negativer Verbrauch: 2300 < 2350.
		{line: 7, input: store.PeriodInput{ReadingDate: "2026-06-01", Readings: baseReadingsForImportTest(map[string]float64{"strom_gesamt": 2300})}},
	}
	ids := make([]int64, len(rows))
	for i := range ids {
		ids[i] = int64(i + 1)
	}

	warnings := importWarnings(rows, ids)

	var hasOutlier, hasNegative bool
	for _, w := range warnings {
		if strings.Contains(w, "Zeile 6") && strings.Contains(w, "Ausreißer") {
			hasOutlier = true
		}
		if strings.Contains(w, "Zeile 7") && strings.Contains(w, "negativer Verbrauch") {
			hasNegative = true
		}
	}
	if !hasOutlier {
		t.Errorf("expected an Ausreißer warning for Zeile 6, got %v", warnings)
	}
	if !hasNegative {
		t.Errorf("expected a negativer-Verbrauch warning for Zeile 7, got %v", warnings)
	}
}

func baseReadingsForImportTest(overrides map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(store.MeterKeys))
	for _, k := range store.MeterKeys {
		out[k] = 0
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func TestPeriodListItems(t *testing.T) {
	periods := []store.PeriodSummary{
		{ID: 3, ReadingDate: "2026-08-01"},
		{ID: 2, ReadingDate: "2026-07-01"},
		{ID: 1, ReadingDate: "2026-06-01"},
	}

	items := periodListItems(periods)
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].Label != "01.08.2026 (01.07.2026–01.08.2026)" {
		t.Errorf("items[0].Label = %q, want %q", items[0].Label, "01.08.2026 (01.07.2026–01.08.2026)")
	}
	if items[2].Label != "01.06.2026 (keine Vorperiode)" {
		t.Errorf("items[2].Label (oldest) = %q, want %q", items[2].Label, "01.06.2026 (keine Vorperiode)")
	}
	if items[0].ID != 3 || items[2].ID != 1 {
		t.Errorf("ids not preserved: %+v", items)
	}
}

func TestPeriodOverviewRows(t *testing.T) {
	periods := []store.PeriodSummary{
		{ID: 3, ReadingDate: "2026-08-01"},
		{ID: 2, ReadingDate: "2026-07-01"},
		{ID: 1, ReadingDate: "2026-06-01"},
	}

	rows := periodOverviewRows(periods)
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if rows[0].ReadingDate != "01.08.2026" || rows[0].Zeitraum != "01.07.2026–01.08.2026" {
		t.Errorf("rows[0] = %+v, want ReadingDate=01.08.2026 Zeitraum=01.07.2026–01.08.2026", rows[0])
	}
	if rows[2].ReadingDate != "01.06.2026" || rows[2].Zeitraum != "keine Vorperiode" {
		t.Errorf("rows[2] (oldest) = %+v, want ReadingDate=01.06.2026 Zeitraum=\"keine Vorperiode\"", rows[2])
	}
}

func TestIngressBase(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"/", ""},
		{"/api/hassio_ingress/xyz/", "/api/hassio_ingress/xyz"},
		{"/api/hassio_ingress/xyz", "/api/hassio_ingress/xyz"},
	}
	for _, c := range cases {
		if got := ingressBase(c.header); got != c.want {
			t.Errorf("ingressBase(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

func TestFormatDecimalDE(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{0.22, "0,22"},
		{0.7, "0,7"},
		{200, "200"},
		{45.5, "45,5"},
		{-3.14, "-3,14"},
		{0, "0"},
		// Ticket #37: max. 2 Nachkommastellen, aber nicht auffuellen.
		{25.333333333333332, "25,33"},
		{0.46999999999999975, "0,47"},
		{1406.3333333333333, "1406,33"},
	}
	for _, c := range cases {
		if got := formatDecimalDE(c.x); got != c.want {
			t.Errorf("formatDecimalDE(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestFormatEuroDE(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{45, "45,00"},
		{45.5, "45,50"},
		{0.22, "0,22"},
		{45.999, "46,00"},
		{0, "0,00"},
	}
	for _, c := range cases {
		if got := formatEuroDE(c.x); got != c.want {
			t.Errorf("formatEuroDE(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestFormatDatumDE(t *testing.T) {
	cases := []struct {
		readingDate string
		want        string
	}{
		{"2026-11-15", "15.11.2026"},
		{"2026-01-01", "01.01.2026"},
		{"not-a-date", "not-a-date"},
	}
	for _, c := range cases {
		if got := formatDatumDE(c.readingDate); got != c.want {
			t.Errorf("formatDatumDE(%q) = %q, want %q", c.readingDate, got, c.want)
		}
	}
}

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
