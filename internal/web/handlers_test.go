package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func TestKategorien(t *testing.T) {
	k := kosten{
		Strom: &calc.StromErgebnis{
			W2AnteilKWh:    305,
			W2VerbrauchKWh: 320,
			KostenW2:       67.20,
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
			WaermeW1MWh:      14.2,
			WaermeW2MWh:      9.6,
			WPAnteilW1KWh:    840,
			WPAnteilW2KWh:    360,
			WPVerbrauchW1KWh: 910,
			WPVerbrauchW2KWh: 390,
			KostenHeizungW1:  112.40,
			KostenHeizungW2:  84.10,
		},
	}

	t.Run("Heizung/Warmwasser zeigt tatsächlichen WP-Verbrauch-kWh (ohne PV-Abzug), dahinter Waermeverbrauch-MWh", func(t *testing.T) {
		for _, tc := range []struct {
			apartmentID      int64
			wantKWh, wantMWh float64
		}{
			{1, 910, 14.2},
			{2, 390, 9.6},
		} {
			kats := kategorien(tc.apartmentID, k)
			var heizung *kategorie
			for i := range kats {
				if kats[i].Label == "Heizung/Warmwasser" {
					heizung = &kats[i]
				}
			}
			if heizung == nil {
				t.Fatalf("Wohnung %d: keine Heizung/Warmwasser-Kategorie gefunden", tc.apartmentID)
			}
			if heizung.Verbrauch != tc.wantKWh || heizung.Einheit != "kWh" {
				t.Errorf("Wohnung %d: Verbrauch/Einheit = %v/%q, want %v/kWh", tc.apartmentID, heizung.Verbrauch, heizung.Einheit, tc.wantKWh)
			}
			if heizung.Verbrauch2 != tc.wantMWh || heizung.Einheit2 != "MWh" {
				t.Errorf("Wohnung %d: Verbrauch2/Einheit2 = %v/%q, want %v/MWh", tc.apartmentID, heizung.Verbrauch2, heizung.Einheit2, tc.wantMWh)
			}
		}
	})

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

	t.Run("Strom zeigt tatsächlichen Verbrauch (ohne PV-Abzug), nicht den abgerechneten Anteil", func(t *testing.T) {
		kats := kategorien(2, k)
		var strom *kategorie
		for i := range kats {
			if kats[i].Label == "Strom" {
				strom = &kats[i]
			}
		}
		if strom == nil {
			t.Fatal("keine Strom-Kategorie gefunden")
		}
		if strom.Verbrauch != 320 {
			t.Errorf("Strom.Verbrauch = %v, want 320 (W2VerbrauchKWh, nicht der abgerechnete Anteil 305)", strom.Verbrauch)
		}
		if strom.Kosten != 67.20 {
			t.Errorf("Strom.Kosten = %v, want 67.20 (bleibt auf dem abgerechneten Anteil basiert)", strom.Kosten)
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

	t.Run("Kind identifiziert jede Kategorie unabhängig vom Label (Architecture Review)", func(t *testing.T) {
		want := map[string]kategorieKind{"Strom": kategorieKindStrom, "Heizung/Warmwasser": kategorieKindHeizung, "Wasser": kategorieKindWasser}
		for _, kat := range kategorien(2, k) {
			if kat.Kind != want[kat.Label] {
				t.Errorf("Kategorie %q: Kind = %v, want %v", kat.Label, kat.Kind, want[kat.Label])
			}
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

// TestGroupKostenByMonat verifies Issue #86: periodenKosten sharing a Monat
// (untermonatige Ablesungen) get their kategorien summed per Label, not
// overwritten by whichever period is processed last.
func TestGroupKostenByMonat(t *testing.T) {
	first := kosten{Strom: &calc.StromErgebnis{KostenW2: 10, W2VerbrauchKWh: 50}, Wasser: &calc.WasserErgebnis{}, Heizung: &calc.HeizungErgebnis{}}
	second := kosten{Strom: &calc.StromErgebnis{KostenW2: 15, W2VerbrauchKWh: 60}, Wasser: &calc.WasserErgebnis{}, Heizung: &calc.HeizungErgebnis{}}
	october := kosten{Strom: &calc.StromErgebnis{KostenW2: 40, W2VerbrauchKWh: 200}, Wasser: &calc.WasserErgebnis{}, Heizung: &calc.HeizungErgebnis{}}

	periods := []periodKosten{
		{ReadingDate: "2026-09-01", Monat: "2026-09-01", K: first},
		{ReadingDate: "2026-09-15", Monat: "2026-09-01", K: second},
		{ReadingDate: "2026-10-01", Monat: "2026-10-01", K: october},
	}

	byMonat := groupKostenByMonat(2, periods)

	if len(byMonat) != 2 {
		t.Fatalf("want 2 Monate, got %d: %+v", len(byMonat), byMonat)
	}

	findStrom := func(kats []kategorie) *kategorie {
		for i := range kats {
			if kats[i].Label == "Strom" {
				return &kats[i]
			}
		}
		return nil
	}

	sept := findStrom(byMonat["2026-09-01"])
	if sept == nil {
		t.Fatalf("September: keine Strom-Kategorie gefunden: %+v", byMonat["2026-09-01"])
	}
	if sept.Kosten != 25 {
		t.Errorf("September Strom.Kosten = %v, want 25 (10+15, summiert statt ueberschrieben)", sept.Kosten)
	}
	if sept.Verbrauch != 110 {
		t.Errorf("September Strom.Verbrauch = %v, want 110 (50+60)", sept.Verbrauch)
	}

	okt := findStrom(byMonat["2026-10-01"])
	if okt == nil || okt.Kosten != 40 {
		t.Errorf("Oktober Strom = %+v, want Kosten=40 (eigener Monat, nicht mit September vermischt)", okt)
	}
}

func TestBuildDashboardVerlauf_Skalierung(t *testing.T) {
	// Neueste Periode (index 0) hat den kleineren Gesamtbetrag - die Skala
	// ist relativ zum groessten Monat im Zeitraum (Architecture Review:
	// vorher relativ zum neuesten Monat, was aeltere/teurere Monate ueber
	// 100% hinauslaufen liess und ihren Text am bar-track-Rand abschnitt).
	neu := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 20, W2AnteilKWh: 123, W2VerbrauchKWh: 123},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 5, KostenAbwasserW2: 5},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 10},
	}
	alt := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 40},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 10, KostenAbwasserW2: 10},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 20},
	}
	periods := []periodKosten{
		{ReadingDate: "2026-11-15", Monat: "2026-11-01", K: neu},
		{ReadingDate: "2026-10-01", Monat: "2026-10-01", K: alt},
	}

	spalte := buildDashboardVerlauf(2, "Wohnung 2", periods, nil)
	var monate []dashboardMonat
	for _, e := range spalte.Eintraege {
		if e.Monat != nil {
			monate = append(monate, *e.Monat)
		}
	}
	if len(monate) != 2 {
		t.Fatalf("want 2 Monate, got %d", len(monate))
	}
	if monate[0].Label != "Nov 2026" || monate[1].Label != "Okt 2026" {
		t.Errorf("Label = %q / %q, want \"Nov 2026\" / \"Okt 2026\"", monate[0].Label, monate[1].Label)
	}
	if !monate[0].IsCurrent || monate[1].IsCurrent {
		t.Errorf("nur der erste (neueste) Monat soll IsCurrent sein, got %+v / %+v", monate[0], monate[1])
	}
	if monate[0].VerbrauchGesamt != 40 {
		t.Errorf("neuester VerbrauchGesamt = %v, want 40", monate[0].VerbrauchGesamt)
	}
	if monate[1].VerbrauchGesamt != 80 {
		t.Errorf("aelterer VerbrauchGesamt = %v, want 80", monate[1].VerbrauchGesamt)
	}

	// Segmente tragen Label/Verbrauch/Einheit fuer die Verbrauchswerte-Ansicht
	// (Ticket #39) - Strom ist bei Wohnung 2 immer das erste Segment.
	strom := monate[0].VerbrauchSegmente[0]
	if strom.Label != "Strom" || strom.Verbrauch != 123 || strom.Einheit != "kWh" {
		t.Errorf("Strom-Segment = %+v, want Label=Strom Verbrauch=123 Einheit=kWh", strom)
	}

	// Skala = groesster VerbrauchGesamt im Zeitraum (80, der aeltere Monat).
	// Der aeltere Monat kommt also auf 100%, der neuere (40, halb so teuer)
	// auf 50% - kein Balken laeuft ueber den Rand hinaus.
	var altSum, neuSum float64
	for _, seg := range monate[1].VerbrauchSegmente {
		altSum += seg.ProzentNeuestesGesamt
	}
	if altSum < 99 || altSum > 101 {
		t.Errorf("Summe der Prozentanteile des aelteren (groessten) Monats = %v, want ~100", altSum)
	}
	for _, seg := range monate[0].VerbrauchSegmente {
		neuSum += seg.ProzentNeuestesGesamt
	}
	if neuSum < 49 || neuSum > 51 {
		t.Errorf("Summe der Prozentanteile des neueren Monats = %v, want ~50 (halb so teuer wie der groesste)", neuSum)
	}

	t.Run("leere Eintraege", func(t *testing.T) {
		spalte := buildDashboardVerlauf(2, "Wohnung 2", nil, nil)
		if spalte.Eintraege != nil {
			t.Errorf("want nil Eintraege for empty input, got %+v", spalte.Eintraege)
		}
	})

	t.Run("neuester VerbrauchGesamt 0 erzeugt kein NaN", func(t *testing.T) {
		zero := kosten{Strom: &calc.StromErgebnis{}, Wasser: &calc.WasserErgebnis{}, Heizung: &calc.HeizungErgebnis{}}
		spalte := buildDashboardVerlauf(2, "Wohnung 2", []periodKosten{{ReadingDate: "2026-11-15", Monat: "2026-11-01", K: zero}}, nil)
		for _, seg := range spalte.Eintraege[0].Monat.VerbrauchSegmente {
			if seg.ProzentNeuestesGesamt != 0 {
				t.Errorf("ProzentNeuestesGesamt = %v, want 0", seg.ProzentNeuestesGesamt)
			}
		}
	})
}

