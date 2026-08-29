package store

import (
	"database/sql"
	"fmt"
)

// MeterKeys is the stable, ordered list of the 9 meter keys every period
// must have a reading for. See Issue #6 / seed() for the full definitions.
var MeterKeys = []string{
	"strom_gesamt",
	"strom_wohnung2",
	"strom_waermepumpe",
	"strom_wallbox",
	"wasser_gesamt",
	"wasser_wohnung2",
	"wasser_warmwasseraufbereitung",
	"waerme_wohnung1",
	"waerme_wohnung2",
}

type Apartment struct {
	ID   int64
	Name string
	QM   float64
}

// Apartments returns the 2 apartments ordered by id.
func Apartments(db *sql.DB) ([]Apartment, error) {
	rows, err := db.Query(`SELECT id, name, qm FROM apartments ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query apartments: %w", err)
	}
	defer rows.Close()

	var out []Apartment
	for rows.Next() {
		var a Apartment
		if err := rows.Scan(&a.ID, &a.Name, &a.QM); err != nil {
			return nil, fmt.Errorf("scan apartment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PeriodInput is one monthly reading, ready to be persisted.
type PeriodInput struct {
	ReadingDate       string // YYYY-MM-DD
	Strompreis        float64
	FrischwasserPreis float64
	AbwasserPreis     float64
	Readings          map[string]float64 // meter key -> Zählerstand, must cover all of MeterKeys
	Personen          map[int64]int64    // apartment id -> Personenzahl
	QM                map[int64]float64  // apartment id -> Wohnfläche (writes through to apartments.qm)
}

// CreatePeriod inserts a new period with its meter readings and occupancy in
// one transaction, and updates the apartments' Wohnfläche (qm changes so
// rarely that it's captured in the same monthly form rather than a separate
// settings area - see Issue #7 resolution).
func CreatePeriod(db *sql.DB, in PeriodInput) (periodID int64, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO periods (reading_date, strompreis, frischwasser_preis, abwasser_preis)
		 VALUES (?, ?, ?, ?)`,
		in.ReadingDate, in.Strompreis, in.FrischwasserPreis, in.AbwasserPreis,
	)
	if err != nil {
		return 0, fmt.Errorf("insert period: %w", err)
	}
	periodID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("period id: %w", err)
	}

	for _, key := range MeterKeys {
		value, ok := in.Readings[key]
		if !ok {
			return 0, fmt.Errorf("missing reading for meter %q", key)
		}
		if _, err := tx.Exec(
			`INSERT INTO meter_readings (period_id, meter_id, zaehlerstand)
			 SELECT ?, id, ? FROM meters WHERE key = ?`,
			periodID, value, key,
		); err != nil {
			return 0, fmt.Errorf("insert reading for %q: %w", key, err)
		}
	}

	for apartmentID, personen := range in.Personen {
		if _, err := tx.Exec(
			`INSERT INTO period_occupancy (period_id, apartment_id, personen) VALUES (?, ?, ?)`,
			periodID, apartmentID, personen,
		); err != nil {
			return 0, fmt.Errorf("insert occupancy for apartment %d: %w", apartmentID, err)
		}
	}

	for apartmentID, qm := range in.QM {
		if _, err := tx.Exec(`UPDATE apartments SET qm = ? WHERE id = ?`, qm, apartmentID); err != nil {
			return 0, fmt.Errorf("update qm for apartment %d: %w", apartmentID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return periodID, nil
}

// LatestPeriod is a period together with its readings and occupancy, as
// shown on the "letzte Ablesung" view.
type LatestPeriod struct {
	ID                  int64
	ReadingDate         string
	Strompreis          float64
	FrischwasserPreis   float64
	AbwasserPreis       float64
	Readings            map[string]float64
	PersonenByApartment map[int64]int64
}

// PeriodReadings is one period's per-meter Zählerstand, as used for the
// Ausreißer-Warnung baseline (Ticket #13).
type PeriodReadings struct {
	ID          int64
	ReadingDate string
	Readings    map[string]float64
}

// RecentPeriodReadings returns the most recent `limit` periods (newest
// first) together with their per-meter Zählerstand.
func RecentPeriodReadings(db *sql.DB, limit int) ([]PeriodReadings, error) {
	rows, err := db.Query(
		`SELECT id, reading_date FROM periods ORDER BY reading_date DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent periods: %w", err)
	}
	defer rows.Close()

	type idDate struct {
		id   int64
		date string
	}
	var ids []idDate
	for rows.Next() {
		var d idDate
		if err := rows.Scan(&d.id, &d.date); err != nil {
			return nil, fmt.Errorf("scan period: %w", err)
		}
		ids = append(ids, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]PeriodReadings, 0, len(ids))
	for _, d := range ids {
		readings, err := readingsByMeterKey(db, d.id)
		if err != nil {
			return nil, fmt.Errorf("readings for period %d: %w", d.id, err)
		}
		out = append(out, PeriodReadings{ID: d.id, ReadingDate: d.date, Readings: readings})
	}
	return out, nil
}

// GetLatestPeriod returns the most recently dated period, or nil if none
// exist yet.
func GetLatestPeriod(db *sql.DB) (*LatestPeriod, error) {
	row := db.QueryRow(
		`SELECT id, reading_date, strompreis, frischwasser_preis, abwasser_preis
		 FROM periods ORDER BY reading_date DESC, id DESC LIMIT 1`,
	)
	p := LatestPeriod{
		Readings:            map[string]float64{},
		PersonenByApartment: map[int64]int64{},
	}
	if err := row.Scan(&p.ID, &p.ReadingDate, &p.Strompreis, &p.FrischwasserPreis, &p.AbwasserPreis); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest period: %w", err)
	}

	rows, err := db.Query(
		`SELECT m.key, r.zaehlerstand
		 FROM meter_readings r JOIN meters m ON m.id = r.meter_id
		 WHERE r.period_id = ?`,
		p.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("query readings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan reading: %w", err)
		}
		p.Readings[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	p.PersonenByApartment, err = PersonenByApartment(db, p.ID)
	if err != nil {
		return nil, err
	}

	return &p, nil
}

// PersonenByApartment returns the given period's occupancy (apartment id ->
// Personenzahl), as recorded at that period's Ablesung.
func PersonenByApartment(db *sql.DB, periodID int64) (map[int64]int64, error) {
	rows, err := db.Query(
		`SELECT apartment_id, personen FROM period_occupancy WHERE period_id = ?`,
		periodID,
	)
	if err != nil {
		return nil, fmt.Errorf("query occupancy: %w", err)
	}
	defer rows.Close()

	out := map[int64]int64{}
	for rows.Next() {
		var apartmentID, personen int64
		if err := rows.Scan(&apartmentID, &personen); err != nil {
			return nil, fmt.Errorf("scan occupancy: %w", err)
		}
		out[apartmentID] = personen
	}
	return out, rows.Err()
}
