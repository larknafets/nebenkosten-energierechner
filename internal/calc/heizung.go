package calc

import (
	"database/sql"
	"fmt"

	"github.com/larknafets/nebenkosten-energierechner/internal/store"
)

// HeizungErgebnis is the Heizung/Warmwasser-Kostenverteilung result for one
// period. See https://github.com/larknafets/nebenkosten-energierechner/issues/16:
// die Wärmepumpen-Stromkosten (aus der Strom-Kostenberechnung, #14) werden
// 70/30 nach Wärmeverbrauch und Wohnfläche auf beide Wohnungen verteilt.
type HeizungErgebnis struct {
	TotalHeizungskostenUnrounded float64

	WaermeW1MWh float64
	WaermeW2MWh float64
	QMW1        float64
	QMW2        float64

	RatioWaermeW1  float64
	RatioWaermeW2  float64
	RatioFlaecheW1 float64
	RatioFlaecheW2 float64

	// KostenHeizung* sind kaufmännisch auf Cent gerundet (Issue #8).
	KostenHeizungW1 float64
	KostenHeizungW2 float64
}

// Heizung computes the Heizungskosten-Zuteilung for the given period.
func Heizung(db *sql.DB, periodID int64) (*HeizungErgebnis, error) {
	strom, err := Strom(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("strom: %w", err)
	}

	verbrauch, err := store.Verbrauch(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("verbrauch: %w", err)
	}

	apartments, err := store.Apartments(db)
	if err != nil {
		return nil, fmt.Errorf("apartments: %w", err)
	}
	var qmW1, qmW2 float64
	for _, a := range apartments {
		switch a.ID {
		case 1:
			qmW1 = a.QM
		case 2:
			qmW2 = a.QM
		}
	}

	waermeW1 := verbrauch["waerme_wohnung1"]
	waermeW2 := verbrauch["waerme_wohnung2"]

	var ratioWaermeW1, ratioWaermeW2 float64
	if total := waermeW1 + waermeW2; total > 0 {
		ratioWaermeW1 = waermeW1 / total
		ratioWaermeW2 = waermeW2 / total
	}

	var ratioFlaecheW1, ratioFlaecheW2 float64
	if total := qmW1 + qmW2; total > 0 {
		ratioFlaecheW1 = qmW1 / total
		ratioFlaecheW2 = qmW2 / total
	}

	total := strom.KostenWPGesamtUnrounded

	return &HeizungErgebnis{
		TotalHeizungskostenUnrounded: total,

		WaermeW1MWh: waermeW1,
		WaermeW2MWh: waermeW2,
		QMW1:        qmW1,
		QMW2:        qmW2,

		RatioWaermeW1:  ratioWaermeW1,
		RatioWaermeW2:  ratioWaermeW2,
		RatioFlaecheW1: ratioFlaecheW1,
		RatioFlaecheW2: ratioFlaecheW2,

		KostenHeizungW1: Round2(total * (0.7*ratioWaermeW1 + 0.3*ratioFlaecheW1)),
		KostenHeizungW2: Round2(total * (0.7*ratioWaermeW2 + 0.3*ratioFlaecheW2)),
	}, nil
}
