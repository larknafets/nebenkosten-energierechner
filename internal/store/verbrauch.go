package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNoPreviousPeriod is returned by Verbrauch when the given period is the
// oldest one on record, so no consumption can be derived yet (Verbrauch =
// aktueller Stand minus Vormonat-Stand needs a Vormonat).
var ErrNoPreviousPeriod = errors.New("no previous period to compute consumption from")

// Period is one periods row without its readings/occupancy - the shared
// shape cost calculations key off (see LatestPeriod for the fuller
// read-view shape used by the UI).
type Period struct {
	ID                int64
	ReadingDate       string
	Strompreis        float64
	FrischwasserPreis float64
	AbwasserPreis     float64
}

// GetPeriodByID fetches a single period by id.
func GetPeriodByID(db *sql.DB, id int64) (*Period, error) {
	var p Period
	err := db.QueryRow(
		`SELECT id, reading_date, strompreis, frischwasser_preis, abwasser_preis
		 FROM periods WHERE id = ?`,
		id,
	).Scan(&p.ID, &p.ReadingDate, &p.Strompreis, &p.FrischwasserPreis, &p.AbwasserPreis)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("period %d not found", id)
		}
		return nil, fmt.Errorf("query period %d: %w", id, err)
	}
	return &p, nil
}

// Verbrauch returns, for every meter key, the consumption in this period:
// this period's Zählerstand minus the chronologically next-older period's
// Zählerstand for the same meter (Ticket #6). Returns ErrNoPreviousPeriod
// if periodID is the oldest period on record.
func Verbrauch(db *sql.DB, periodID int64) (map[string]float64, error) {
	period, err := GetPeriodByID(db, periodID)
	if err != nil {
		return nil, err
	}

	var previousID int64
	err = db.QueryRow(
		`SELECT id FROM periods
		 WHERE reading_date < ? OR (reading_date = ? AND id < ?)
		 ORDER BY reading_date DESC, id DESC LIMIT 1`,
		period.ReadingDate, period.ReadingDate, period.ID,
	).Scan(&previousID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoPreviousPeriod
		}
		return nil, fmt.Errorf("find previous period: %w", err)
	}

	current, err := readingsByMeterKey(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("current readings: %w", err)
	}
	previous, err := readingsByMeterKey(db, previousID)
	if err != nil {
		return nil, fmt.Errorf("previous readings: %w", err)
	}

	out := make(map[string]float64, len(MeterKeys))
	for _, key := range MeterKeys {
		out[key] = current[key] - previous[key]
	}
	return out, nil
}

func readingsByMeterKey(db *sql.DB, periodID int64) (map[string]float64, error) {
	rows, err := db.Query(
		`SELECT m.key, r.zaehlerstand
		 FROM meter_readings r JOIN meters m ON m.id = r.meter_id
		 WHERE r.period_id = ?`,
		periodID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}
