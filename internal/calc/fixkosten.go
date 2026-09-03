package calc

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// FixkostenPosition is one of the 14 Kostenpositionen's result for one
// Fixkosten-Eingabe: its Logik/Typ for that Jahr, the whole-house
// Monatswert, and its split onto both Wohnungen.
type FixkostenPosition struct {
	Key        string
	Label      string
	Logik      string
	Typ        string
	Monatswert float64
	KostenW1   float64
	KostenW2   float64
}

// FixkostenErgebnis is one Fixkosten-Eingabe's full breakdown - all 14
// Positionen plus each Wohnung's total (kaufmännisch auf Cent gerundet,
// Issue #8, same convention as calc.Strom/Heizung/Wasser).
type FixkostenErgebnis struct {
	Positionen []FixkostenPosition
	KostenW1   float64
	KostenW2   float64
}

// Fixkosten computes eintragID's full Kostenpositionen-Aufteilung. Returns
// store.ErrNoKostenpositionenJahr if the Eingabe's Jahr was never angelegt
// on /stammdaten.
func Fixkosten(db *sql.DB, eintragID int64) (*FixkostenErgebnis, error) {
	eintrag, err := store.GetFixkostenEintragDetails(db, eintragID)
	if err != nil {
		return nil, fmt.Errorf("fixkosten eintrag: %w", err)
	}
	if eintrag == nil {
		return nil, fmt.Errorf("fixkosten eintrag %d not found", eintragID)
	}

	jahr, err := jahrFromMonat(eintrag.Monat)
	if err != nil {
		return nil, err
	}

	jahresdaten, err := store.KostenpositionenJahr(db, jahr)
	if err != nil {
		return nil, fmt.Errorf("kostenpositionen jahresdaten: %w", err)
	}
	if len(jahresdaten) == 0 {
		return nil, store.ErrNoKostenpositionenJahr
	}

	kostenpositionen, err := store.Kostenpositionen(db)
	if err != nil {
		return nil, fmt.Errorf("kostenpositionen: %w", err)
	}

	apartments, err := store.Apartments(db)
	if err != nil {
		return nil, fmt.Errorf("apartments: %w", err)
	}
	var qmW1, qmW2, flurstueckW1, flurstueckW2 float64
	for _, a := range apartments {
		switch a.ID {
		case 1:
			qmW1, flurstueckW1 = a.QM, a.FlurstueckGroesse
		case 2:
			qmW2, flurstueckW2 = a.QM, a.FlurstueckGroesse
		}
	}
	personenW1 := float64(eintrag.Personen[1])
	personenW2 := float64(eintrag.Personen[2])

	ergebnis := FixkostenErgebnis{Positionen: make([]FixkostenPosition, 0, len(kostenpositionen))}

	for _, kp := range kostenpositionen {
		kj, ok := jahresdaten[kp.ID]
		if !ok {
			// Jahr exists (checked above) but this specific Kostenposition has
			// no row in it - UpsertKostenpositionenJahr always writes all 14
			// together, so this only happens if the DB was edited by hand.
			// Skip rather than guess a Logik/Typ.
			continue
		}

		monatswert, err := monatswertFuer(db, kp.ID, kj, eintrag, jahr)
		if err != nil {
			return nil, err
		}

		ratioW1, ratioW2 := splitRatio(kj.Logik, qmW1, qmW2, flurstueckW1, flurstueckW2, personenW1, personenW2)

		kostenW1 := Round2(monatswert * ratioW1)
		kostenW2 := Round2(monatswert * ratioW2)

		ergebnis.Positionen = append(ergebnis.Positionen, FixkostenPosition{
			Key:        kp.Key,
			Label:      kp.Label,
			Logik:      kj.Logik,
			Typ:        kj.Typ,
			Monatswert: monatswert,
			KostenW1:   kostenW1,
			KostenW2:   kostenW2,
		})
		ergebnis.KostenW1 += kostenW1
		ergebnis.KostenW2 += kostenW2
	}

	ergebnis.KostenW1 = Round2(ergebnis.KostenW1)
	ergebnis.KostenW2 = Round2(ergebnis.KostenW2)

	return &ergebnis, nil
}

// monatswertFuer resolves one Kostenposition's whole-house Monatswert:
// jaehrlich positions come from Jahreswert/12; monatlich positions use the
// Eingabe's own explicit Wert, falling back to the last known Jahreswert/12
// (or 0) when a Typ-Wechsel left this month without one (Issue #60).
func monatswertFuer(db *sql.DB, kostenpositionID int64, kj store.KostenpositionJahr, eintrag *store.FixkostenEintragDetails, jahr int) (float64, error) {
	if kj.Typ == store.TypJaehrlich {
		return kj.Jahreswert / 12, nil
	}

	if wert, ok := eintrag.Werte[kostenpositionID]; ok {
		return wert, nil
	}

	letzterJahreswert, ok, err := store.LatestJaehrlichWert(db, kostenpositionID, jahr)
	if err != nil {
		return 0, fmt.Errorf("letzter bekannter jahreswert: %w", err)
	}
	if !ok {
		return 0, nil
	}
	return letzterJahreswert / 12, nil
}

// splitRatio returns Wohnung 1/2's shares for the given Logik. wohneinheit
// is a fixed 50/50; the other 3 delegate to Ratio2 (50/50 fallback when
// both inputs are 0, Issue #26's zero-guard convention).
func splitRatio(logik string, qmW1, qmW2, flurstueckW1, flurstueckW2, personenW1, personenW2 float64) (w1, w2 float64) {
	switch logik {
	case store.LogikFlurstueck:
		return Ratio2(flurstueckW1, flurstueckW2)
	case store.LogikQM:
		return Ratio2(qmW1, qmW2)
	case store.LogikPersonen:
		return Ratio2(personenW1, personenW2)
	default: // store.LogikWohneinheit
		return 0.5, 0.5
	}
}

// jahrFromMonat parses a Fixkosten-Eingabe's Monat ("YYYY-MM-01") into its
// calendar year.
func jahrFromMonat(monat string) (int, error) {
	t, err := time.Parse("2006-01-02", monat)
	if err != nil {
		return 0, fmt.Errorf("invalid monat %q: %w", monat, err)
	}
	return t.Year(), nil
}
