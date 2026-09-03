package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Logik values for kostenpositionen_jahre.logik - the 4 allocation rules a
// Kostenposition can be split between Wohnung 1/2 by (Issue #60).
const (
	LogikWohneinheit = "wohneinheit"
	LogikFlurstueck  = "flurstueck"
	LogikQM          = "qm"
	LogikPersonen    = "personen"
)

// Typ values for kostenpositionen_jahre.typ.
const (
	TypJaehrlich = "jaehrlich"
	TypMonatlich = "monatlich"
)

// Kostenposition is one of the 14 fixed cost positions (Issue #60) - id/key/
// label only, app-fixed structure like meters. Logik/Typ/Jahreswert are
// per-year data, see KostenpositionJahr.
type Kostenposition struct {
	ID    int64
	Key   string
	Label string
}

// Kostenpositionen returns the 14 Kostenpositionen ordered by id.
func Kostenpositionen(db *sql.DB) ([]Kostenposition, error) {
	rows, err := db.Query(`SELECT id, key, label FROM kostenpositionen ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query kostenpositionen: %w", err)
	}
	defer rows.Close()

	var out []Kostenposition
	for rows.Next() {
		var kp Kostenposition
		if err := rows.Scan(&kp.ID, &kp.Key, &kp.Label); err != nil {
			return nil, fmt.Errorf("scan kostenposition: %w", err)
		}
		out = append(out, kp)
	}
	return out, rows.Err()
}

// KostenpositionJahr is one Kostenposition's data for one Jahr - änder-/
// löschbar, not historized/frozen (Issue #60).
type KostenpositionJahr struct {
	KostenpositionID int64
	Jahr             int
	Logik            string
	Typ              string
	Jahreswert       float64 // only meaningful when Typ == TypJaehrlich
}

// KostenpositionJahrInput is one Kostenposition's Jahr data, ready to be
// persisted via UpsertKostenpositionenJahr.
type KostenpositionJahrInput struct {
	Logik      string
	Typ        string
	Jahreswert float64
}

// KostenpositionenJahr returns the given Jahr's data, keyed by
// kostenposition_id. Missing from the map means that Kostenposition has no
// data for this Jahr yet (e.g. the Jahr was never angelegt).
func KostenpositionenJahr(db *sql.DB, jahr int) (map[int64]KostenpositionJahr, error) {
	rows, err := db.Query(
		`SELECT kostenposition_id, logik, typ, jahreswert FROM kostenpositionen_jahre WHERE jahr = ?`,
		jahr,
	)
	if err != nil {
		return nil, fmt.Errorf("query kostenpositionen_jahre: %w", err)
	}
	defer rows.Close()

	out := map[int64]KostenpositionJahr{}
	for rows.Next() {
		kj := KostenpositionJahr{Jahr: jahr}
		if err := rows.Scan(&kj.KostenpositionID, &kj.Logik, &kj.Typ, &kj.Jahreswert); err != nil {
			return nil, fmt.Errorf("scan kostenposition_jahr: %w", err)
		}
		out[kj.KostenpositionID] = kj
	}
	return out, rows.Err()
}

// KostenpositionenJahre returns every Jahr that has Kostenpositionen-Daten,
// newest first - the Stammdaten page's year-block listing.
func KostenpositionenJahre(db *sql.DB) ([]int, error) {
	rows, err := db.Query(`SELECT DISTINCT jahr FROM kostenpositionen_jahre ORDER BY jahr DESC`)
	if err != nil {
		return nil, fmt.Errorf("query kostenpositionen_jahre years: %w", err)
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var jahr int
		if err := rows.Scan(&jahr); err != nil {
			return nil, fmt.Errorf("scan jahr: %w", err)
		}
		out = append(out, jahr)
	}
	return out, rows.Err()
}

// UpsertKostenpositionenJahr writes every given Kostenposition's Logik/Typ/
// Jahreswert for jahr in one transaction - covers both "Jahr anlegen" (first
// write for that jahr) and correcting an existing Jahr (Issue #60).
func UpsertKostenpositionenJahr(db *sql.DB, jahr int, in map[int64]KostenpositionJahrInput) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for kostenpositionID, kpi := range in {
		if _, err := tx.Exec(
			`INSERT INTO kostenpositionen_jahre (kostenposition_id, jahr, logik, typ, jahreswert)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(kostenposition_id, jahr) DO UPDATE SET
			   logik = excluded.logik, typ = excluded.typ, jahreswert = excluded.jahreswert`,
			kostenpositionID, jahr, kpi.Logik, kpi.Typ, kpi.Jahreswert,
		); err != nil {
			return fmt.Errorf("upsert kostenposition %d jahr %d: %w", kostenpositionID, jahr, err)
		}
	}

	return tx.Commit()
}

// DeleteKostenpositionenJahr removes every Kostenposition's data for jahr -
// "Jahr löschen" (Issue #60 Story 16) deletes the whole year, not individual
// Kostenpositionen rows within it.
func DeleteKostenpositionenJahr(db *sql.DB, jahr int) error {
	_, err := db.Exec(`DELETE FROM kostenpositionen_jahre WHERE jahr = ?`, jahr)
	if err != nil {
		return fmt.Errorf("delete kostenpositionen_jahre for jahr %d: %w", jahr, err)
	}
	return nil
}

// LatestJaehrlichWert returns the most recent Jahr at or before maxJahr in
// which kostenpositionID was typ=jaehrlich, and its Jahreswert - the
// "letzter bekannter Jahreswert" fallback calc.Fixkosten uses for a
// monatlich-typed Kostenposition with no explicit Monatswert for a given
// month (Issue #60: a Typ-Wechsel mitten im Jahr leaves later months
// without an explicit fixkosten_werte row). ok is false if no such Jahr
// exists, in which case the fallback is 0.
func LatestJaehrlichWert(db *sql.DB, kostenpositionID int64, maxJahr int) (wert float64, ok bool, err error) {
	err = db.QueryRow(
		`SELECT jahreswert FROM kostenpositionen_jahre
		 WHERE kostenposition_id = ? AND jahr <= ? AND typ = ?
		 ORDER BY jahr DESC LIMIT 1`,
		kostenpositionID, maxJahr, TypJaehrlich,
	).Scan(&wert)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query latest jaehrlich wert for kostenposition %d: %w", kostenpositionID, err)
	}
	return wert, true, nil
}

// FixkostenInput is one monthly Fixkosten-Eingabe, ready to be persisted.
type FixkostenInput struct {
	Monat    string            // YYYY-MM-01, same convention as PeriodInput.ReadingDate
	Personen map[int64]int64   // apartment id -> Personenzahl - own to Fixkosten, not period_occupancy (Issue #60 Story 8)
	Werte    map[int64]float64 // kostenposition id -> expliziter Monatswert, only for that Jahr's monatlich-typed Positionen
}

// ErrFixkostenEintragNotFound is returned by UpdateFixkostenEintrag and
// DeleteFixkostenEintrag when the given id doesn't exist.
var ErrFixkostenEintragNotFound = errors.New("fixkosten eintrag not found")

// ErrNoKostenpositionenJahr is returned by calc.Fixkosten when the given
// Fixkosten-Eingabe's Jahr has no Kostenpositionen-Jahresdaten yet (the
// Jahr was never "angelegt" on /stammdaten) - analogous to
// ErrNoPreviousPeriod's softened-to-a-user-hint handling in the web layer.
var ErrNoKostenpositionenJahr = errors.New("no kostenpositionen jahresdaten for this jahr")

// insertFixkostenTx inserts one Fixkosten-Eingabe with its Werte/Personen.
// Shared by CreateFixkostenEintrag and (a future bulk-insert, should one
// ever be needed) so the write shape stays in one place.
func insertFixkostenTx(tx *sql.Tx, in FixkostenInput) (eintragID int64, err error) {
	res, err := tx.Exec(`INSERT INTO fixkosten_eintraege (monat) VALUES (?)`, in.Monat)
	if err != nil {
		return 0, fmt.Errorf("insert fixkosten eintrag: %w", err)
	}
	eintragID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("fixkosten eintrag id: %w", err)
	}

	for kostenpositionID, wert := range in.Werte {
		if _, err := tx.Exec(
			`INSERT INTO fixkosten_werte (fixkosten_eintrag_id, kostenposition_id, wert) VALUES (?, ?, ?)`,
			eintragID, kostenpositionID, wert,
		); err != nil {
			return 0, fmt.Errorf("insert fixkosten wert for kostenposition %d: %w", kostenpositionID, err)
		}
	}

	for apartmentID, personen := range in.Personen {
		if _, err := tx.Exec(
			`INSERT INTO fixkosten_personen (fixkosten_eintrag_id, apartment_id, personen) VALUES (?, ?, ?)`,
			eintragID, apartmentID, personen,
		); err != nil {
			return 0, fmt.Errorf("insert fixkosten personen for apartment %d: %w", apartmentID, err)
		}
	}

	return eintragID, nil
}

// CreateFixkostenEintrag inserts a new Fixkosten-Eingabe in its own
// transaction.
func CreateFixkostenEintrag(db *sql.DB, in FixkostenInput) (eintragID int64, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	eintragID, err = insertFixkostenTx(tx, in)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return eintragID, nil
}

// UpdateFixkostenEintrag overwrites an existing Fixkosten-Eingabe's Monat/
// Werte/Personen in place - no new row, no history (same "korrigieren"
// convention as UpdatePeriod). Unlike Ablesungen, Fixkosten-Monatswerte
// don't depend on a chronological Vorperiode (no Verbrauch/Diff involved),
// so there's no neighbor-date reordering constraint to enforce here.
func UpdateFixkostenEintrag(db *sql.DB, eintragID int64, in FixkostenInput) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE fixkosten_eintraege SET monat = ? WHERE id = ?`, in.Monat, eintragID)
	if err != nil {
		return fmt.Errorf("update fixkosten eintrag: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("update fixkosten eintrag rows affected: %w", err)
	} else if n == 0 {
		return fmt.Errorf("%w: eintrag %d", ErrFixkostenEintragNotFound, eintragID)
	}

	for kostenpositionID, wert := range in.Werte {
		if _, err := tx.Exec(
			`INSERT INTO fixkosten_werte (fixkosten_eintrag_id, kostenposition_id, wert) VALUES (?, ?, ?)
			 ON CONFLICT(fixkosten_eintrag_id, kostenposition_id) DO UPDATE SET wert = excluded.wert`,
			eintragID, kostenpositionID, wert,
		); err != nil {
			return fmt.Errorf("update fixkosten wert for kostenposition %d: %w", kostenpositionID, err)
		}
	}

	for apartmentID, personen := range in.Personen {
		if _, err := tx.Exec(
			`INSERT INTO fixkosten_personen (fixkosten_eintrag_id, apartment_id, personen) VALUES (?, ?, ?)
			 ON CONFLICT(fixkosten_eintrag_id, apartment_id) DO UPDATE SET personen = excluded.personen`,
			eintragID, apartmentID, personen,
		); err != nil {
			return fmt.Errorf("update fixkosten personen for apartment %d: %w", apartmentID, err)
		}
	}

	return tx.Commit()
}

// DeleteFixkostenEintrag removes a Fixkosten-Eingabe together with its
// Werte/Personen in one transaction.
func DeleteFixkostenEintrag(db *sql.DB, eintragID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM fixkosten_werte WHERE fixkosten_eintrag_id = ?`, eintragID); err != nil {
		return fmt.Errorf("delete fixkosten werte: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM fixkosten_personen WHERE fixkosten_eintrag_id = ?`, eintragID); err != nil {
		return fmt.Errorf("delete fixkosten personen: %w", err)
	}

	res, err := tx.Exec(`DELETE FROM fixkosten_eintraege WHERE id = ?`, eintragID)
	if err != nil {
		return fmt.Errorf("delete fixkosten eintrag: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete fixkosten eintrag rows affected: %w", err)
	} else if n == 0 {
		return fmt.Errorf("%w: eintrag %d", ErrFixkostenEintragNotFound, eintragID)
	}

	return tx.Commit()
}

