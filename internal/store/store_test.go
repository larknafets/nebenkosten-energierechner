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

// TestDateNeighborBounds verifies the "korrigieren" date-reorder guard
// (Ticket #44 review finding): a period's own date is excluded from the
// bounds computation, and only the closest neighbors on either side count.
func TestDateNeighborBounds(t *testing.T) {
	all := []PeriodSummary{
		{ID: 1, ReadingDate: "2026-06-01"},
		{ID: 2, ReadingDate: "2026-07-01"},
		{ID: 3, ReadingDate: "2026-08-01"},
	}

	prev, next, hasPrev, hasNext := dateNeighborBounds(all, 2, "2026-07-01")
	if !hasPrev || prev != "2026-06-01" {
		t.Errorf("prev = %q, %v, want 2026-06-01, true", prev, hasPrev)
	}
	if !hasNext || next != "2026-08-01" {
		t.Errorf("next = %q, %v, want 2026-08-01, true", next, hasNext)
	}

	// Oldest period: no prev bound.
	_, _, hasPrev, _ = dateNeighborBounds(all, 1, "2026-06-01")
	if hasPrev {
		t.Error("oldest period: hasPrev = true, want false")
	}

	// Newest period: no next bound.
	_, _, _, hasNext = dateNeighborBounds(all, 3, "2026-08-01")
	if hasNext {
		t.Error("newest period: hasNext = true, want false")
	}
}

