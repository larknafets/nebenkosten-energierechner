package calc_test

import (
	"math"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func TestHeizung_70_30_Verteilung(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))

	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:             "2026-11-01",
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		Readings: baseReadings(map[string]float64{
			"strom_gesamt":      18420,
			"strom_wohnung2":    6120,
			"strom_waermepumpe": 9840,
			"waerme_wohnung1":   6,
			"waerme_wohnung2":   4,
		}),
		Personen: map[int64]int64{1: 2, 2: 1},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}

	got, err := calc.Heizung(db, id)
	if err != nil {
		t.Fatalf("calc.Heizung: %v", err)
	}

	if got.TotalHeizungskostenUnrounded != 2164.80 {
		t.Errorf("TotalHeizungskostenUnrounded = %v, want 2164.80 (WPAnteil 9840 * Strompreis 0.22)", got.TotalHeizungskostenUnrounded)
	}
	if got.RatioWaermeW1 != 0.6 || got.RatioWaermeW2 != 0.4 {
		t.Errorf("RatioWaerme = W1:%v W2:%v, want W1:0.6 W2:0.4", got.RatioWaermeW1, got.RatioWaermeW2)
	}
	if got.KostenHeizungW1 != 1282.48 {
		t.Errorf("KostenHeizungW1 = %v, want 1282.48", got.KostenHeizungW1)
	}
	if got.KostenHeizungW2 != 882.32 {
		t.Errorf("KostenHeizungW2 = %v, want 882.32", got.KostenHeizungW2)
	}
	// Summe (vor Rundung) muss dem Total entsprechen (Acceptance Criteria).
	if sum := got.KostenHeizungW1 + got.KostenHeizungW2; sum != got.TotalHeizungskostenUnrounded {
		t.Errorf("Summe der gerundeten Kosten = %v, want %v (Rundung darf hier nicht abweichen)", sum, got.TotalHeizungskostenUnrounded)
	}
}

func TestHeizung_WPVerbrauch_TatsaechlicherWertOhnePVAbzug(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))

	// Netzbezug 6000 (nach W2-Zuteilung 0 Rest fuer WP) < WP-Unterzaehler
	// 10000 -> die vollen 10000 kWh sind tatsächlicher WP-Verbrauch, davon
	// wird aber nichts vom Netzbezug gedeckt (komplett durch PV gedeckt).
	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:             "2026-11-01",
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		Readings: baseReadings(map[string]float64{
			"strom_gesamt":      0,
			"strom_waermepumpe": 10000,
			"waerme_wohnung1":   6,
			"waerme_wohnung2":   4,
		}),
		Personen: map[int64]int64{1: 2, 2: 1},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}

	got, err := calc.Heizung(db, id)
	if err != nil {
		t.Fatalf("calc.Heizung: %v", err)
	}
	if got.WPAnteilW1KWh != 0 || got.WPAnteilW2KWh != 0 {
		t.Errorf("WPAnteil = W1:%v W2:%v, want 0/0 (kein Netzbezug, kompletter WP-Verbrauch durch PV gedeckt)", got.WPAnteilW1KWh, got.WPAnteilW2KWh)
	}
	if sum := got.WPVerbrauchW1KWh + got.WPVerbrauchW2KWh; math.Abs(sum-10000) > 0.001 {
		t.Errorf("WPVerbrauchW1KWh+WPVerbrauchW2KWh = %v, want 10000 (voller tatsächlicher WP-Verbrauch bleibt erhalten, auch wenn PV alles deckt)", sum)
	}
	if got.WPVerbrauchW1KWh <= 0 || got.WPVerbrauchW2KWh <= 0 {
		t.Errorf("WPVerbrauch = W1:%v W2:%v, want beide > 0 (anders als WPAnteil, das hier 0 ist)", got.WPVerbrauchW1KWh, got.WPVerbrauchW2KWh)
	}
}

func TestHeizung_KeinWaermeVerbrauch_FaelltAufHaelftigeVerteilungZurueck(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))

	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:             "2026-11-01",
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		Readings: baseReadings(map[string]float64{
			"strom_gesamt":      1000,
			"strom_waermepumpe": 500,
		}),
		Personen: map[int64]int64{1: 2, 2: 1},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}

	got, err := calc.Heizung(db, id)
	if err != nil {
		t.Fatalf("calc.Heizung: %v", err)
	}
	// Issue #26: bei 0 Wärmeverbrauch (z.B. Sommer, Wärmepumpe lief nur für
	// Warmwasser) darf der 70%-Wärmeanteil nicht auf 0/0 fallen - das würde
	// 70% der Wärmepumpen-Stromkosten stillschweigend aus beiden
	// Abrechnungen verschwinden lassen. Fallback ist eine hälftige
	// Verteilung statt NaN oder Kostenverlust.
	if got.RatioWaermeW1 != 0.5 || got.RatioWaermeW2 != 0.5 {
		t.Errorf("bei 0 Wärmeverbrauch sollte RatioWaerme 0.5/0.5 sein, got W1=%v W2=%v", got.RatioWaermeW1, got.RatioWaermeW2)
	}
	if sum := got.KostenHeizungW1 + got.KostenHeizungW2; math.Abs(sum-got.TotalHeizungskostenUnrounded) > 0.02 {
		t.Errorf("Summe der Heizungskosten = %v, want ~%v (voller WP-Kostenanteil muss verteilt werden, nicht verschwinden)", sum, got.TotalHeizungskostenUnrounded)
	}
}

func TestHeizung_ErsterPeriodeOhneVorperiode(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"strom_gesamt": 100,
	}))

	_, err := calc.Heizung(db, p1)
	if err == nil {
		t.Fatal("expected error for first period without a previous one")
	}
}