// FixkostenEintragSummary identifies one Fixkosten-Eingabe without its
// Werte/Personen, for the /fixkosten Übersicht.
type FixkostenEintragSummary struct {
	ID    int64
	Monat string
}

// AllFixkostenEintraege returns every Fixkosten-Eingabe (newest first),
// without Werte/Personen.
func AllFixkostenEintraege(db *sql.DB) ([]FixkostenEintragSummary, error) {
	rows, err := db.Query(`SELECT id, monat FROM fixkosten_eintraege ORDER BY monat DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query fixkosten eintraege: %w", err)
	}
	defer rows.Close()

	var out []FixkostenEintragSummary
	for rows.Next() {
		var f FixkostenEintragSummary
		if err := rows.Scan(&f.ID, &f.Monat); err != nil {
			return nil, fmt.Errorf("scan fixkosten eintrag: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FixkostenEintragDetails is one Fixkosten-Eingabe together with its
// Werte/Personen.
type FixkostenEintragDetails struct {
	ID       int64
	Monat    string
	Personen map[int64]int64   // apartment id -> Personenzahl
	Werte    map[int64]float64 // kostenposition id -> expliziter Monatswert
}

// GetFixkostenEintragDetails returns the given Fixkosten-Eingabe with its
// Werte/Personen, or nil if it doesn't exist.
func GetFixkostenEintragDetails(db *sql.DB, eintragID int64) (*FixkostenEintragDetails, error) {
	f := FixkostenEintragDetails{
		Personen: map[int64]int64{},
		Werte:    map[int64]float64{},
	}
	if err := db.QueryRow(`SELECT id, monat FROM fixkosten_eintraege WHERE id = ?`, eintragID).Scan(&f.ID, &f.Monat); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query fixkosten eintrag %d: %w", eintragID, err)
	}

	werteRows, err := db.Query(`SELECT kostenposition_id, wert FROM fixkosten_werte WHERE fixkosten_eintrag_id = ?`, eintragID)
	if err != nil {
		return nil, fmt.Errorf("query fixkosten werte: %w", err)
	}
	defer werteRows.Close()
	for werteRows.Next() {
		var kostenpositionID int64
		var wert float64
		if err := werteRows.Scan(&kostenpositionID, &wert); err != nil {
			return nil, fmt.Errorf("scan fixkosten wert: %w", err)
		}
		f.Werte[kostenpositionID] = wert
	}
	if err := werteRows.Err(); err != nil {
		return nil, err
	}

	personenRows, err := db.Query(`SELECT apartment_id, personen FROM fixkosten_personen WHERE fixkosten_eintrag_id = ?`, eintragID)
	if err != nil {
		return nil, fmt.Errorf("query fixkosten personen: %w", err)
	}
	defer personenRows.Close()
	for personenRows.Next() {
		var apartmentID, personen int64
		if err := personenRows.Scan(&apartmentID, &personen); err != nil {
			return nil, fmt.Errorf("scan fixkosten personen: %w", err)
		}
		f.Personen[apartmentID] = personen
	}
	if err := personenRows.Err(); err != nil {
		return nil, err
	}

	return &f, nil
}

// GetLatestFixkostenEintrag returns the most recently dated Fixkosten-
// Eingabe, or nil if none exist yet - the "neu erfassen" form's prefill
// source (Issue #60 Story 2/9).
func GetLatestFixkostenEintrag(db *sql.DB) (*FixkostenEintragDetails, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM fixkosten_eintraege ORDER BY monat DESC, id DESC LIMIT 1`).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest fixkosten eintrag id: %w", err)
	}
	return GetFixkostenEintragDetails(db, id)
}
