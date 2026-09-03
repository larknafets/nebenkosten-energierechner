package web

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// dashboardSegment is one colored slice of a Dashboard bar - shared shape
// for the Jahressummen-Karten' mini-bar, the Monatsverlauf's Verbrauch/
// Fixkosten/Kombiniert bars, and (via ProzentNeuestesGesamt/ProzentGesamt,
// reused for either "percent of the newest month" or "percent of this
// card's own total" depending on the call site) every percentage-scaled bar
// on the page.
type dashboardSegment struct {
	Farbe                 string
	Label                 string
	Kosten                float64
	Verbrauch             float64 // Menge (kWh/MWh/m³) - only set for Verbrauch-Kategorien, 0 for Fixkosten/Kombiniert
	Einheit               string
	ProzentNeuestesGesamt float64
}

// setSegmentPct fills every segment's ProzentNeuestesGesamt as its share of
// denom (percent, not compressed to fit - Ticket #19's established
// convention: an older/bigger total can run past 100%). No-op if denom<=0.
func setSegmentPct(segs []dashboardSegment, denom float64) {
	if denom <= 0 {
		return
	}
	for i := range segs {
		segs[i].ProzentNeuestesGesamt = segs[i].Kosten / denom * 100
	}
}

// fixkostenKosten is one Fixkosten-Eingabe's Monat and computed Ergebnis.
// alleFixkostenKosten already drops Eingaben whose Jahr has no
// Kostenpositionen (store.ErrNoKostenpositionenJahr) - every dashboard view
// simply doesn't show a month it can't compute, same as Verbrauch silently
// stopping at the oldest period without a Vorperiode.
type fixkostenKosten struct {
	Monat string
	Erg   *calc.FixkostenErgebnis
}

// alleFixkostenKosten returns every computable Fixkosten-Eingabe's Ergebnis,
// newest first (store.AllFixkostenEingaben's own order).
func alleFixkostenKosten(db *sql.DB) ([]fixkostenKosten, error) {
	eingaben, err := store.AllFixkostenEingaben(db)
	if err != nil {
		return nil, fmt.Errorf("fixkosten eingaben: %w", err)
	}

	out := make([]fixkostenKosten, 0, len(eingaben))
	for _, e := range eingaben {
		erg, err := calc.Fixkosten(db, e.ID)
		if err != nil {
			if errors.Is(err, store.ErrNoKostenpositionenJahr) {
				continue
			}
			return nil, fmt.Errorf("fixkosten %d: %w", e.ID, err)
		}
		out = append(out, fixkostenKosten{Monat: e.Monat, Erg: erg})
	}
	return out, nil
}

// fixkostenGruppen groups one Fixkosten-Ergebnis's 14 Positionen by Logik
// into the 4 fixed buckets the "Fixkosten"-Modus bar shows (always all 4,
// even at 0 - same "show every category" convention as kategorien()).
func fixkostenGruppen(apartmentID int64, erg *calc.FixkostenErgebnis) []dashboardSegment {
	sums := map[string]float64{}
	for _, p := range erg.Positionen {
		sums[p.Logik] += p.KostenFor(apartmentID)
	}
	logiken := []string{store.LogikWohneinheit, store.LogikFlurstueck, store.LogikQM, store.LogikPersonen}
	out := make([]dashboardSegment, len(logiken))
	for i, logik := range logiken {
		out[i] = dashboardSegment{Farbe: "logik-" + logik, Label: logikLabels[logik], Kosten: calc.Round2(sums[logik])}
	}
	return out
}

// anzeigeJahr is the Dashboard's auto-following Anzeigejahr (Issue #60
// Story 22) - the year of whichever is chronologically newest across both
// Ablesungen and Fixkosten-Eingaben, purely data-driven (not wall-clock
// time). Falls back to the real current year only when neither series has
// any data yet (fresh install).
func anzeigeJahr(allPeriods []store.PeriodSummary, fixkostenEingaben []store.FixkostenEingabeSummary) int {
	var jahr int
	if len(allPeriods) > 0 {
		if t, err := time.Parse("2006-01-02", allPeriods[0].ReadingDate); err == nil {
			jahr = t.Year()
		}
	}
	if len(fixkostenEingaben) > 0 {
		if t, err := time.Parse("2006-01-02", fixkostenEingaben[0].Monat); err == nil && t.Year() > jahr {
			jahr = t.Year()
		}
	}
	if jahr == 0 {
		jahr = time.Now().Year()
	}
	return jahr
}

