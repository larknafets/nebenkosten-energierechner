package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// MeterKeys is the stable, ordered list of the meter keys every period must
// have a reading for. See Issue #6 / seed() for the full definitions.
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
	"strom_einspeisung",
}

type Apartment struct {
	ID                int64
	Name              string
	QM                float64
	FlurstueckGroesse float64
}

// Apartments returns the 2 apartments ordered by id.
func Apartments(db *sql.DB) ([]Apartment, error) {
	rows, err := db.Query(`SELECT id, name, qm, flurstueck_groesse FROM apartments ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query apartments: %w", err)
	}
	defer rows.Close()

	var out []Apartment
	for rows.Next() {
		var a Apartment
		if err := rows.Scan(&a.ID, &a.Name, &a.QM, &a.FlurstueckGroesse); err != nil {
			return nil, fmt.Errorf("scan apartment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// StammdatenInput is one apartment's Wohnfläche/Flurstücksgröße, as
// edited on the /stammdaten page (Issue #61) - unlike Personen/prices, these
// aren't part of a monthly Ablesung anymore: current values only, no
// history, no per-period freezing.
type StammdatenInput struct {
	QM                float64
	FlurstueckGroesse float64
}

// UpdateStammdaten writes every given apartment's Wohnfläche/
// Flurstücksgröße in one transaction. Takes effect immediately for every
// month's calculation (apartments.qm/flurstueck_groesse are live columns,
// not historized - same behavior qm already had before Issue #61 moved its
// editing here).
func UpdateStammdaten(db *sql.DB, in map[int64]StammdatenInput) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for apartmentID, s := range in {
		if _, err := tx.Exec(
			`UPDATE apartments SET qm = ?, flurstueck_groesse = ? WHERE id = ?`,
			s.QM, s.FlurstueckGroesse, apartmentID,
		); err != nil {
			return fmt.Errorf("update stammdaten for apartment %d: %w", apartmentID, err)
		}
	}

	return tx.Commit()
}

// PeriodInput is one monthly reading, ready to be persisted.
type PeriodInput struct {
	ReadingDate             string // YYYY-MM-DD
	Strompreis              float64
	FrischwasserPreis       float64
	AbwasserPreis           float64
	HeizungWaermeGewichtung float64            // Heizungs-Split-Gewichtung (0.7/0.6/0.5), Ticket #27
	EinspeisungPreis        float64            // EUR/kWh für die PV-Einspeisevergütung (Ticket #47)
	Readings                map[string]float64 // meter key -> Zählerstand, must cover all of MeterKeys
	Personen                map[int64]int64    // apartment id -> Personenzahl
}

// insertPeriodTx inserts one period with its meter readings and occupancy.
// Wohnfläche/Flurstücksgröße live on apartments and are edited separately on
// /stammdaten (Issue #61), not per period. Shared by CreatePeriod (one
// period, own transaction) and ImportPeriods (many periods, one shared
// transaction - Ticket #54).
func insertPeriodTx(tx *sql.Tx, in PeriodInput) (periodID int64, err error) {
	res, err := tx.Exec(
		`INSERT INTO periods (reading_date, strompreis, frischwasser_preis, abwasser_preis, heizung_waerme_gewichtung, einspeisung_preis)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.ReadingDate, in.Strompreis, in.FrischwasserPreis, in.AbwasserPreis, in.HeizungWaermeGewichtung, in.EinspeisungPreis,
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

	return periodID, nil
}

// CreatePeriod inserts a new period in its own transaction.
func CreatePeriod(db *sql.DB, in PeriodInput) (periodID int64, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	periodID, err = insertPeriodTx(tx, in)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return periodID, nil
}

// ImportPeriods inserts every given period in one shared transaction - all
// or nothing, for the CSV bulk import (Ticket #54): a failure on any input
// rolls back every period from this call, not just the failing one.
func ImportPeriods(db *sql.DB, inputs []PeriodInput) (ids []int64, err error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	ids = make([]int64, 0, len(inputs))
	for _, in := range inputs {
		id, err := insertPeriodTx(tx, in)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return ids, nil
}

// PeriodDateTooEarlyError is returned by UpdatePeriod when in.ReadingDate
// would move periodID at or before its chronological predecessor's date.
type PeriodDateTooEarlyError struct {
	Neighbor string // the predecessor's ReadingDate
}

func (e *PeriodDateTooEarlyError) Error() string {
	return fmt.Sprintf("reading date must be after the previous period (%s)", e.Neighbor)
}

// PeriodDateTooLateError is UpdatePeriod's mirror of PeriodDateTooEarlyError
// for the chronological successor.
type PeriodDateTooLateError struct {
	Neighbor string // the successor's ReadingDate
}

func (e *PeriodDateTooLateError) Error() string {
	return fmt.Sprintf("reading date must be before the next period (%s)", e.Neighbor)
}

// dateNeighborBounds finds, among `all` periods excluding periodID, the
// closest ReadingDate below and above `currentDate` - the range a
// correction may move periodID's own date within without silently
// reordering it past a neighbor (Ticket #44 review finding: generalizing
// "korrigieren" to any period means a date change can now shift which
// period is whose Vorperiode for everyone in between, not just itself).
func dateNeighborBounds(all []PeriodSummary, periodID int64, currentDate string) (prev, next string, hasPrev, hasNext bool) {
	for _, p := range all {
		if p.ID == periodID {
			continue
		}
		if p.ReadingDate <= currentDate && (!hasPrev || p.ReadingDate > prev) {
			prev, hasPrev = p.ReadingDate, true
		}
		if p.ReadingDate >= currentDate && (!hasNext || p.ReadingDate < next) {
			next, hasNext = p.ReadingDate, true
		}
	}
	return
}

// checkDateNeighbors validates in.ReadingDate against periodID's
// chronological neighbors before UpdatePeriod writes it. The neighbor
// bounds are computed around periodID's *current* (pre-edit) date, not the
// new one - a correction may only move the date within the gap it already
// occupies, not jump elsewhere and skip the check.
func checkDateNeighbors(db *sql.DB, periodID int64, readingDate string) error {
	var currentDate string
	if err := db.QueryRow(`SELECT reading_date FROM periods WHERE id = ?`, periodID).Scan(&currentDate); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: period %d", ErrPeriodNotFound, periodID)
		}
		return fmt.Errorf("query period %d: %w", periodID, err)
	}

	all, err := AllPeriods(db)
	if err != nil {
		return fmt.Errorf("periods: %w", err)
	}

	prev, next, hasPrev, hasNext := dateNeighborBounds(all, periodID, currentDate)
	if hasPrev && readingDate <= prev {
		return &PeriodDateTooEarlyError{Neighbor: prev}
	}
	if hasNext && readingDate >= next {
		return &PeriodDateTooLateError{Neighbor: next}
	}
	return nil
}

