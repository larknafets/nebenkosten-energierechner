package calc

import (
	"database/sql"
	"fmt"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// StromErgebnis is the PV-Netzbezug-Zuteilung result for one period. See
// https://github.com/larknafets/nebenkostenrechner/issues/2 for the
// formula: Netzbezug wird zuerst Wohnung 2 zugeteilt (gedeckelt auf ihren
// Verbrauch), dann die Wärmepumpe auf den Rest - was danach übrig bleibt,
// zählt implizit zu Wohnung 1 (keine eigene Kostenposition).
type StromErgebnis struct {
	NetzbezugGesamtKWh float64
	W2AnteilKWh        float64
	WPAnteilKWh        float64

	// PVAnteilW2KWh/PVAnteilWPKWh is the gap between the submeter's own
	// consumption and what the min()-cap actually attributed to Netzbezug -
	// since the submeters read gross consumption while Netzbezug is net of
	// PV self-consumption, a gap can only exist because PV covered it
	// (Ticket #50: "Nicht dem Netzbezug zugeordnet (PV)").
	PVAnteilW2KWh float64
	PVAnteilWPKWh float64

	// KostenW2 is the displayed cost position for Wohnung 2 - kaufmännisch
	// auf Cent gerundet (Issue #8).
	KostenW2 float64

	// KostenWPGesamtUnrounded is the (not yet rounded) total cost of the
	// Wärmepumpe's electricity - it isn't a displayed position on its own,
	// it's the input the Heizungs-Kostenberechnung-Ticket (#7 / #16)
	// splits 70/30 between the two apartments.
	KostenWPGesamtUnrounded float64
}

// Strom computes the Stromkosten-Zuteilung for the given period.
func Strom(db *sql.DB, periodID int64) (*StromErgebnis, error) {
	period, err := store.GetPeriodByID(db, periodID)
	if err != nil {
		return nil, err
	}

	verbrauch, err := store.Verbrauch(db, periodID)
	if err != nil {
		return nil, fmt.Errorf("verbrauch: %w", err)
	}

	netzbezugGesamt := verbrauch["strom_gesamt"]

	w2Anteil := min(netzbezugGesamt, verbrauch["strom_wohnung2"])
	rest1 := netzbezugGesamt - w2Anteil
	wpAnteil := min(rest1, verbrauch["strom_waermepumpe"])

	return &StromErgebnis{
		NetzbezugGesamtKWh:      netzbezugGesamt,
		W2AnteilKWh:             w2Anteil,
		WPAnteilKWh:             wpAnteil,
		PVAnteilW2KWh:           max(0, verbrauch["strom_wohnung2"]-w2Anteil),
		PVAnteilWPKWh:           max(0, verbrauch["strom_waermepumpe"]-wpAnteil),
		KostenW2:                Round2(w2Anteil * period.Strompreis),
		KostenWPGesamtUnrounded: wpAnteil * period.Strompreis,
	}, nil
}
