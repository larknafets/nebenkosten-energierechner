// Package store owns the SQLite schema, migrations, and seed data for the
// Nebenkosten-Energierechner. See https://github.com/larknafets/nebenkosten-energierechner/issues/6
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

	if err := seed(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed master data: %w", err)
	}

	return db, nil
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
	apartments := []apartmentSeed{
		{id: 1, name: "Wohnung 1", qm: 116.23},
		{id: 2, name: "Wohnung 2", qm: 86},
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
