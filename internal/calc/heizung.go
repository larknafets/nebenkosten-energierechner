package calc

import (
	"database/sql"
	"fmt"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// HeizungErgebnis is the Heizung/Warmwasser-Kostenverteilung result for one
// period. See https://github.com/larknafets/nebenkostenrechner/issues/16:
// die Wärmepumpen-Stromkosten (aus der Strom-Kostenberechnung, #14) werden
// nach Wärmeverbrauch und Wohnfläche auf beide Wohnungen verteilt, gewichtet
// mit der Periode-eigenen HeizungWaermeGewichtung (0.7/0.6/0.5, Default 0.7
// - Issue #27; früher fix 70/30).
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

	// WPAnteil*KWh ist strom.WPAnteilKWh, mit denselben Gewichten wie
	// KostenHeizung* auf die Wohnungen verteilt.
	WPAnteilW1KWh float64
	WPAnteilW2KWh float64
}

// Heizung computes the Heizungskosten-Zuteilung for the given period.
func Heizung(db *sql.DB, periodID int64) (*HeizungErgebnis, error) {
	strom, err := Strom(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("strom: %w", err)
	}

	period, err := store.GetPeriodByID(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("period: %w", err)
	}
	gewichtungWaerme := period.HeizungWaermeGewichtung
	gewichtungFlaeche := 1 - gewichtungWaerme

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

	ratioWaermeW1, ratioWaermeW2 := Ratio2(waermeW1, waermeW2)

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

		KostenHeizungW1: Round2(total * (gewichtungWaerme*ratioWaermeW1 + gewichtungFlaeche*ratioFlaecheW1)),
		KostenHeizungW2: Round2(total * (gewichtungWaerme*ratioWaermeW2 + gewichtungFlaeche*ratioFlaecheW2)),

		WPAnteilW1KWh: strom.WPAnteilKWh * (gewichtungWaerme*ratioWaermeW1 + gewichtungFlaeche*ratioFlaecheW1),
		WPAnteilW2KWh: strom.WPAnteilKWh * (gewichtungWaerme*ratioWaermeW2 + gewichtungFlaeche*ratioFlaecheW2),
	}, nil
}
