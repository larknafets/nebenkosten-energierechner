package calc_test

import (
	"testing"

	"github.com/larknafets/nebenkosten-energierechner/internal/calc"
	"github.com/larknafets/nebenkosten-energierechner/internal/store"
)

func TestWasser_PersonenanteilUndKosten(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))

	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:       "2026-11-01",
		Strompreis:        0.22,
		FrischwasserPreis: 1.46,
		AbwasserPreis:     4.87,
		Readings: baseReadings(map[string]float64{
			"wasser_gesamt":                 100,
			"wasser_wohnung2":               30,
			"wasser_warmwasseraufbereitung": 20,
		}),
		Personen: map[int64]int64{1: 1, 2: 1},
		QM:       map[int64]float64{1: 116.23, 2: 86},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}

	got, err := calc.Wasser(db, id)
	if err != nil {
		t.Fatalf("calc.Wasser: %v", err)
	}

	if got.PersonenW1 != 1 || got.PersonenW2 != 1 {
		t.Errorf("Personen = W1:%v W2:%v, want W1:1 W2:1", got.PersonenW1, got.PersonenW2)
	}
	if got.WWAnteilW1 != 10 || got.WWAnteilW2 != 10 {
		t.Errorf("WWAnteil = W1:%v W2:%v, want W1:10 W2:10 (20m³ hälftig auf 1:1 Personen)", got.WWAnteilW1, got.WWAnteilW2)
	}
	if got.FrischwasserW1 != 60 {
		t.Errorf("FrischwasserW1 = %v, want 60 (Gesamt 100 - Wohnung2 30 - WW 20 + WWAnteil 10)", got.FrischwasserW1)
	}
	if got.FrischwasserW2 != 40 {
		t.Errorf("FrischwasserW2 = %v, want 40 (Wohnung2 30 + WWAnteil 10)", got.FrischwasserW2)
	}
	if got.AbwasserW1 != got.FrischwasserW1 || got.AbwasserW2 != got.FrischwasserW2 {
		t.Errorf("Abwasser sollte gleich Frischwasser sein, got AbwasserW1=%v AbwasserW2=%v", got.AbwasserW1, got.AbwasserW2)
	}
	if got.KostenFrischwasserW1 != 87.60 {
		t.Errorf("KostenFrischwasserW1 = %v, want 87.60", got.KostenFrischwasserW1)
	}
	if got.KostenFrischwasserW2 != 58.40 {
		t.Errorf("KostenFrischwasserW2 = %v, want 58.40", got.KostenFrischwasserW2)
	}
	if got.KostenAbwasserW1 != 292.20 {
		t.Errorf("KostenAbwasserW1 = %v, want 292.20", got.KostenAbwasserW1)
	}
	if got.KostenAbwasserW2 != 194.80 {
		t.Errorf("KostenAbwasserW2 = %v, want 194.80", got.KostenAbwasserW2)
	}
}

func TestWasser_KeinePersonen_FaelltAufHaelftigeVerteilungZurueck(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))

	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:       "2026-11-01",
		Strompreis:        0.22,
		FrischwasserPreis: 1.46,
		AbwasserPreis:     4.87,
		Readings: baseReadings(map[string]float64{
			"wasser_gesamt":                 100,
			"wasser_wohnung2":               30,
			"wasser_warmwasseraufbereitung": 20,
		}),
		Personen: map[int64]int64{1: 0, 2: 0},
		QM:       map[int64]float64{1: 116.23, 2: 86},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}

	got, err := calc.Wasser(db, id)
	if err != nil {
		t.Fatalf("calc.Wasser: %v", err)
	}
	// Issue #26: bei 0 Personen (leere Felder, Leerstand) darf der
	// Warmwasseraufbereitung-Anteil nicht auf 0/0 fallen - das würde das
	// komplette WW-Volumen (und dessen Kosten) stillschweigend aus beiden
	// Abrechnungen verschwinden lassen. Fallback ist eine hälftige
	// Verteilung statt NaN oder Kostenverlust.
	if got.WWAnteilW1 != 10 || got.WWAnteilW2 != 10 {
		t.Errorf("bei 0 Personen sollte WWAnteil hälftig verteilt werden (10/10 von 20m³), got W1=%v W2=%v", got.WWAnteilW1, got.WWAnteilW2)
	}
}

func TestWasser_ErsterPeriodeOhneVorperiode(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"wasser_gesamt": 100,
	}))

	_, err := calc.Wasser(db, p1)
	if err == nil {
		t.Fatal("expected error for first period without a previous one")
	}
}