// dashboardJahresCard is one apartment's Jahressummen-Karte (Issue #60
// Story 21) - Fixkosten first, then Strom/Heizung/Wasser, same field order
// the mini-bar Segmente use. Also feeds the KPI-Strip below the Wohnung-
// Umschalter (VerbrauchEUR/FixkostenEUR/GesamtEUR), so the numbers always
// match between the two.
type dashboardJahresCard struct {
	ApartmentID   int64
	ApartmentName string
	ApartmentQM   float64
	FixkostenEUR  float64
	StromEUR      float64
	HeizungEUR    float64
	WasserEUR     float64
	VerbrauchEUR  float64
	GesamtEUR     float64
	Segmente      []dashboardSegment
}

// buildJahresCard sums the given apartment's Verbrauch- and Fixkosten-Kosten
// over every period/Eingabe whose date falls in jahr.
func buildJahresCard(apartmentID int64, apartmentName string, apartmentQM float64, jahr int, periodenKosten []periodKosten, fixkostenListe []fixkostenKosten) dashboardJahresCard {
	var strom, heizung, wasser, fix float64
	for _, pk := range periodenKosten {
		t, err := time.Parse("2006-01-02", pk.ReadingDate)
		if err != nil || t.Year() != jahr {
			continue
		}
		for _, kat := range kategorien(apartmentID, pk.K) {
			switch kat.Label {
			case "Strom":
				strom += kat.Kosten
			case "Heizung/Warmwasser":
				heizung += kat.Kosten
			case "Wasser":
				wasser += kat.Kosten
			}
		}
	}
	for _, fk := range fixkostenListe {
		t, err := time.Parse("2006-01-02", fk.Monat)
		if err != nil || t.Year() != jahr {
			continue
		}
		fix += fk.Erg.KostenFor(apartmentID)
	}
	strom, heizung, wasser, fix = calc.Round2(strom), calc.Round2(heizung), calc.Round2(wasser), calc.Round2(fix)
	verbrauch := calc.Round2(strom + heizung + wasser)
	gesamt := calc.Round2(verbrauch + fix)

	segs := []dashboardSegment{{Farbe: "fix", Label: "Fixkosten", Kosten: fix}}
	if apartmentID == 2 {
		segs = append(segs, dashboardSegment{Farbe: "strom", Label: "Strom", Kosten: strom})
	}
	segs = append(segs,
		dashboardSegment{Farbe: "heizung", Label: "Heizung/Ww", Kosten: heizung},
		dashboardSegment{Farbe: "wasser", Label: "Wasser", Kosten: wasser},
	)
	setSegmentPct(segs, gesamt)

	return dashboardJahresCard{
		ApartmentID: apartmentID, ApartmentName: apartmentName, ApartmentQM: apartmentQM,
		FixkostenEUR: fix, StromEUR: strom, HeizungEUR: heizung, WasserEUR: wasser,
		VerbrauchEUR: verbrauch, GesamtEUR: gesamt, Segmente: segs,
	}
}

// dashboardMonat is one calendar month's combined Verbrauch+Fixkosten
// Monatsverlauf-Zeile - up to 3 independently pct-scaled bar variants (one
// per numeric Modus); the 4th Modus ("Verbrauchswerte") reuses
// VerbrauchSegmente's raw Verbrauch/Einheit as text instead of a bar.
type dashboardMonat struct {
	Label     string
	Jahr      int
	IsCurrent bool

	HasVerbrauch      bool
	VerbrauchSegmente []dashboardSegment
	VerbrauchGesamt   float64

	HasFixkosten      bool
	FixkostenSegmente []dashboardSegment
	FixkostenGesamt   float64

	HasKombiniert      bool
	KombiniertSegmente []dashboardSegment
	KombiniertGesamt   float64
}

// dashboardJahreszeile is the Monatsverlauf's per-Jahr summary row (Issue
// #60 Story 27) - unlike the old Ablesung-only Verlauf's December-triggered
// separator, every Jahr gets one, including the not-yet-complete newest one
// (IstLaufend), which only sums whatever months are recorded so far.
type dashboardJahreszeile struct {
	Jahr           int
	IstLaufend     bool
	VerbrauchSumme float64
	FixkostenSumme float64
	GesamtSumme    float64
}