func TestBuildSimpleVerlaufUndJahresCard(t *testing.T) {
	// Wallboxen/PV-Anlage (Ticket #67) - whole-house, rein informativ, kein
	// Fixkosten-Anteil, keine Wohnungs-Zuteilung. Wallbox-Anteil kommt aus
	// StromErgebnis (dritte, PV-Netzbezug-gedeckelte Zuteilungsstufe nach
	// Wohnung 2 und Wärmepumpe, Ticket #67 Nachtrag).
	neu := kosten{Strom: &calc.StromErgebnis{WallboxAnteilKWh: 30, PVAnteilWallboxKWh: 10, KostenWallbox: 12}}
	alt := kosten{Strom: &calc.StromErgebnis{WallboxAnteilKWh: 15, PVAnteilWallboxKWh: 5, KostenWallbox: 6}}
	ohneWallbox := kosten{} // Strom nil - erste Periode ohne Vorperiode

	periods := []periodKosten{
		{ReadingDate: "2026-11-15", Monat: "2026-11-01", K: neu},
		{ReadingDate: "2026-10-01", Monat: "2026-10-01", K: alt},
		{ReadingDate: "2026-09-01", Monat: "2026-09-01", K: ohneWallbox},
	}

	spalte := buildSimpleVerlauf(wallboxSeries, periods)
	if spalte.ID != "wallbox" || spalte.Name != "Wallboxen" || spalte.IstErtrag {
		t.Errorf("Spalte-Metadaten falsch: %+v", spalte)
	}

	var monate []dashboardSimpleMonat
	var jahreszeilen []dashboardSimpleJahreszeile
	for _, e := range spalte.Eintraege {
		if e.Monat != nil {
			monate = append(monate, *e.Monat)
		}
		if e.Jahreszeile != nil {
			jahreszeilen = append(jahreszeilen, *e.Jahreszeile)
		}
	}
	if len(monate) != 3 {
		t.Fatalf("want 3 Monate, got %d: %+v", len(monate), monate)
	}
	if !monate[0].IsCurrent || monate[1].IsCurrent || monate[2].IsCurrent {
		t.Errorf("nur der erste (neueste) Monat soll IsCurrent sein, got %+v", monate)
	}
	if !monate[0].HasWert || monate[0].EUR != 12 {
		t.Errorf("neuester Monat = %+v, want HasWert EUR=12", monate[0])
	}
	if len(monate[0].Segmente) != 2 {
		t.Fatalf("want 2 Segmente (Stromkosten, PV-Anteil), got %d: %+v", len(monate[0].Segmente), monate[0].Segmente)
	}
	if s := monate[0].Segmente[0]; s.Label != "Stromkosten" || s.Verbrauch != 30 || s.Einheit != "kWh" {
		t.Errorf("Segment 0 = %+v, want Label=Stromkosten Verbrauch=30 Einheit=kWh", s)
	}
	if s := monate[0].Segmente[1]; s.Label != "PV-Anteil" || s.Verbrauch != 10 || s.Einheit != "kWh" {
		t.Errorf("Segment 1 = %+v, want Label=PV-Anteil Verbrauch=10 Einheit=kWh", s)
	}
	if monate[2].HasWert {
		t.Errorf("Monat ohne Wallbox-Ergebnis soll HasWert=false sein, got %+v", monate[2])
	}
	// Skala = neuester tatsaechlicher Gesamt-kWh (30+10=40) - jedes Segment
	// des aelteren Monats (15/5 kWh) skaliert gegen diesen gemeinsamen Nenner
	// (nicht gegen seinen eigenen Segment-Typ), die 2 Segmentbreiten summieren
	// sich also auf die Haelfte der Balkenbreite (15+5=20, halb so viel wie
	// 40) - analog TestBuildDashboardVerlauf_Skalierung, aber kWh- statt
	// EUR-basiert, siehe buildSimpleVerlauf.
	if want := 37.5; monate[1].Segmente[0].ProzentNeuestesGesamt < want-0.01 || monate[1].Segmente[0].ProzentNeuestesGesamt > want+0.01 {
		t.Errorf("aelteres Stromkosten-Segment ProzentNeuestesGesamt = %v, want ~37.5 (15/40)", monate[1].Segmente[0].ProzentNeuestesGesamt)
	}
	if want := 12.5; monate[1].Segmente[1].ProzentNeuestesGesamt < want-0.01 || monate[1].Segmente[1].ProzentNeuestesGesamt > want+0.01 {
		t.Errorf("aelteres PV-Anteil-Segment ProzentNeuestesGesamt = %v, want ~12.5 (5/40)", monate[1].Segmente[1].ProzentNeuestesGesamt)
	}
	if len(jahreszeilen) != 1 || jahreszeilen[0].Summe != 18 {
		t.Errorf("Jahreszeile = %+v, want 1 Eintrag mit Summe=18 (12+6, der Monat ohne Wert zaehlt 0)", jahreszeilen)
	}

	card := buildSimpleJahresCard(wallboxSeries, 2026, periods)
	if card.GesamtEUR != 18 || card.IstErtrag {
		t.Errorf("card = %+v, want GesamtEUR=18 IstErtrag=false", card)
	}
	// Jahressumme je Segment: Stromkosten 30+15=45 kWh, PV-Anteil 10+5=15
	// kWh (der Monat ohne Wallbox-Ergebnis zaehlt nicht mit).
	if len(card.Segmente) != 2 {
		t.Fatalf("want 2 Segmente, got %d: %+v", len(card.Segmente), card.Segmente)
	}
	if s := card.Segmente[0]; s.Label != "Stromkosten" || s.Verbrauch != 45 || s.Kosten != 18 {
		t.Errorf("card.Segmente[0] = %+v, want Label=Stromkosten Verbrauch=45 Kosten=18", s)
	}
	if s := card.Segmente[1]; s.Label != "PV-Anteil" || s.Verbrauch != 15 || s.Kosten != 0 {
		t.Errorf("card.Segmente[1] = %+v, want Label=PV-Anteil Verbrauch=15 Kosten=0", s)
	}
	if want := 75.0; card.Segmente[0].ProzentNeuestesGesamt < want-0.01 || card.Segmente[0].ProzentNeuestesGesamt > want+0.01 {
		t.Errorf("Stromkosten-Segment ProzentNeuestesGesamt = %v, want ~75 (45/60)", card.Segmente[0].ProzentNeuestesGesamt)
	}

	t.Run("PV-Anlage nutzt Einspeisung statt Wallbox", func(t *testing.T) {
		p := []periodKosten{{ReadingDate: "2026-11-15", Monat: "2026-11-01", K: kosten{Einspeisung: &calc.EinspeisungErgebnis{EinspeisungKWh: 100, Ertrag: 8}}}}
		pvCard := buildSimpleJahresCard(pvSeries, 2026, p)
		if pvCard.GesamtEUR != 8 || !pvCard.IstErtrag {
			t.Errorf("pvCard = %+v, want GesamtEUR=8 IstErtrag=true", pvCard)
		}
	})

	t.Run("leere Eintraege", func(t *testing.T) {
		spalte := buildSimpleVerlauf(wallboxSeries, nil)
		if spalte.Eintraege != nil {
			t.Errorf("want nil Eintraege for empty input, got %+v", spalte.Eintraege)
		}
	})

	t.Run("mehrere Ablesungen im selben Monat werden zu einem Balken summiert", func(t *testing.T) {
		erste := kosten{Strom: &calc.StromErgebnis{WallboxAnteilKWh: 10, PVAnteilWallboxKWh: 2, KostenWallbox: 4}}
		zweite := kosten{Strom: &calc.StromErgebnis{WallboxAnteilKWh: 20, PVAnteilWallboxKWh: 3, KostenWallbox: 8}}
		periods := []periodKosten{
			{ReadingDate: "2026-09-15", Monat: "2026-09-01", K: zweite},
			{ReadingDate: "2026-09-01", Monat: "2026-09-01", K: erste},
		}
		spalte := buildSimpleVerlauf(wallboxSeries, periods)
		if len(spalte.Eintraege) != 2 { // 1 Monat + 1 Jahreszeile
			t.Fatalf("want 2 Eintraege (1 Monat zusammengefasst), got %d: %+v", len(spalte.Eintraege), spalte.Eintraege)
		}
		m := spalte.Eintraege[0].Monat
		if m == nil || m.EUR != 12 {
			t.Fatalf("Monat = %+v, want EUR=12 (4+8, aus beiden Ablesungen desselben Monats)", m)
		}
		if s := m.Segmente[0]; s.Label != "Stromkosten" || s.Verbrauch != 30 {
			t.Errorf("Stromkosten-Segment = %+v, want Verbrauch=30 (10+20)", s)
		}
	})
}

