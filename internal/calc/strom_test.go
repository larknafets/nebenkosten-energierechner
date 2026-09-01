package calc_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// baseReadings returns a full 9-meter reading map, defaulting every meter
// to 0 except the overrides given.
func baseReadings(overrides map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(store.MeterKeys))
	for _, k := range store.MeterKeys {
		out[k] = 0
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func mustCreatePeriod(t *testing.T, db *sql.DB, date string, strompreis float64, readings map[string]float64) int64 {
	t.Helper()
	id, err := store.CreatePeriod(db, store.PeriodInput{
		ReadingDate:             date,
		Strompreis:              strompreis,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		Readings:                readings,
		Personen:                map[int64]int64{1: 2, 2: 1},
		QM:                      map[int64]float64{1: 116.23, 2: 86},
	})
	if err != nil {
		t.Fatalf("create period %s: %v", date, err)
	}
	return id
}

func TestStrom_SequentialAllocation(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))
	p2 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"strom_gesamt":      18420,
		"strom_wohnung2":    6120,
		"strom_waermepumpe": 9840,
	}))

	got, err := calc.Strom(db, p2)
	if err != nil {
		t.Fatalf("calc.Strom: %v", err)
	}

	if got.NetzbezugGesamtKWh != 18420 {
		t.Errorf("NetzbezugGesamtKWh = %v, want 18420", got.NetzbezugGesamtKWh)
	}
	if got.W2AnteilKWh != 6120 {
		t.Errorf("W2AnteilKWh = %v, want 6120", got.W2AnteilKWh)
	}
	if got.WPAnteilKWh != 9840 {
		t.Errorf("WPAnteilKWh = %v, want 9840 (Rest1=12300, gedeckelt auf Verbrauch)", got.WPAnteilKWh)
	}
	if got.KostenW2 != 1346.40 {
		t.Errorf("KostenW2 = %v, want 1346.40", got.KostenW2)
	}
	if got.KostenWPGesamtUnrounded != 2164.80 {
		t.Errorf("KostenWPGesamtUnrounded = %v, want 2164.80", got.KostenWPGesamtUnrounded)
	}
	if got.PVAnteilW2KWh != 0 || got.PVAnteilWPKWh != 0 {
		t.Errorf("ohne Deckelung sollte kein PV-Anteil anfallen, got W2=%v WP=%v", got.PVAnteilW2KWh, got.PVAnteilWPKWh)
	}
}

func TestStrom_WaermepumpeGedeckeltAufRest(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))
	// Netzbezug 1000, Wohnung2 verbraucht 900 -> nur 100 Rest für die
	// Wärmepumpe, obwohl sie selbst 5000 kWh verbraucht hätte.
	p2 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"strom_gesamt":      1000,
		"strom_wohnung2":    900,
		"strom_waermepumpe": 5000,
	}))

	got, err := calc.Strom(db, p2)
	if err != nil {
		t.Fatalf("calc.Strom: %v", err)
	}
	if got.WPAnteilKWh != 100 {
		t.Errorf("WPAnteilKWh = %v, want 100 (gedeckelt auf Rest-Netzbezug)", got.WPAnteilKWh)
	}
	if got.PVAnteilWPKWh != 4900 {
		t.Errorf("PVAnteilWPKWh = %v, want 4900 (5000 Verbrauch - 100 angerechnet, durch PV gedeckt)", got.PVAnteilWPKWh)
	}
}

func TestStrom_PVUeberschussBeideNull(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-10-01", 0.22, baseReadings(nil))
	p2 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"strom_gesamt":      0,
		"strom_wohnung2":    300,
		"strom_waermepumpe": 400,
	}))

	got, err := calc.Strom(db, p2)
	if err != nil {
		t.Fatalf("calc.Strom: %v", err)
	}
	if got.W2AnteilKWh != 0 || got.WPAnteilKWh != 0 {
		t.Errorf("bei Netzbezug=0 sollten beide Anteile 0 sein, got W2=%v WP=%v", got.W2AnteilKWh, got.WPAnteilKWh)
	}
	if got.KostenW2 != 0 || got.KostenWPGesamtUnrounded != 0 {
		t.Errorf("bei Netzbezug=0 sollten beide Kosten 0 sein, got KostenW2=%v KostenWP=%v", got.KostenW2, got.KostenWPGesamtUnrounded)
	}
	if got.PVAnteilW2KWh != 300 || got.PVAnteilWPKWh != 400 {
		t.Errorf("bei Netzbezug=0 sollte der volle Verbrauch dem PV-Anteil zugerechnet werden, got W2=%v WP=%v", got.PVAnteilW2KWh, got.PVAnteilWPKWh)
	}
}

func TestStrom_ErsterPeriodeOhneVorperiode(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-11-01", 0.22, baseReadings(map[string]float64{
		"strom_gesamt": 100,
	}))

	_, err := calc.Strom(db, p1)
	if err == nil {
		t.Fatal("expected error for first period without a previous one")
	}
}