// dashboardVerlaufEintrag is one row of a Monatsverlauf column: either a
// Monat or (if Jahreszeile is set) a year-summary row. Exactly one is set.
type dashboardVerlaufEintrag struct {
	Monat       *dashboardMonat
	Jahreszeile *dashboardJahreszeile
}

// dashboardVerlaufSpalte is one apartment's Monatsverlauf, newest first.
type dashboardVerlaufSpalte struct {
	ApartmentID   int64
	ApartmentName string
	Eintraege     []dashboardVerlaufEintrag
}

// buildDashboardVerlauf merges the given apartment's Verbrauch (from
// periodenKosten) and Fixkosten (from fixkostenListe) into one calendar-
// month Monatsverlauf. The 2 input series aren't forced 1:1 - a month with
// only an Ablesung, only a Fixkosten-Eingabe, or both, is equally valid;
// when a month has 2+ Perioden/Eingaben, the newest one wins (both inputs
// are already newest-first).
func buildDashboardVerlauf(apartmentID int64, apartmentName string, periodenKosten []periodKosten, fixkostenListe []fixkostenKosten) dashboardVerlaufSpalte {
	type bucket struct {
		label         string
		jahr          int
		verbrauchKats []kategorie
		fixErg        *calc.FixkostenErgebnis
	}
	buckets := map[int]*bucket{}
	var order []int

	ensure := func(dateStr string) *bucket {
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil
		}
		key := t.Year()*1000 + int(t.Month())
		b, ok := buckets[key]
		if !ok {
			b = &bucket{label: germanPeriodLabelShort(dateStr), jahr: t.Year()}
			buckets[key] = b
			order = append(order, key)
		}
		return b
	}

	for _, pk := range periodenKosten {
		if b := ensure(pk.ReadingDate); b != nil && b.verbrauchKats == nil {
			b.verbrauchKats = kategorien(apartmentID, pk.K)
		}
	}
	for _, fk := range fixkostenListe {
		if b := ensure(fk.Monat); b != nil && b.fixErg == nil {
			b.fixErg = fk.Erg
		}
	}

	sort.Sort(sort.Reverse(sort.IntSlice(order)))

	monate := make([]dashboardMonat, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		dm := dashboardMonat{Label: b.label, Jahr: b.jahr}

		if b.verbrauchKats != nil {
			dm.HasVerbrauch = true
			for _, kat := range b.verbrauchKats {
				dm.VerbrauchGesamt += kat.Kosten
				dm.VerbrauchSegmente = append(dm.VerbrauchSegmente, dashboardSegment{
					Farbe: kat.Farbe, Label: kat.Label, Kosten: kat.Kosten, Verbrauch: kat.Verbrauch, Einheit: kat.Einheit,
				})
			}
			dm.VerbrauchGesamt = calc.Round2(dm.VerbrauchGesamt)
		}

		if b.fixErg != nil {
			dm.HasFixkosten = true
			dm.FixkostenSegmente = fixkostenGruppen(apartmentID, b.fixErg)
			for _, seg := range dm.FixkostenSegmente {
				dm.FixkostenGesamt += seg.Kosten
			}
			dm.FixkostenGesamt = calc.Round2(dm.FixkostenGesamt)
		}

		if dm.HasVerbrauch || dm.HasFixkosten {
			dm.HasKombiniert = true
			if dm.HasVerbrauch {
				dm.KombiniertSegmente = append(dm.KombiniertSegmente, dashboardSegment{Farbe: "verbrauch-gesamt", Label: "Verbrauch", Kosten: dm.VerbrauchGesamt})
			}
			if dm.HasFixkosten {
				dm.KombiniertSegmente = append(dm.KombiniertSegmente, dashboardSegment{Farbe: "fix", Label: "Fixkosten", Kosten: dm.FixkostenGesamt})
			}
			dm.KombiniertGesamt = calc.Round2(dm.VerbrauchGesamt + dm.FixkostenGesamt)
		}

		monate = append(monate, dm)
	}
	if len(monate) > 0 {
		monate[0].IsCurrent = true
	}

	// Baseline je Modus = das neueste Monat, das für diesen Modus überhaupt
	// Daten hat - nicht zwingend monate[0]: die allerneueste Ablesung/
	// Fixkosten-Eingabe kann fehlen, ohne dass jeder ältere Monat dadurch auf
	// 0% einfriert.
	var neuestesVerbrauch, neuestesFixkosten, neuestesKombiniert float64
	for _, m := range monate {
		if neuestesVerbrauch == 0 && m.HasVerbrauch {
			neuestesVerbrauch = m.VerbrauchGesamt
		}
		if neuestesFixkosten == 0 && m.HasFixkosten {
			neuestesFixkosten = m.FixkostenGesamt
		}
		if neuestesKombiniert == 0 && m.HasKombiniert {
			neuestesKombiniert = m.KombiniertGesamt
		}
	}
	for i := range monate {
		setSegmentPct(monate[i].VerbrauchSegmente, neuestesVerbrauch)
		setSegmentPct(monate[i].FixkostenSegmente, neuestesFixkosten)
		setSegmentPct(monate[i].KombiniertSegmente, neuestesKombiniert)
	}

	return dashboardVerlaufSpalte{
		ApartmentID: apartmentID, ApartmentName: apartmentName,
		Eintraege: mitJahreszeilen(monate),
	}
}

