package calc

import (
	"database/sql"
	"fmt"

	"github.com/larknafets/nebenkosten-energierechner/internal/store"
)

// EinspeisungErgebnis is the PV-Einspeisevergütung result for one period -
// whole-house, no apartment split (the Einspeisezähler isn't apartment-
// specific), unlike StromErgebnis/WasserErgebnis/HeizungErgebnis (Ticket #47).
type EinspeisungErgebnis struct {
	EinspeisungKWh float64

	// Ertrag is kaufmännisch auf Cent gerundet (Issue #8-Konvention).
	Ertrag float64
}

// Einspeisung computes the PV-Einspeisevergütung for the given period:
// eingespeiste kWh (Verbrauch des Einspeisezählers) mal Einspeisung-Preis.
func Einspeisung(db *sql.DB, periodID int64) (*EinspeisungErgebnis, error) {
	period, err := store.GetPeriodByID(db, periodID)
	if err != nil {
		return nil, err
	}

	verbrauch, err := store.Verbrauch(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("verbrauch: %w", err)
	}

	kwh := verbrauch["strom_einspeisung"]
	return &EinspeisungErgebnis{
		EinspeisungKWh: kwh,
		Ertrag:         Round2(kwh * period.EinspeisungPreis),
	}, nil
}
