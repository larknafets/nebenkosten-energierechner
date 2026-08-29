package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func baseReadings(overrides map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(MeterKeys))
	for _, k := range MeterKeys {
		out[k] = 0
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func mustCreatePeriod(t *testing.T, db *sql.DB, date string, readings map[string]float64) int64 {
	t.Helper()
	id, err := CreatePeriod(db, PeriodInput{
		ReadingDate:             date,
		Strompreis:              0.22,
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

func TestCreatePeriod_GetLatestPeriod_AllPeriods_Roundtrip(t *testing.T) {
	db := openTestDB(t)

	p1 := mustCreatePeriod(t, db, "2026-09-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	p2 := mustCreatePeriod(t, db, "2026-10-01", baseReadings(map[string]float64{"strom_gesamt": 200}))

	latest, err := GetLatestPeriod(db)
	if err != nil {
		t.Fatalf("GetLatestPeriod: %v", err)
	}
	if latest == nil {
		t.Fatal("GetLatestPeriod: want a period, got nil")
	}
	if latest.ID != p2 {
		t.Errorf("GetLatestPeriod.ID = %d, want %d (the newer period)", latest.ID, p2)
	}
	if latest.ReadingDate != "2026-10-01" {
		t.Errorf("GetLatestPeriod.ReadingDate = %q, want 2026-10-01", latest.ReadingDate)
	}
	if latest.Strompreis != 0.22 || latest.FrischwasserPreis != 1.46 || latest.AbwasserPreis != 4.87 {
		t.Errorf("GetLatestPeriod prices = %v/%v/%v, want 0.22/1.46/4.87", latest.Strompreis, latest.FrischwasserPreis, latest.AbwasserPreis)
	}
	if latest.HeizungWaermeGewichtung != 0.7 {
		t.Errorf("GetLatestPeriod.HeizungWaermeGewichtung = %v, want 0.7", latest.HeizungWaermeGewichtung)
	}
	if latest.Readings["strom_gesamt"] != 200 {
		t.Errorf("GetLatestPeriod.Readings[strom_gesamt] = %v, want 200", latest.Readings["strom_gesamt"])
	}
	if latest.PersonenByApartment[1] != 2 || latest.PersonenByApartment[2] != 1 {
		t.Errorf("GetLatestPeriod.PersonenByApartment = %v, want {1:2, 2:1}", latest.PersonenByApartment)
	}

	all, err := AllPeriods(db)
	if err != nil {
		t.Fatalf("AllPeriods: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("AllPeriods: want 2 periods, got %d", len(all))
	}
	if all[0].ID != p2 || all[1].ID != p1 {
		t.Errorf("AllPeriods order = [%d, %d], want [%d, %d] (newest first)", all[0].ID, all[1].ID, p2, p1)
	}
}

// TestSeed_ApartmentsQMStartsAtZero verifies Ticket #38: a fresh install
// doesn't hardcode this household's real Wohnfläche as an app default -
// qm starts at 0, like Strompreis/Personen have no seed default either.
func TestSeed_ApartmentsQMStartsAtZero(t *testing.T) {
	db := openTestDB(t)
	apartments, err := Apartments(db)
	if err != nil {
		t.Fatalf("Apartments: %v", err)
	}
	if len(apartments) != 2 {
		t.Fatalf("want 2 apartments, got %d", len(apartments))
	}
	for _, a := range apartments {
		if a.QM != 0 {
			t.Errorf("apartment %q QM = %v, want 0 (unset until first Ablesung)", a.Name, a.QM)
		}
	}
}

func TestGetLatestPeriod_NoPeriods(t *testing.T) {
	db := openTestDB(t)
	latest, err := GetLatestPeriod(db)
	if err != nil {
		t.Fatalf("GetLatestPeriod: %v", err)
	}
	if latest != nil {
		t.Errorf("GetLatestPeriod on empty db = %+v, want nil", latest)
	}
}

func TestVerbrauch_Normal(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-09-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	p2 := mustCreatePeriod(t, db, "2026-10-01", baseReadings(map[string]float64{"strom_gesamt": 350}))

	v, err := Verbrauch(db, p2)
	if err != nil {
		t.Fatalf("Verbrauch: %v", err)
	}
	if v["strom_gesamt"] != 250 {
		t.Errorf("Verbrauch[strom_gesamt] = %v, want 250", v["strom_gesamt"])
	}
}

func TestVerbrauch_ErrNoPreviousPeriod(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-09-01", baseReadings(map[string]float64{"strom_gesamt": 100}))

	_, err := Verbrauch(db, p1)
	if !errors.Is(err, ErrNoPreviousPeriod) {
		t.Fatalf("Verbrauch on the oldest period: err = %v, want ErrNoPreviousPeriod", err)
	}
}

// TestVerbrauch_UeberLuecke verifies that a missing Ablesung (Ticket #9)
// doesn't break Verbrauch - it just diffs against whichever period is
// chronologically next-older, however far back that is.
func TestVerbrauch_UeberLuecke(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-06-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	// 4 Monate Luecke (Juli-Sept fehlen) statt der ueblichen 1.
	p2 := mustCreatePeriod(t, db, "2026-10-01", baseReadings(map[string]float64{"strom_gesamt": 500}))

	v, err := Verbrauch(db, p2)
	if err != nil {
		t.Fatalf("Verbrauch: %v", err)
	}
	if v["strom_gesamt"] != 400 {
		t.Errorf("Verbrauch ueber Luecke [strom_gesamt] = %v, want 400 (500-100, ueber den laengeren Zeitraum)", v["strom_gesamt"])
	}
}

func TestUpdatePeriod_Roundtrip(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-09-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	p2 := mustCreatePeriod(t, db, "2026-10-01", baseReadings(map[string]float64{"strom_gesamt": 200}))

	err := UpdatePeriod(db, p2, PeriodInput{
		ReadingDate:             "2026-10-02",
		Strompreis:              0.25,
		FrischwasserPreis:       1.50,
		AbwasserPreis:           5.00,
		HeizungWaermeGewichtung: 0.6,
		Readings:                baseReadings(map[string]float64{"strom_gesamt": 210}),
		Personen:                map[int64]int64{1: 3, 2: 1},
		QM:                      map[int64]float64{1: 116.23, 2: 86},
	})
	if err != nil {
		t.Fatalf("UpdatePeriod: %v", err)
	}

	latest, err := GetLatestPeriod(db)
	if err != nil {
		t.Fatalf("GetLatestPeriod: %v", err)
	}
	if latest.ID != p2 {
		t.Fatalf("GetLatestPeriod.ID = %d, want %d (UpdatePeriod must not create a new row)", latest.ID, p2)
	}
	if latest.ReadingDate != "2026-10-02" {
		t.Errorf("ReadingDate = %q, want 2026-10-02", latest.ReadingDate)
	}
	if latest.Strompreis != 0.25 || latest.FrischwasserPreis != 1.50 || latest.AbwasserPreis != 5.00 {
		t.Errorf("prices = %v/%v/%v, want 0.25/1.50/5.00", latest.Strompreis, latest.FrischwasserPreis, latest.AbwasserPreis)
	}
	if latest.HeizungWaermeGewichtung != 0.6 {
		t.Errorf("HeizungWaermeGewichtung = %v, want 0.6", latest.HeizungWaermeGewichtung)
	}
	if latest.Readings["strom_gesamt"] != 210 {
		t.Errorf("Readings[strom_gesamt] = %v, want 210", latest.Readings["strom_gesamt"])
	}
	if latest.PersonenByApartment[1] != 3 {
		t.Errorf("PersonenByApartment[1] = %v, want 3", latest.PersonenByApartment[1])
	}

	v, err := Verbrauch(db, p2)
	if err != nil {
		t.Fatalf("Verbrauch: %v", err)
	}
	if v["strom_gesamt"] != 110 {
		t.Errorf("Verbrauch[strom_gesamt] after update = %v, want 110 (210-100, recomputed live)", v["strom_gesamt"])
	}
}

func TestUpdatePeriod_UnknownID(t *testing.T) {
	db := openTestDB(t)
	err := UpdatePeriod(db, 999, PeriodInput{
		ReadingDate:             "2026-10-01",
		HeizungWaermeGewichtung: 0.7,
		Readings:                baseReadings(nil),
	})
	if err == nil {
		t.Fatal("UpdatePeriod on unknown id: want error, got nil")
	}
}

func TestEnsurePeriodsHeizungGewichtungColumn(t *testing.T) {
	t.Run("fuegt Spalte zu einer alten Tabelle ohne sie hinzu, mit Default 0.7", func(t *testing.T) {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { db.Close() })

		// Pre-#27-Schema: periods ohne heizung_waerme_gewichtung.
		if _, err := db.Exec(`CREATE TABLE periods (
			id                 INTEGER PRIMARY KEY,
			reading_date       TEXT NOT NULL,
			strompreis         REAL NOT NULL,
			frischwasser_preis REAL NOT NULL,
			abwasser_preis     REAL NOT NULL
		)`); err != nil {
			t.Fatalf("create old-shape periods table: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO periods (reading_date, strompreis, frischwasser_preis, abwasser_preis) VALUES (?, ?, ?, ?)`,
			"2026-01-01", 0.22, 1.46, 4.87,
		); err != nil {
			t.Fatalf("insert pre-existing row: %v", err)
		}

		if err := ensurePeriodsHeizungGewichtungColumn(db); err != nil {
			t.Fatalf("ensurePeriodsHeizungGewichtungColumn: %v", err)
		}

		var gewichtung float64
		if err := db.QueryRow(`SELECT heizung_waerme_gewichtung FROM periods WHERE reading_date = '2026-01-01'`).Scan(&gewichtung); err != nil {
			t.Fatalf("query migrated column: %v", err)
		}
		if gewichtung != 0.7 {
			t.Errorf("pre-existing row's heizung_waerme_gewichtung = %v, want 0.7 (backfilled default)", gewichtung)
		}

		// Idempotent: ein zweiter Aufruf darf nicht mit "duplicate column" fehlschlagen.
		if err := ensurePeriodsHeizungGewichtungColumn(db); err != nil {
			t.Fatalf("second ensurePeriodsHeizungGewichtungColumn call: %v", err)
		}
	})

	t.Run("neue Tabelle hat die Spalte bereits - no-op", func(t *testing.T) {
		db := openTestDB(t)
		if err := ensurePeriodsHeizungGewichtungColumn(db); err != nil {
			t.Fatalf("ensurePeriodsHeizungGewichtungColumn on an already-current schema: %v", err)
		}
	})
}