// mitJahreszeilen inserts a dashboardJahreszeile right after every calendar
// Jahr's last (=oldest displayed) Monat-row - triggered by the Jahr
// changing while walking newest-to-oldest, plus once more after the final
// (oldest) Monat, so every Jahr gets exactly one summary row (Issue #60
// Story 27), unlike the old December-only separator.
func mitJahreszeilen(monate []dashboardMonat) []dashboardVerlaufEintrag {
	if len(monate) == 0 {
		return nil
	}
	neuestesJahr := monate[0].Jahr

	out := make([]dashboardVerlaufEintrag, 0, len(monate)+4)
	currentJahr := monate[0].Jahr
	var vSumme, fSumme float64
	flush := func() {
		out = append(out, dashboardVerlaufEintrag{Jahreszeile: &dashboardJahreszeile{
			Jahr: currentJahr, IstLaufend: currentJahr == neuestesJahr,
			VerbrauchSumme: calc.Round2(vSumme), FixkostenSumme: calc.Round2(fSumme), GesamtSumme: calc.Round2(vSumme + fSumme),
		}})
	}
	for i, m := range monate {
		if m.Jahr != currentJahr {
			flush()
			currentJahr = m.Jahr
			vSumme, fSumme = 0, 0
		}
		vSumme += m.VerbrauchGesamt
		fSumme += m.FixkostenGesamt
		mm := m
		out = append(out, dashboardVerlaufEintrag{Monat: &mm})
		if i == len(monate)-1 {
			flush()
		}
	}
	return out
}

// dashboardSimpleCard is Wallbox/PV-Anlage's Jahressummen-Karte (Ticket
// #67) - a single EUR figure, unlike dashboardJahresCard: whole-house, no
// Wohnungs-Aufteilung and no Fixkosten-Anteil (rein informativ, siehe
// StromErgebnis.WallboxAnteilKWh/calc.Einspeisung).
type dashboardSimpleCard struct {
	Name      string
	GesamtEUR float64
	IstErtrag bool // true for PV-Anlage: "+" prefix, success color (Vergütung statt Kosten)
}

// dashboardSimpleMonat is one calendar month's row in a Wallbox/PV-Anlage
// Monatsverlauf - a single bar segment, unlike dashboardMonat's 4 Modus-
// Varianten (there's no Fixkosten/Kombiniert distinction for these).
type dashboardSimpleMonat struct {
	Label                 string
	Jahr                  int
	IsCurrent             bool
	HasWert               bool
	KWh                   float64
	EUR                   float64
	ProzentNeuestesGesamt float64
}

type dashboardSimpleJahreszeile struct {
	Jahr       int
	IstLaufend bool
	Summe      float64
}

type dashboardSimpleEintrag struct {
	Monat       *dashboardSimpleMonat
	Jahreszeile *dashboardSimpleJahreszeile
}

// dashboardSimpleSpalte is Wallbox/PV-Anlage's Monatsverlauf, newest first -
// analog zu dashboardVerlaufSpalte, aber ohne Wohnungs-Aufteilung.
type dashboardSimpleSpalte struct {
	ID        string
	Name      string
	IstErtrag bool
	Eintraege []dashboardSimpleEintrag
}

// simpleWert extracts one period's kWh/EUR for a Wallbox/PV-Anlage series -
// ok=false skips the period entirely (no Ablesung/keine Vorperiode).
type simpleWert func(k kosten) (kwh, eur float64, ok bool)

