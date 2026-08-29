package web

import (
	"testing"

	"github.com/larknafets/nebenkosten-energierechner/internal/calc"
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
		Strom:   &calc.StromErgebnis{KostenW2: 20},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 5, KostenAbwasserW2: 5},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 10},
	}
	alt := kosten{
		Strom:   &calc.StromErgebnis{KostenW2: 40},
		Wasser:  &calc.WasserErgebnis{KostenFrischwasserW2: 10, KostenAbwasserW2: 10},
		Heizung: &calc.HeizungErgebnis{KostenHeizungW2: 20},
	}
	periods := []periodKosten{
		{Label: "Nov 2026", K: neu},
		{Label: "Okt 2026", K: alt},
	}

	monate := verlaufMonate(2, periods)
	if len(monate) != 2 {
		t.Fatalf("want 2 Monate, got %d", len(monate))
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
		monate := verlaufMonate(2, []periodKosten{{Label: "Nov 2026", K: zero}})
		for _, seg := range monate[0].Segmente {
			if seg.ProzentNeuestesGesamt != 0 {
				t.Errorf("ProzentNeuestesGesamt = %v, want 0", seg.ProzentNeuestesGesamt)
			}
		}
	})
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
