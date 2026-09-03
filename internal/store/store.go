// Package store owns the SQLite schema, migrations, and seed data for the
// Nebenkostenrechner. See https://github.com/larknafets/nebenkostenrechner/issues/6
// for the schema decision this package implements.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open creates the database file's parent directory if needed, opens the
// SQLite database at path, applies the schema, and seeds the fixed master
// data (apartments, meters) if it isn't present yet.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// CREATE TABLE IF NOT EXISTS above only creates periods on a brand-new
	// database - a pre-existing one (from before Issue #27) needs this
	// column added explicitly. SQLite has no "ADD COLUMN IF NOT EXISTS", so
	// check first.
	if err := ensurePeriodsHeizungGewichtungColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate heizung_waerme_gewichtung column: %w", err)
	}

	if err := ensurePeriodsEinspeisungPreisColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate einspeisung_preis column: %w", err)
	}

	if err := ensureApartmentsFlurstueckGroesseColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate flurstueck_groesse column: %w", err)
	}

	if err := seed(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed master data: %w", err)
	}

	return db, nil
}

// ensurePeriodsHeizungGewichtungColumn adds the heizung_waerme_gewichtung
// column to an existing periods table that predates it (Issue #27),
// defaulting existing rows to 0.7 (the previously hardcoded 70/30 split).
func ensurePeriodsHeizungGewichtungColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(periods)`)
	if err != nil {
		return fmt.Errorf("inspect periods columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan periods column: %w", err)
		}
		if name == "heizung_waerme_gewichtung" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(`ALTER TABLE periods ADD COLUMN heizung_waerme_gewichtung REAL NOT NULL DEFAULT 0.7`)
	return err
}

// ensurePeriodsEinspeisungPreisColumn adds the einspeisung_preis column to
// an existing periods table that predates it (Issue #47), defaulting
// existing rows to 0 - historical periods simply show no Einspeisevergütung
// until corrected.
func ensurePeriodsEinspeisungPreisColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(periods)`)
	if err != nil {
		return fmt.Errorf("inspect periods columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan periods column: %w", err)
		}
		if name == "einspeisung_preis" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(`ALTER TABLE periods ADD COLUMN einspeisung_preis REAL NOT NULL DEFAULT 0`)
	return err
}

// ensureApartmentsFlurstueckGroesseColumn adds the flurstueck_groesse column
// to an existing apartments table that predates it (Issue #61), defaulting
// existing rows to 0 - additive, the existing qm value is untouched.
func ensureApartmentsFlurstueckGroesseColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(apartments)`)
	if err != nil {
		return fmt.Errorf("inspect apartments columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan apartments column: %w", err)
		}
		if name == "flurstueck_groesse" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(`ALTER TABLE apartments ADD COLUMN flurstueck_groesse REAL NOT NULL DEFAULT 0`)
	return err
}

type apartmentSeed struct {
	id   int64
	name string
	qm   float64
}

type meterSeed struct {
	id          int64
	key         string
	meterType   string
	unit        string
	apartmentID *int64
	label       string
}

func apartmentID(id int64) *int64 { return &id }

// seed inserts the fixed master data for the 2 apartments and 9 meters if
// they're not already present. Keyed on `apartments.id` / `meters.key` so
// it's safe to run on every startup.
func seed(db *sql.DB) error {
	// qm (and flurstueck_groesse, via the column DEFAULT) start at 0 (Ticket
	// #38) - unlike the apartment id/name, Wohnungsgröße/Flurstücksgröße are
	// user data, not app-fixed structure, so they aren't hardcoded here.
	// Both are live columns, edited on /stammdaten (Issue #61), not seeded
	// with a real value.
	apartments := []apartmentSeed{
		{id: 1, name: "Wohnung 1", qm: 0},
		{id: 2, name: "Wohnung 2", qm: 0},
	}
	for _, a := range apartments {
		if _, err := db.Exec(
			`INSERT INTO apartments (id, name, qm) VALUES (?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			a.id, a.name, a.qm,
		); err != nil {
			return fmt.Errorf("seed apartment %q: %w", a.name, err)
		}
	}

	meters := []meterSeed{
		{id: 1, key: "strom_gesamt", meterType: "strom", unit: "kWh", apartmentID: nil, label: "Stromzähler Gesamt"},
		{id: 2, key: "strom_wohnung2", meterType: "strom", unit: "kWh", apartmentID: apartmentID(2), label: "Zwischenstromzähler Wohnung 2"},
		{id: 3, key: "strom_waermepumpe", meterType: "strom", unit: "kWh", apartmentID: nil, label: "Zwischenstromzähler Wärmepumpe"},
		{id: 4, key: "strom_wallbox", meterType: "strom", unit: "kWh", apartmentID: nil, label: "Zwischenzähler Wallboxen"},
		{id: 5, key: "wasser_gesamt", meterType: "wasser", unit: "m3", apartmentID: nil, label: "Wasserzähler Gesamt"},
		{id: 6, key: "wasser_wohnung2", meterType: "wasser", unit: "m3", apartmentID: apartmentID(2), label: "Zwischenwasserzähler Wohnung 2"},
		{id: 7, key: "wasser_warmwasseraufbereitung", meterType: "wasser", unit: "m3", apartmentID: nil, label: "Zwischenwasserzähler Warmwasseraufbereitung"},
		{id: 8, key: "waerme_wohnung1", meterType: "waerme", unit: "MWh", apartmentID: apartmentID(1), label: "Wärmemengenzähler Wohnung 1"},
		{id: 9, key: "waerme_wohnung2", meterType: "waerme", unit: "MWh", apartmentID: apartmentID(2), label: "Wärmemengenzähler Wohnung 2"},
		{id: 10, key: "strom_einspeisung", meterType: "strom", unit: "kWh", apartmentID: nil, label: "Einspeisezähler (PV)"},
	}
	for _, m := range meters {
		if _, err := db.Exec(
			`INSERT INTO meters (id, key, type, unit, apartment_id, label) VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			m.id, m.key, m.meterType, m.unit, m.apartmentID, m.label,
		); err != nil {
			return fmt.Errorf("seed meter %q: %w", m.key, err)
		}
	}

	return nil
}
