package calc_test

import (
	"database/sql"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func mustCreatePeriodMitEinspeisung(t *testing.T, db *sql.DB, date string, einspeisungPreis float64, readings map[string]float64) int64 {
	t.Helper()
	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:             date,
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		EinspeisungPreis:        einspeisungPreis,
		Readings:                readings,
		Personen:                map[int64]int64{1: 2, 2: 1},
	})
	if err != nil {
		t.Fatalf("create period %s: %v", date, err)
	}
	return id
}

func TestEinspeisung_ErtragBerechnung(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriodMitEinspeisung(t, db, "2026-10-01", 0.08, baseReadings(map[string]float64{
		"strom_einspeisung": 1000,
	}))
	p2 := mustCreatePeriodMitEinspeisung(t, db, "2026-11-01", 0.08, baseReadings(map[string]float64{
		"strom_einspeisung": 1350,
	}))

	got, err := calc.Einspeisung(db, p2)
	if err != nil {
		t.Fatalf("calc.Einspeisung: %v", err)
	}
	if got.EinspeisungKWh != 350 {
		t.Errorf("EinspeisungKWh = %v, want 350", got.EinspeisungKWh)
	}
	if got.Ertrag != 28.00 {
		t.Errorf("Ertrag = %v, want 28.00 (350 * 0.08)", got.Ertrag)
	}
}

func TestEinspeisung_ErsterPeriodeOhneVorperiode(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriodMitEinspeisung(t, db, "2026-11-01", 0.08, baseReadings(map[string]float64{
		"strom_einspeisung": 100,
	}))

	_, err := calc.Einspeisung(db, p1)
	if err == nil {
		t.Fatal("expected error for first period without a previous one")
	}
}