// TestUpdatePeriod_DateConflict proves UpdatePeriod itself rejects a date
// moved past a chronological neighbor - not just the web wizard that used to
// be the only caller checking this (architecture review: the invariant now
// lives behind UpdatePeriod's own interface, so every caller is protected).
func TestUpdatePeriod_DateConflict(t *testing.T) {
	db := openTestDB(t)
	mustCreatePeriod(t, db, "2026-06-01", baseReadings(nil))
	p2 := mustCreatePeriod(t, db, "2026-07-01", baseReadings(nil))
	mustCreatePeriod(t, db, "2026-08-01", baseReadings(nil))

	t.Run("date at or before the previous neighbor", func(t *testing.T) {
		err := UpdatePeriod(db, p2, PeriodInput{
			ReadingDate:             "2026-06-01",
			HeizungWaermeGewichtung: 0.7,
			Readings:                baseReadings(nil),
		})
		var tooEarly *PeriodDateTooEarlyError
		if !errors.As(err, &tooEarly) {
			t.Fatalf("UpdatePeriod err = %v, want *PeriodDateTooEarlyError", err)
		}
		if tooEarly.Neighbor != "2026-06-01" {
			t.Errorf("Neighbor = %q, want 2026-06-01", tooEarly.Neighbor)
		}
	})

	t.Run("date at or after the next neighbor", func(t *testing.T) {
		err := UpdatePeriod(db, p2, PeriodInput{
			ReadingDate:             "2026-08-01",
			HeizungWaermeGewichtung: 0.7,
			Readings:                baseReadings(nil),
		})
		var tooLate *PeriodDateTooLateError
		if !errors.As(err, &tooLate) {
			t.Fatalf("UpdatePeriod err = %v, want *PeriodDateTooLateError", err)
		}
		if tooLate.Neighbor != "2026-08-01" {
			t.Errorf("Neighbor = %q, want 2026-08-01", tooLate.Neighbor)
		}
	})

	t.Run("date within the gap stays allowed", func(t *testing.T) {
		if err := UpdatePeriod(db, p2, PeriodInput{
			ReadingDate:             "2026-07-15",
			HeizungWaermeGewichtung: 0.7,
			Readings:                baseReadings(nil),
		}); err != nil {
			t.Fatalf("UpdatePeriod: %v", err)
		}
	})
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

// TestGetPeriodDetails_ArbitraryPeriod verifies GetPeriodDetails (Ticket
// #43/#44's generalization of GetLatestPeriod) returns the right period's
// data by id, not just the latest one.
func TestGetPeriodDetails_ArbitraryPeriod(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-09-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	mustCreatePeriod(t, db, "2026-10-01", baseReadings(map[string]float64{"strom_gesamt": 200}))

	got, err := GetPeriodDetails(db, p1)
	if err != nil {
		t.Fatalf("GetPeriodDetails: %v", err)
	}
	if got == nil {
		t.Fatal("GetPeriodDetails: want the older period, got nil")
	}
	if got.ID != p1 || got.ReadingDate != "2026-09-01" {
		t.Errorf("GetPeriodDetails = {ID:%d, ReadingDate:%q}, want {%d, 2026-09-01}", got.ID, got.ReadingDate, p1)
	}
	if got.Readings["strom_gesamt"] != 100 {
		t.Errorf("GetPeriodDetails.Readings[strom_gesamt] = %v, want 100", got.Readings["strom_gesamt"])
	}
}

func TestGetPeriodDetails_UnknownID(t *testing.T) {
	db := openTestDB(t)
	got, err := GetPeriodDetails(db, 999)
	if err != nil {
		t.Fatalf("GetPeriodDetails: %v", err)
	}
	if got != nil {
		t.Errorf("GetPeriodDetails(999) = %+v, want nil", got)
	}
}

// TestPeriodReadingsBefore_ArbitraryTarget verifies the Ausreißer-Baseline
// lookup works against any target period, not just the latest one (Ticket
// #44: "korrigieren" is no longer limited to the latest Ablesung).
func TestPeriodReadingsBefore_ArbitraryTarget(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-06-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	p2 := mustCreatePeriod(t, db, "2026-07-01", baseReadings(map[string]float64{"strom_gesamt": 200}))
	mustCreatePeriod(t, db, "2026-08-01", baseReadings(map[string]float64{"strom_gesamt": 300}))

	before, err := PeriodReadingsBefore(db, p2, 4)
	if err != nil {
		t.Fatalf("PeriodReadingsBefore: %v", err)
	}
	if len(before) != 1 || before[0].ID != p1 {
		t.Fatalf("PeriodReadingsBefore(p2) = %+v, want just [p1]", before)
	}
}

func TestDeletePeriod_MiddlePeriod_Roundtrip(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-06-01", baseReadings(map[string]float64{"strom_gesamt": 100}))
	p2 := mustCreatePeriod(t, db, "2026-07-01", baseReadings(map[string]float64{"strom_gesamt": 250}))
	p3 := mustCreatePeriod(t, db, "2026-08-01", baseReadings(map[string]float64{"strom_gesamt": 500}))

	if err := DeletePeriod(db, p2); err != nil {
		t.Fatalf("DeletePeriod: %v", err)
	}

	all, err := AllPeriods(db)
	if err != nil {
		t.Fatalf("AllPeriods: %v", err)
	}
	if len(all) != 2 || all[0].ID != p3 || all[1].ID != p1 {
		t.Fatalf("AllPeriods after delete = %+v, want [p3, p1]", all)
	}

	if got, err := GetPeriodDetails(db, p2); err != nil || got != nil {
		t.Fatalf("GetPeriodDetails(deleted p2) = %+v, %v, want nil, nil", got, err)
	}

	// p3's Verbrauch must now diff against p1 (the new previous period),
	// recomputed live since nothing caches consumption.
	v, err := Verbrauch(db, p3)
	if err != nil {
		t.Fatalf("Verbrauch: %v", err)
	}
	if v["strom_gesamt"] != 400 {
		t.Errorf("Verbrauch[strom_gesamt] after deleting p2 = %v, want 400 (500-100)", v["strom_gesamt"])
	}
}

func TestDeletePeriod_UnknownID(t *testing.T) {
	db := openTestDB(t)
	if err := DeletePeriod(db, 999); err == nil {
		t.Fatal("DeletePeriod on unknown id: want error, got nil")
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

func TestEnsurePeriodsEinspeisungPreisColumn(t *testing.T) {
	t.Run("fuegt Spalte zu einer alten Tabelle ohne sie hinzu, mit Default 0", func(t *testing.T) {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { db.Close() })

		// Pre-#47-Schema: periods ohne einspeisung_preis.
		if _, err := db.Exec(`CREATE TABLE periods (
			id                        INTEGER PRIMARY KEY,
			reading_date              TEXT NOT NULL,
			strompreis                REAL NOT NULL,
			frischwasser_preis        REAL NOT NULL,
			abwasser_preis            REAL NOT NULL,
			heizung_waerme_gewichtung REAL NOT NULL DEFAULT 0.7
		)`); err != nil {
			t.Fatalf("create old-shape periods table: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO periods (reading_date, strompreis, frischwasser_preis, abwasser_preis) VALUES (?, ?, ?, ?)`,
			"2026-01-01", 0.22, 1.46, 4.87,
		); err != nil {
			t.Fatalf("insert pre-existing row: %v", err)
		}

		if err := ensurePeriodsEinspeisungPreisColumn(db); err != nil {
			t.Fatalf("ensurePeriodsEinspeisungPreisColumn: %v", err)
		}

		var preis float64
		if err := db.QueryRow(`SELECT einspeisung_preis FROM periods WHERE reading_date = '2026-01-01'`).Scan(&preis); err != nil {
			t.Fatalf("query migrated column: %v", err)
		}
		if preis != 0 {
			t.Errorf("pre-existing row's einspeisung_preis = %v, want 0 (backfilled default)", preis)
		}

		// Idempotent: ein zweiter Aufruf darf nicht mit "duplicate column" fehlschlagen.
		if err := ensurePeriodsEinspeisungPreisColumn(db); err != nil {
			t.Fatalf("second ensurePeriodsEinspeisungPreisColumn call: %v", err)
		}
	})

	t.Run("neue Tabelle hat die Spalte bereits - no-op", func(t *testing.T) {
		db := openTestDB(t)
		if err := ensurePeriodsEinspeisungPreisColumn(db); err != nil {
			t.Fatalf("ensurePeriodsEinspeisungPreisColumn on an already-current schema: %v", err)
		}
	})
}

// TestSeed_AddsNewMeterToExistingDB verifies Ticket #47: seed() runs on
// every Open() and is additive via ON CONFLICT DO NOTHING, so a meter added
// after a database was first created (like strom_einspeisung) still gets
// backfilled into an existing installation without a dedicated migration.
func TestSeed_AddsNewMeterToExistingDB(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(`DELETE FROM meters WHERE key = 'strom_einspeisung'`); err != nil {
		t.Fatalf("simulate pre-Ticket-#47 db: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM meters WHERE key = 'strom_einspeisung'`).Scan(&count); err != nil {
		t.Fatalf("count meters: %v", err)
	}
	if count != 0 {
		t.Fatalf("setup: strom_einspeisung still present after DELETE")
	}

	if err := seed(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.QueryRow(`SELECT count(*) FROM meters WHERE key = 'strom_einspeisung'`).Scan(&count); err != nil {
		t.Fatalf("count meters after seed: %v", err)
	}
	if count != 1 {
		t.Errorf("strom_einspeisung meters count after seed = %d, want 1 (backfilled)", count)
	}
}

func TestEinspeisungPreis_Roundtrip(t *testing.T) {
	db := openTestDB(t)
	p1 := mustCreatePeriod(t, db, "2026-09-01", baseReadings(nil))

	if err := UpdatePeriod(db, p1, PeriodInput{
		ReadingDate:             "2026-09-01",
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		EinspeisungPreis:        0.082,
		Readings:                baseReadings(nil),
		Personen:                map[int64]int64{1: 2, 2: 1},
		QM:                      map[int64]float64{1: 116.23, 2: 86},
	}); err != nil {
		t.Fatalf("UpdatePeriod: %v", err)
	}

	got, err := GetPeriodDetails(db, p1)
	if err != nil {
		t.Fatalf("GetPeriodDetails: %v", err)
	}
	if got.EinspeisungPreis != 0.082 {
		t.Errorf("EinspeisungPreis = %v, want 0.082", got.EinspeisungPreis)
	}

	byID, err := GetPeriodByID(db, p1)
	if err != nil {
		t.Fatalf("GetPeriodByID: %v", err)
	}
	if byID.EinspeisungPreis != 0.082 {
		t.Errorf("GetPeriodByID.EinspeisungPreis = %v, want 0.082", byID.EinspeisungPreis)
	}
}