func TestBuildDashboardVerlauf_KombiniertModus(t *testing.T) {
	verbrauch := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 20},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 5, KostenAbwasserW2: 5},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 10},
	}
	fix := &calc.FixkostenErgebnis{
		Positionen: []calc.FixkostenPosition{{Key: "abfall_haushalt", Label: "Abfallwirtschaft Grundgebühr Haushalt", Logik: store.LogikWohneinheit, KostenW1: 15, KostenW2: 25}},
		KostenW1:   15, KostenW2: 25,
	}

	periods := []periodKosten{{ReadingDate: "2026-09-01", Monat: "2026-09-01", K: verbrauch}}
	fixkostenListe := []fixkostenKosten{{Monat: "2026-09-01", Erg: fix}}

	spalte := buildDashboardVerlauf(2, "Wohnung 2", periods, fixkostenListe)
	if len(spalte.Eintraege) != 2 { // 1 Monat + 1 Jahreszeile
		t.Fatalf("want 2 Eintraege, got %d: %+v", len(spalte.Eintraege), spalte.Eintraege)
	}
	m := spalte.Eintraege[0].Monat
	if m == nil {
		t.Fatal("Eintraege[0] should be a Monat")
	}
	if !m.HasVerbrauch || !m.HasFixkosten || !m.HasKombiniert {
		t.Fatalf("Has* = %v/%v/%v, want alle true", m.HasVerbrauch, m.HasFixkosten, m.HasKombiniert)
	}
	if m.VerbrauchGesamt != 40 {
		t.Errorf("VerbrauchGesamt = %v, want 40", m.VerbrauchGesamt)
	}
	if m.FixkostenGesamt != 25 {
		t.Errorf("FixkostenGesamt = %v, want 25 (Wohnung 2 -> KostenW2)", m.FixkostenGesamt)
	}
	if m.KombiniertGesamt != 65 {
		t.Errorf("KombiniertGesamt = %v, want 65 (40+25)", m.KombiniertGesamt)
	}
	if len(m.KombiniertSegmente) != 2 {
		t.Fatalf("want 2 Kombiniert-Segmente, got %d", len(m.KombiniertSegmente))
	}
}