// simpleSeries bundles a Wallbox/PV-Anlage entity's identity (ID/Name/
// IstErtrag) with its Wert-Extractor - the 2 call sites in handleDashboard
// otherwise repeated the same id/name/istErtrag/wert-func quadruple twice
// each, once per build* call.
type simpleSeries struct {
	ID        string
	Name      string
	IstErtrag bool
	Wert      simpleWert
}

// wallboxSeries reads the Wallbox-Anteil from StromErgebnis - a third,
// PV-Netzbezug-gedeckelte Zuteilungsstufe nach Wohnung 2 und Wärmepumpe
// (Ticket #67 Nachtrag), nicht der rohe Zwischenzähler-Verbrauch.
var wallboxSeries = simpleSeries{
	ID: "wallbox", Name: "Wallboxen", IstErtrag: false,
	Wert: func(k kosten) (kwh, eur float64, ok bool) {
		if k.Strom == nil {
			return 0, 0, false
		}
		return k.Strom.WallboxAnteilKWh, k.Strom.KostenWallbox, true
	},
}

var pvSeries = simpleSeries{
	ID: "pv", Name: "PV-Anlage", IstErtrag: true,
	Wert: func(k kosten) (kwh, eur float64, ok bool) {
		if k.Einspeisung == nil {
			return 0, 0, false
		}
		return k.Einspeisung.EinspeisungKWh, k.Einspeisung.Ertrag, true
	},
}

// buildSimpleJahresCard sums a Wallbox/PV-Anlage series over every period
// whose ReadingDate falls in jahr.
func buildSimpleJahresCard(series simpleSeries, jahr int, periodenKosten []periodKosten) dashboardSimpleCard {
	var sum float64
	for _, pk := range periodenKosten {
		t, err := time.Parse("2006-01-02", pk.ReadingDate)
		if err != nil || t.Year() != jahr {
			continue
		}
		if _, eur, ok := series.Wert(pk.K); ok {
			sum += eur
		}
	}
	return dashboardSimpleCard{Name: series.Name, GesamtEUR: calc.Round2(sum), IstErtrag: series.IstErtrag}
}

// buildSimpleVerlauf builds a Wallbox/PV-Anlage Monatsverlauf - one bar per
// Ablesungs-Monat plus a Jahressumme-Trennzeile per Kalenderjahr, analog zu
// buildDashboardVerlauf's Fixkosten-freier Fall.
func buildSimpleVerlauf(series simpleSeries, periodenKosten []periodKosten) dashboardSimpleSpalte {
	id, name, istErtrag, wert := series.ID, series.Name, series.IstErtrag, series.Wert
	monate := make([]dashboardSimpleMonat, 0, len(periodenKosten))
	for _, pk := range periodenKosten {
		t, err := time.Parse("2006-01-02", pk.ReadingDate)
		if err != nil {
			continue
		}
		dm := dashboardSimpleMonat{Label: germanPeriodLabelShort(pk.ReadingDate), Jahr: t.Year()}
		if kwh, eur, ok := wert(pk.K); ok {
			dm.HasWert = true
			dm.KWh = kwh
			dm.EUR = calc.Round2(eur)
		}
		monate = append(monate, dm)
	}
	if len(monate) > 0 {
		monate[0].IsCurrent = true
	}

	var neuestesEUR float64
	for _, m := range monate {
		if m.HasWert {
			neuestesEUR = m.EUR
			break
		}
	}
	if neuestesEUR > 0 {
		for i := range monate {
			if monate[i].HasWert {
				monate[i].ProzentNeuestesGesamt = monate[i].EUR / neuestesEUR * 100
			}
		}
	}

	var eintraege []dashboardSimpleEintrag
	if len(monate) > 0 {
		neuestesJahr := monate[0].Jahr
		currentJahr := monate[0].Jahr
		var summe float64
		flush := func() {
			eintraege = append(eintraege, dashboardSimpleEintrag{Jahreszeile: &dashboardSimpleJahreszeile{
				Jahr: currentJahr, IstLaufend: currentJahr == neuestesJahr, Summe: calc.Round2(summe),
			}})
		}
		for i, m := range monate {
			if m.Jahr != currentJahr {
				flush()
				currentJahr = m.Jahr
				summe = 0
			}
			summe += m.EUR
			mm := m
			eintraege = append(eintraege, dashboardSimpleEintrag{Monat: &mm})
			if i == len(monate)-1 {
				flush()
			}
		}
	}

	return dashboardSimpleSpalte{ID: id, Name: name, IstErtrag: istErtrag, Eintraege: eintraege}
}