// UpdatePeriod overwrites an existing period's fields, readings, and
// occupancy in place - no new row, no history of the previous values
// (Ticket #34: only the latest period is ever editable, in-place, no
// audit log). Costs aren't stored anywhere (berechneKosten/Verbrauch read
// live from the DB on every request), so overwriting here is all that's
// needed for the change to show up - nothing to invalidate. The neighbor-date
// invariant (see checkDateNeighbors) is enforced here, not just by the web
// wizard, so any caller gets the same protection.
func UpdatePeriod(db *sql.DB, periodID int64, in PeriodInput) error {
	if err := checkDateNeighbors(db, periodID, in.ReadingDate); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE periods SET reading_date = ?, strompreis = ?, frischwasser_preis = ?, abwasser_preis = ?, heizung_waerme_gewichtung = ?, einspeisung_preis = ?
		 WHERE id = ?`,
		in.ReadingDate, in.Strompreis, in.FrischwasserPreis, in.AbwasserPreis, in.HeizungWaermeGewichtung, in.EinspeisungPreis, periodID,
	)
	if err != nil {
		return fmt.Errorf("update period: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("update period rows affected: %w", err)
	} else if n == 0 {
		return fmt.Errorf("%w: period %d", ErrPeriodNotFound, periodID)
	}

	for _, key := range MeterKeys {
		value, ok := in.Readings[key]
		if !ok {
			return fmt.Errorf("missing reading for meter %q", key)
		}
		if _, err := tx.Exec(
			`INSERT INTO meter_readings (period_id, meter_id, zaehlerstand)
			 SELECT ?, id, ? FROM meters WHERE key = ?
			 ON CONFLICT(period_id, meter_id) DO UPDATE SET zaehlerstand = excluded.zaehlerstand`,
			periodID, value, key,
		); err != nil {
			return fmt.Errorf("update reading for %q: %w", key, err)
		}
	}

	for apartmentID, personen := range in.Personen {
		if _, err := tx.Exec(
			`INSERT INTO period_occupancy (period_id, apartment_id, personen) VALUES (?, ?, ?)
			 ON CONFLICT(period_id, apartment_id) DO UPDATE SET personen = excluded.personen`,
			periodID, apartmentID, personen,
		); err != nil {
			return fmt.Errorf("update occupancy for apartment %d: %w", apartmentID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// LatestPeriod is a period together with its readings and occupancy, as
// shown on the "letzte Ablesung" view.
type LatestPeriod struct {
	ID                      int64
	ReadingDate             string
	Strompreis              float64
	FrischwasserPreis       float64
	AbwasserPreis           float64
	HeizungWaermeGewichtung float64
	EinspeisungPreis        float64
	Readings                map[string]float64
	PersonenByApartment     map[int64]int64
}

// PeriodReadings is one period's per-meter Zählerstand, as used for the
// Ausreißer-Warnung baseline (Ticket #13).
type PeriodReadings struct {
	ID          int64
	ReadingDate string
	Readings    map[string]float64
}

// idDate is a period's id/reading_date pair, ordered a query returns them
// in - the input periodReadingsFor turns into full PeriodReadings.
type idDate struct {
	id   int64
	date string
}

// periodReadingsFor fetches each id's readings and zips them with the
// already-known dates - the shared tail of RecentPeriodReadings and
// PeriodReadingsBefore, which differ only in how they select the ids.
func periodReadingsFor(db *sql.DB, ids []idDate) ([]PeriodReadings, error) {
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

	return periodReadingsFor(db, ids)
}

// PeriodSummary identifies one period without its readings, for views that
// only need to iterate periods (e.g. the Dashboard Verlauf, Ticket #19).
type PeriodSummary struct {
	ID          int64
	ReadingDate string
}

// AllPeriods returns every period (newest first), without readings.
func AllPeriods(db *sql.DB) ([]PeriodSummary, error) {
	rows, err := db.Query(`SELECT id, reading_date FROM periods ORDER BY reading_date DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query periods: %w", err)
	}
	defer rows.Close()

	var out []PeriodSummary
	for rows.Next() {
		var p PeriodSummary
		if err := rows.Scan(&p.ID, &p.ReadingDate); err != nil {
			return nil, fmt.Errorf("scan period: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllPeriodDetails returns every period with its full readings and
// occupancy, oldest first - the CSV export's data source (Ticket #53). 3
// batched queries total (periods, readings, occupancy), not one per period.
func AllPeriodDetails(db *sql.DB) ([]*LatestPeriod, error) {
	rows, err := db.Query(
		`SELECT id, reading_date, strompreis, frischwasser_preis, abwasser_preis, heizung_waerme_gewichtung, einspeisung_preis
		 FROM periods ORDER BY reading_date ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query periods: %w", err)
	}
	defer rows.Close()

	byID := map[int64]*LatestPeriod{}
	var out []*LatestPeriod
	for rows.Next() {
		p := &LatestPeriod{
			Readings:            map[string]float64{},
			PersonenByApartment: map[int64]int64{},
		}
		if err := rows.Scan(&p.ID, &p.ReadingDate, &p.Strompreis, &p.FrischwasserPreis, &p.AbwasserPreis, &p.HeizungWaermeGewichtung, &p.EinspeisungPreis); err != nil {
			return nil, fmt.Errorf("scan period: %w", err)
		}
		byID[p.ID] = p
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	readingRows, err := db.Query(
		`SELECT r.period_id, m.key, r.zaehlerstand FROM meter_readings r JOIN meters m ON m.id = r.meter_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query readings: %w", err)
	}
	defer readingRows.Close()
	for readingRows.Next() {
		var periodID int64
		var key string
		var value float64
		if err := readingRows.Scan(&periodID, &key, &value); err != nil {
			return nil, fmt.Errorf("scan reading: %w", err)
		}
		if p, ok := byID[periodID]; ok {
			p.Readings[key] = value
		}
	}
	if err := readingRows.Err(); err != nil {
		return nil, err
	}

	occRows, err := db.Query(`SELECT period_id, apartment_id, personen FROM period_occupancy`)
	if err != nil {
		return nil, fmt.Errorf("query occupancy: %w", err)
	}
	defer occRows.Close()
	for occRows.Next() {
		var periodID, apartmentID, personen int64
		if err := occRows.Scan(&periodID, &apartmentID, &personen); err != nil {
			return nil, fmt.Errorf("scan occupancy: %w", err)
		}
		if p, ok := byID[periodID]; ok {
			p.PersonenByApartment[apartmentID] = personen
		}
	}
	if err := occRows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// GetLatestPeriod returns the most recently dated period, or nil if none
// exist yet.
func GetLatestPeriod(db *sql.DB) (*LatestPeriod, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM periods ORDER BY reading_date DESC, id DESC LIMIT 1`).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest period id: %w", err)
	}
	return GetPeriodDetails(db, id)
}

// GetPeriodDetails returns the given period together with its readings and
// occupancy, or nil if it doesn't exist - the per-id generalization of
// GetLatestPeriod (Ticket #43/#44: viewing/editing an Ablesung isn't limited
// to the latest period anymore).
func GetPeriodDetails(db *sql.DB, id int64) (*LatestPeriod, error) {
	row := db.QueryRow(
		`SELECT id, reading_date, strompreis, frischwasser_preis, abwasser_preis, heizung_waerme_gewichtung, einspeisung_preis
		 FROM periods WHERE id = ?`,
		id,
	)
	p := LatestPeriod{
		Readings:            map[string]float64{},
		PersonenByApartment: map[int64]int64{},
	}
	if err := row.Scan(&p.ID, &p.ReadingDate, &p.Strompreis, &p.FrischwasserPreis, &p.AbwasserPreis, &p.HeizungWaermeGewichtung, &p.EinspeisungPreis); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query period %d: %w", id, err)
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

// PeriodReadingsBefore returns up to `limit` periods chronologically before
// the given periodID (newest of those first), together with their per-meter
// Zählerstand - the Ausreißer-Warnung/Vorperiode-Vergleich baseline for
// editing an arbitrary (not necessarily latest) period (Ticket #44).
func PeriodReadingsBefore(db *sql.DB, periodID int64, limit int) ([]PeriodReadings, error) {
	target, err := GetPeriodByID(db, periodID)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT id, reading_date FROM periods
		 WHERE reading_date < ? OR (reading_date = ? AND id < ?)
		 ORDER BY reading_date DESC, id DESC LIMIT ?`,
		target.ReadingDate, target.ReadingDate, target.ID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query periods before %d: %w", periodID, err)
	}
	defer rows.Close()

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

	return periodReadingsFor(db, ids)
}

// ErrPeriodNotFound is returned by UpdatePeriod and DeletePeriod when the
// given periodID doesn't exist - callers use it to tell "nothing to do"
// apart from a genuine storage error (e.g. to answer with 404 instead of
// 500).
var ErrPeriodNotFound = errors.New("period not found")

// DeletePeriod removes a period together with its readings and occupancy in
// one transaction (Ticket #45). No soft-delete, no restriction on which
// period can be deleted - Verbrauch/Kosten are computed live from the DB
// (see internal/store/verbrauch.go), so a neighboring period's consumption
// simply recomputes against its new adjacent period on the next read.
func DeletePeriod(db *sql.DB, periodID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM meter_readings WHERE period_id = ?`, periodID); err != nil {
		return fmt.Errorf("delete readings: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM period_occupancy WHERE period_id = ?`, periodID); err != nil {
		return fmt.Errorf("delete occupancy: %w", err)
	}

	res, err := tx.Exec(`DELETE FROM periods WHERE id = ?`, periodID)
	if err != nil {
		return fmt.Errorf("delete period: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete period rows affected: %w", err)
	} else if n == 0 {
		return fmt.Errorf("%w: period %d", ErrPeriodNotFound, periodID)
	}

	return tx.Commit()
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