func TestBuildDashboardVerlauf_NurFixkosten(t *testing.T) {
	fix := &calc.FixkostenErgebnis{
		Positionen: []calc.FixkostenPosition{{Key: "abfall_haushalt", Label: "Abfallwirtschaft Grundgebühr Haushalt", Logik: store.LogikWohneinheit, KostenW1: 15, KostenW2: 25}},
		KostenW1:   15, KostenW2: 25,
	}
	spalte := buildDashboardVerlauf(1, "Wohnung 1", nil, []fixkostenKosten{{Monat: "2026-09-01", Erg: fix}})
	m := spalte.Eintraege[0].Monat
	if m == nil {
		t.Fatal("Eintraege[0] should be a Monat")
	}
	if m.HasVerbrauch {
		t.Error("HasVerbrauch = true, want false (keine Ablesung fuer diesen Monat)")
	}
	if !m.HasFixkosten || m.FixkostenGesamt != 15 {
		t.Errorf("HasFixkosten/FixkostenGesamt = %v/%v, want true/15", m.HasFixkosten, m.FixkostenGesamt)
	}
	if !m.HasKombiniert || m.KombiniertGesamt != 15 {
		t.Errorf("HasKombiniert/KombiniertGesamt = %v/%v, want true/15 (nur Fixkosten vorhanden)", m.HasKombiniert, m.KombiniertGesamt)
	}
}

