package calc

import (
	"database/sql"
	"fmt"

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

// KostenFor returns this Position's Kosten for the given apartment (1 or 2)
// - the shared "which Wohnung's field" selector callers building per-
// apartment breakdowns (Dashboard Fixkosten-Modus, Jahressummen-Karten)
// would otherwise repeat as their own if/switch.
func (p FixkostenPosition) KostenFor(apartmentID int64) float64 {
	if apartmentID == 2 {
		return p.KostenW2
	}
	return p.KostenW1
}

// FixkostenErgebnis is one Fixkosten-Eingabe's full breakdown - all 14
// Positionen plus each Wohnung's total (kaufmännisch auf Cent gerundet,
// Issue #8, same convention as calc.Strom/Heizung/Wasser).
type FixkostenErgebnis struct {
	Positionen []FixkostenPosition
	KostenW1   float64
	KostenW2   float64
}

// KostenFor returns this Ergebnis's total Kosten for the given apartment (1
// or 2), same selector convention as FixkostenPosition.KostenFor.
func (e FixkostenErgebnis) KostenFor(apartmentID int64) float64 {
	if apartmentID == 2 {
		return e.KostenW2
	}
	return e.KostenW1
}

// Fixkosten computes eingabeID's full Kostenpositionen-Aufteilung. Returns
// store.ErrNoKostenpositionenJahr if the Eingabe's Jahr was never angelegt
// on /stammdaten.
func Fixkosten(db *sql.DB, eingabeID int64) (*FixkostenErgebnis, error) {
	eingabe, err := store.GetFixkostenEingabeDetails(db, eingabeID)
	if err != nil {
		return nil, fmt.Errorf("fixkosten eingabe: %w", err)
	}
	if eingabe == nil {
		return nil, fmt.Errorf("fixkosten eingabe %d not found", eingabeID)
	}

	jahr, err := store.JahrFromMonat(eingabe.Monat)
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
	qmW1, qmW2 := apartmentValues(apartments, func(a store.Apartment) float64 { return a.QM })
	flurstueckW1, flurstueckW2 := apartmentValues(apartments, func(a store.Apartment) float64 { return a.FlurstueckGroesse })
	personenW1 := float64(eingabe.Personen[1])
	personenW2 := float64(eingabe.Personen[2])

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

		monatswert, err := monatswertFuer(db, kp.ID, kj, eingabe, jahr)
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
func monatswertFuer(db *sql.DB, kostenpositionID int64, kj store.KostenpositionJahr, eingabe *store.FixkostenEingabeDetails, jahr int) (float64, error) {
	if kj.Typ == store.TypJaehrlich {
		return kj.Jahreswert / 12, nil
	}

	if wert, ok := eingabe.Werte[kostenpositionID]; ok {
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