func TestMitJahreszeilen(t *testing.T) {
	leer := kosten{Strom: &calc.StromErgebnis{}, Wasser: &calc.WasserErgebnis{KostenFrischwasserW2: 10}, Heizung: &calc.HeizungErgebnis{}}

	t.Run("jedes Jahr bekommt eine Summenzeile, auch das laufende", func(t *testing.T) {
		periods := []periodKosten{
			{ReadingDate: "2026-01-15", Monat: "2026-01-01", K: leer}, // 10
			{ReadingDate: "2025-12-15", Monat: "2025-12-01", K: leer}, // 10
			{ReadingDate: "2025-11-15", Monat: "2025-11-01", K: leer}, // 10
		}
		spalte := buildDashboardVerlauf(2, "Wohnung 2", periods, nil)
		eintraege := spalte.Eintraege

		if len(eintraege) != 5 { // 3 Monate + 2 Jahreszeilen (2026 und 2025)
			t.Fatalf("want 5 Eintraege, got %d: %+v", len(eintraege), eintraege)
		}
		if eintraege[0].Monat == nil || eintraege[0].Monat.Label != "Jan 2026" {
			t.Errorf("Eintrag 0 soll Jan 2026 (Monat) sein, got %+v", eintraege[0])
		}
		if eintraege[1].Jahreszeile == nil {
			t.Fatalf("Eintrag 1 soll die Jahreszeile 2026 sein, got %+v", eintraege[1])
		}
		if eintraege[1].Jahreszeile.Jahr != 2026 || !eintraege[1].Jahreszeile.IstLaufend {
			t.Errorf("Jahreszeile 1 = %+v, want Jahr:2026 IstLaufend:true (neuestes Jahr)", eintraege[1].Jahreszeile)
		}
		if eintraege[1].Jahreszeile.VerbrauchSumme != 10 {
			t.Errorf("Jahreszeile 2026 VerbrauchSumme = %v, want 10 (nur Januar)", eintraege[1].Jahreszeile.VerbrauchSumme)
		}
		if eintraege[2].Monat == nil || eintraege[2].Monat.Label != "Dez 2025" {
			t.Errorf("Eintrag 2 soll Dez 2025 sein, got %+v", eintraege[2])
		}
		if eintraege[3].Monat == nil || eintraege[3].Monat.Label != "Nov 2025" {
			t.Errorf("Eintrag 3 soll Nov 2025 sein, got %+v", eintraege[3])
		}
		if eintraege[4].Jahreszeile == nil {
			t.Fatalf("Eintrag 4 soll die Jahreszeile 2025 sein, got %+v", eintraege[4])
		}
		if eintraege[4].Jahreszeile.Jahr != 2025 || eintraege[4].Jahreszeile.IstLaufend {
			t.Errorf("Jahreszeile 4 = %+v, want Jahr:2025 IstLaufend:false", eintraege[4].Jahreszeile)
		}
		// VerbrauchSumme 2025 = Dez (10) + Nov (10) = 20 - Januar 2026 gehoert
		// nicht zu Kalenderjahr 2025.
		if eintraege[4].Jahreszeile.VerbrauchSumme != 20 {
			t.Errorf("Jahreszeile 2025 VerbrauchSumme = %v, want 20", eintraege[4].Jahreszeile.VerbrauchSumme)
		}
	})

	t.Run("leere Eintraege", func(t *testing.T) {
		if got := mitJahreszeilen(nil); got != nil {
			t.Errorf("want nil, got %+v", got)
		}
	})
}

func TestBuildJahresCard(t *testing.T) {
	verbrauch2026 := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 20},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 5, KostenAbwasserW2: 5},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 10},
	}
	verbrauch2025 := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 999},
		Wasser:  &calc.WasserErgebnis{},
		Heizung: &calc.HeizungErgebnis{},
	}
	periods := []periodKosten{
		{ReadingDate: "2026-09-01", Monat: "2026-09-01", K: verbrauch2026, Personen: map[int64]int64{1: 3, 2: 2}},
		{ReadingDate: "2026-08-01", Monat: "2026-08-01", K: verbrauch2026, Personen: map[int64]int64{1: 3, 2: 1}},
		{ReadingDate: "2025-09-01", Monat: "2025-09-01", K: verbrauch2025, Personen: map[int64]int64{1: 3, 2: 99}}, // anderes Jahr, muss ausgeschlossen werden
	}
	fixkostenListe := []fixkostenKosten{
		{Monat: "2026-09-01", Erg: &calc.FixkostenErgebnis{KostenW1: 15, KostenW2: 25}},
		{Monat: "2025-09-01", Erg: &calc.FixkostenErgebnis{KostenW1: 999, KostenW2: 999}}, // anderes Jahr
	}

	card := buildJahresCard(2, "Wohnung 2", 86, 120.5, 2026, periods, fixkostenListe)
	if card.StromEUR != 40 || card.HeizungEUR != 20 || card.WasserEUR != 20 {
		t.Errorf("Strom/Heizung/Wasser = %v/%v/%v, want 40/20/20 (2 Perioden in 2026)", card.StromEUR, card.HeizungEUR, card.WasserEUR)
	}
	if card.FixkostenEUR != 25 {
		t.Errorf("FixkostenEUR = %v, want 25 (2025er Eingabe ausgeschlossen)", card.FixkostenEUR)
	}
	if card.VerbrauchEUR != 80 {
		t.Errorf("VerbrauchEUR = %v, want 80", card.VerbrauchEUR)
	}
	if card.GesamtEUR != 105 {
		t.Errorf("GesamtEUR = %v, want 105", card.GesamtEUR)
	}
	if card.ApartmentFlurstueck != 120.5 {
		t.Errorf("ApartmentFlurstueck = %v, want 120.5", card.ApartmentFlurstueck)
	}
	if card.PersonenSchnitt != 1.5 {
		t.Errorf("PersonenSchnitt = %v, want 1.5 (Schnitt aus 2 und 1, 2025er Periode ausgeschlossen)", card.PersonenSchnitt)
	}
}

func TestBuildJahresCard_PersonenSchnittOhneAblesung(t *testing.T) {
	card := buildJahresCard(1, "Wohnung 1", 116.23, 200, 2026, nil, nil)
	if card.PersonenSchnitt != 0 {
		t.Errorf("PersonenSchnitt = %v, want 0 (keine Ablesung im Jahr)", card.PersonenSchnitt)
	}
}

func TestAnzeigeJahr(t *testing.T) {
	t.Run("folgt dem neueren der beiden Serien", func(t *testing.T) {
		periods := []store.PeriodSummary{{ReadingDate: "2026-01-15"}}
		fixkosten := []store.FixkostenEingabeSummary{{Monat: "2027-03-01"}}
		if got := anzeigeJahr(periods, fixkosten); got != 2027 {
			t.Errorf("anzeigeJahr = %d, want 2027 (Fixkosten ist neuer)", got)
		}
	})

	t.Run("faellt ohne jegliche Daten auf das echte aktuelle Jahr zurueck", func(t *testing.T) {
		got := anzeigeJahr(nil, nil)
		if got != time.Now().Year() {
			t.Errorf("anzeigeJahr(nil, nil) = %d, want %d", got, time.Now().Year())
		}
	})
}

// TestMonatInput verifies Issue #86's code-review fix: monatInput bundles
// the "YYYY-MM" (<input type="month">) <-> "YYYY-MM-01" (store) conversion
// in one place, and monatInputFromStored guards against a too-short stored
// value instead of panicking (CreatePeriod never validates Monat, so a
// malformed value can reach the edit form).
func TestMonatInput(t *testing.T) {
	t.Run("toStored ergaenzt -01 an ein blankes YYYY-MM", func(t *testing.T) {
		if got := monatInput("2026-09").toStored(); got != "2026-09-01" {
			t.Errorf("toStored() = %q, want 2026-09-01", got)
		}
	})

	t.Run("toStored laesst ein bereits normalisiertes YYYY-MM-01 unveraendert", func(t *testing.T) {
		if got := monatInput("2026-09-01").toStored(); got != "2026-09-01" {
			t.Errorf("toStored() = %q, want 2026-09-01", got)
		}
	})

	t.Run("monatInputFromStored kuerzt YYYY-MM-01 auf YYYY-MM", func(t *testing.T) {
		if got := monatInputFromStored("2026-09-01"); got != "2026-09" {
			t.Errorf("monatInputFromStored(%q) = %q, want 2026-09", "2026-09-01", got)
		}
	})

	t.Run("monatInputFromStored gibt bei zu kurzem Wert leeren String zurueck, statt zu panicken", func(t *testing.T) {
		for _, stored := range []string{"", "202", "2026-0"} {
			if got := monatInputFromStored(stored); got != "" {
				t.Errorf("monatInputFromStored(%q) = %q, want \"\" (kein Panic)", stored, got)
			}
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

// TestParsePeriodInput_Monat verifies Issue #86: the wizard's <input
// type="month"> submits "YYYY-MM" (no day) - parsePeriodInput normalizes it
// to the store's "YYYY-MM-01" format.
func TestParsePeriodInput_Monat(t *testing.T) {
	apartments := []store.Apartment{{ID: 1, Name: "Wohnung 1"}, {ID: 2, Name: "Wohnung 2"}}

	form := url.Values{
		"reading_date":       {"2026-08-31"},
		"monat":              {"2026-09"},
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
		t.Fatalf("parsePeriodInput: %v", err)
	}
	if in.Monat != "2026-09-01" {
		t.Errorf("Monat = %q, want 2026-09-01", in.Monat)
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
		// Issue #87: formatDecimalDE gruppiert ab 1000 mit "." als
		// Tausendertrenner (siehe groupThousandsDE) - parseDecimalDE muss
		// das wieder entfernen, nicht als Dezimalpunkt fehlinterpretieren.
		{"1.000", 1000, false},
		{"2.345,43", 2345.43, false},
		{"-12.345", -12345, false},
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
// the exact values elsewhere don't matter for the tests using this. monat
// is derived from readingDate (YYYY-MM-01), matching the wizard's own
// auto-vorbelegung (Issue #86).
func csvRow(readingDate string, stromGesamt string) string {
	return strings.Join([]string{
		readingDate, readingDate[:7] + "-01", stromGesamt, "0", "0", "0", "0", "0", "0", "0", "0", "0",
		"0,22", "1,46", "4,87", "0,7", "0,08",
		"2", "1",
	}, ";")
}

// TestCSVHeader_HasMonatColumn verifies Issue #86: monat is exported/
// imported right after reading_date.
func TestCSVHeader_HasMonatColumn(t *testing.T) {
	if len(csvHeader) < 2 || csvHeader[0] != "reading_date" || csvHeader[1] != "monat" {
		t.Errorf("csvHeader[0:2] = %v, want [reading_date monat]", csvHeader[:min(2, len(csvHeader))])
	}
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
	if rows[1].input.Monat != "2026-07-01" {
		t.Errorf("Monat = %q, want 2026-07-01 (aus der monat-Spalte, roundtrip-fest)", rows[1].input.Monat)
	}
}

// TestParseImportCSV_RoundTrip_ThousandsSeparator verifies Issue #87: a
// Zählerstand ≥ 1000, which formatDecimalDE writes with a "." thousands
// separator on export (e.g. "1.000"), survives reimport unchanged.
func TestParseImportCSV_RoundTrip_ThousandsSeparator(t *testing.T) {
	csvText := strings.Join(csvHeader, ";") + "\n" +
		csvRow("2026-08-01", formatDecimalDE(1000)) + "\n"

	rows, err := parseImportCSV(strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("parseImportCSV: %v", err)
	}
	if got := rows[0].input.Readings["strom_gesamt"]; got != 1000 {
		t.Errorf("Readings[strom_gesamt] = %v, want 1000 (export/reimport verlustfrei)", got)
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

// TestPeriodOverviewGroups verifies Issue #86: the Ablesungen-Übersicht
// groups periods by Monat (Abrechnungsmonat), newest first - untermonatige
// Ablesungen sharing a Monat land in one group's Rows, ready for the
// template's rowspan-Spalte (Variante B).
func TestPeriodOverviewGroups(t *testing.T) {
	periods := []store.PeriodSummary{
		{ID: 4, ReadingDate: "2026-10-01", Monat: "2026-10-01"},
		{ID: 3, ReadingDate: "2026-09-15", Monat: "2026-09-01"},
		{ID: 2, ReadingDate: "2026-09-01", Monat: "2026-09-01"},
		{ID: 1, ReadingDate: "2026-08-01", Monat: "2026-08-01"},
	}

	groups := periodOverviewGroups(periods)
	if len(groups) != 3 {
		t.Fatalf("len(groups) = %d, want 3 (Okt, Sept, Aug)", len(groups))
	}

	okt := groups[0]
	if okt.MonatLabel != "Oktober 2026" || len(okt.Rows) != 1 {
		t.Errorf("groups[0] = %+v, want MonatLabel=Oktober 2026 mit 1 Row", okt)
	}
	if okt.Rows[0].ReadingDate != "01.10.2026" || okt.Rows[0].Zeitraum != "15.09.2026–01.10.2026" {
		t.Errorf("Okt-Row = %+v, want ReadingDate=01.10.2026 Zeitraum=15.09.2026–01.10.2026", okt.Rows[0])
	}

	sept := groups[1]
	if sept.MonatLabel != "September 2026" || len(sept.Rows) != 2 {
		t.Fatalf("groups[1] = %+v, want MonatLabel=September 2026 mit 2 Rows (untermonatig)", sept)
	}
	if sept.Rows[0].ReadingDate != "15.09.2026" || sept.Rows[1].ReadingDate != "01.09.2026" {
		t.Errorf("Sept-Rows Reihenfolge = [%q, %q], want [15.09.2026, 01.09.2026] (neueste zuerst)", sept.Rows[0].ReadingDate, sept.Rows[1].ReadingDate)
	}

	aug := groups[2]
	if aug.MonatLabel != "August 2026" || len(aug.Rows) != 1 || aug.Rows[0].Zeitraum != "keine Vorperiode" {
		t.Errorf("groups[2] = %+v, want MonatLabel=August 2026, 1 Row, Zeitraum=keine Vorperiode (aelteste)", aug)
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
		{1406.3333333333333, "1.406,33"},
		{-1234.5, "-1.234,5"},
		{1234567, "1.234.567"},
	}
	for _, c := range cases {
		if got := formatDecimalDE(c.x); got != c.want {
			t.Errorf("formatDecimalDE(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestGroupThousandsDE(t *testing.T) {
	cases := []struct{ s, want string }{
		{"0", "0"},
		{"200", "200"},
		{"1000", "1.000"},
		{"12345,43", "12.345,43"},
		{"-12345", "-12.345"},
		{"1234567,89", "1.234.567,89"},
	}
	for _, c := range cases {
		if got := groupThousandsDE(c.s); got != c.want {
			t.Errorf("groupThousandsDE(%q) = %q, want %q", c.s, got, c.want)
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

func TestFormatDecimalDE1(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{2, "2,0"},
		{1.5, "1,5"},
		{2.26, "2,3"},
		{0, "0,0"},
	}
	for _, c := range cases {
		if got := formatDecimalDE1(c.x); got != c.want {
			t.Errorf("formatDecimalDE1(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestFormatDecimalDE2(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{0.7, "0,70"},
		{107, "107,00"},
		{25.333333333333332, "25,33"},
		{0, "0,00"},
	}
	for _, c := range cases {
		if got := formatDecimalDE2(c.x); got != c.want {
			t.Errorf("formatDecimalDE2(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestFormatMeterDiff(t *testing.T) {
	cases := []struct {
		current, previous float64
		want              string
	}{
		{107, 84, "+23,00"},
		{107, 107, "0,00"},
		{84, 107, "-23,00"},
		{25.33, 20.13, "+5,20"},
	}
	for _, c := range cases {
		if got := formatMeterDiff(c.current, c.previous); got != c.want {
			t.Errorf("formatMeterDiff(%v, %v) = %q, want %q", c.current, c.previous, got, c.want)
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

// seedPeriodInputAt builds a valid PeriodInput at the given "YYYY-MM-DD"
// date, ReadingDate and Monat both set to it - everything else a fixed
// valid default (exact values don't matter for the ordering tests using
// this).
func seedPeriodInputAt(date string) store.PeriodInput {
	readings := make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		readings[key] = 0
	}
	return store.PeriodInput{
		ReadingDate:             date,
		Monat:                   date,
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		EinspeisungPreis:        0.08,
		Readings:                readings,
		Personen:                map[int64]int64{1: 2, 2: 1},
	}
}

// periodFormValues builds a valid Ablesung-Formular Wertesatz for the given
// reading_date/monat ("YYYY-MM"), everything else a fixed valid default -
// handleUpdateAblesung's seam tests only care about the Vorperiode/
// Folgeperiode-Konflikt branch.
func periodFormValues(readingDate, monat string) url.Values {
	v := url.Values{}
	v.Set("reading_date", readingDate)
	v.Set("monat", monat)
	for _, key := range store.MeterKeys {
		v.Set(key, "0")
	}
	v.Set("strompreis", "0.22")
	v.Set("frischwasser_preis", "1.46")
	v.Set("abwasser_preis", "4.87")
	v.Set("einspeisung_preis", "0.08")
	v.Set("heizung_gewichtung", "0.7")
	v.Set("personen_1", "2")
	v.Set("personen_2", "1")
	return v
}

// TestHandleUpdateAblesung_ErrorMapping covers the seam the architecture
// review flagged as untested: handleUpdateAblesung's errors.As-switch maps
// store.PeriodDateTooEarly/TooLateError and PeriodMonatTooEarly/TooLateError
// each to their own German message and StatusBadRequest, not a generic 500.
func TestHandleUpdateAblesung_ErrorMapping(t *testing.T) {
	db := openTestDB(t)

	if _, err := store.CreatePeriod(db, seedPeriodInputAt("2026-01-01")); err != nil {
		t.Fatalf("CreatePeriod older: %v", err)
	}
	target, err := store.CreatePeriod(db, seedPeriodInputAt("2026-06-01"))
	if err != nil {
		t.Fatalf("CreatePeriod target: %v", err)
	}
	if _, err := store.CreatePeriod(db, seedPeriodInputAt("2026-12-01")); err != nil {
		t.Fatalf("CreatePeriod newer: %v", err)
	}

	cases := []struct {
		name        string
		readingDate string
		monat       string
		wantSubstr  string
	}{
		{"Ablesedatum vor Vorperiode", "2025-12-01", "2026-06", "Ablesedatum muss nach der Vorperiode"},
		{"Ablesedatum nach Folgeperiode", "2027-01-01", "2026-06", "Ablesedatum muss vor der Folgeperiode"},
		{"Abrechnungsmonat vor Vorperiode", "2026-06-15", "2025-12", "Abrechnungsmonat darf nicht vor dem der Vorperiode"},
		{"Abrechnungsmonat nach Folgeperiode", "2026-06-15", "2027-01", "Abrechnungsmonat darf nicht nach dem der Folgeperiode"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := periodFormValues(c.readingDate, c.monat)
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/ablesungen/%d", target), strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", strconv.FormatInt(target, 10))

			w := httptest.NewRecorder()
			handleUpdateAblesung(db)(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), c.wantSubstr) {
				t.Errorf("body = %q, want substring %q", w.Body.String(), c.wantSubstr)
			}
		})
	}

	t.Run("Erfolgsfall redirected mit StatusFound", func(t *testing.T) {
		form := periodFormValues("2026-06-15", "2026-06")
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/ablesungen/%d", target), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", strconv.FormatInt(target, 10))

		w := httptest.NewRecorder()
		handleUpdateAblesung(db)(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusFound, w.Body.String())
		}
		if loc := w.Header().Get("Location"); !strings.Contains(loc, fmt.Sprintf("/ablesungen/%d", target)) {
			t.Errorf("Location = %q, want it to reference period %d", loc, target)
		}
	})
}
