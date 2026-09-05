package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// dashboardData is every piece of already-computed data the Dashboard and
// the HA-Widget-Routen (Issue #77 ff.) build their views from - factored
// out of handleDashboard so both share one implementation of "walk every
// Periode/Fixkosten-Eingabe and compute this Jahr's Kosten" instead of
// diverging copies.
type dashboardData struct {
	HasAnyData     bool
	Apartments     []store.Apartment
	PeriodenKosten []periodKosten
	FixkostenListe []fixkostenKosten
	Jahr           int
}

// loadDashboardData runs the shared, DB-heavy first half of both
// handleDashboard and the widget handlers: apartments, every period's
// Kosten (stopping at the oldest Periode ohne Vorperiode, same as before),
// every Fixkosten-Eingabe's Ergebnis, and the auto-following Anzeigejahr.
// HasAnyData=false (zero-value everything else) when neither Ablesungen
// noch Fixkosten-Eingaben exist yet.
func loadDashboardData(db *sql.DB) (dashboardData, error) {
	apartments, err := store.Apartments(db)
	if err != nil {
		return dashboardData{}, fmt.Errorf("apartments: %w", err)
	}

	allPeriods, err := store.AllPeriods(db)
	if err != nil {
		return dashboardData{}, fmt.Errorf("all periods: %w", err)
	}
	fixkostenEingaben, err := store.AllFixkostenEingaben(db)
	if err != nil {
		return dashboardData{}, fmt.Errorf("fixkosten eingaben: %w", err)
	}

	if len(allPeriods) == 0 && len(fixkostenEingaben) == 0 {
		return dashboardData{Apartments: apartments}, nil
	}

	// Verbrauch walks newest -> oldest and stops at the first period
	// without a Vorperiode - that's always the very first period ever
	// recorded (every later one has an earlier neighbour to diff
	// against), so it's the natural end of the available history.
	var periodenKosten []periodKosten
	for _, p := range allPeriods {
		pk, err := berechneKosten(db, p.ID)
		if err != nil {
			return dashboardData{}, err
		}
		if pk.KostenNote != "" {
			break
		}
		personen, err := store.PersonenByApartment(db, p.ID)
		if err != nil {
			return dashboardData{}, fmt.Errorf("personen: %w", err)
		}
		periodenKosten = append(periodenKosten, periodKosten{ReadingDate: p.ReadingDate, Monat: p.Monat, K: pk, Personen: personen})
	}

	fixkostenListe, err := alleFixkostenKosten(db)
	if err != nil {
		return dashboardData{}, err
	}

	return dashboardData{
		HasAnyData:     true,
		Apartments:     apartments,
		PeriodenKosten: periodenKosten,
		FixkostenListe: fixkostenListe,
		Jahr:           anzeigeJahr(allPeriods, fixkostenEingaben),
	}, nil
}

// handleDashboard serves the redesigned Dashboard (Issue #60): Jahressummen-
// Karten je Wohnung for the auto-following Anzeigejahr, then a Wohnung-
// Umschalter with a combined Verbrauch+Fixkosten Monatsverlauf (4 Modi).
func handleDashboard(db *sql.DB, version, buildDate string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dd, err := loadDashboardData(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !dd.HasAnyData {
			data := struct {
				Base       string
				Aktuell    string
				HasAnyData bool
				Version    string
				BuildDate  string
			}{Base: requestBase(r), Aktuell: "dashboard", Version: version, BuildDate: buildDate}
			if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		apartments, periodenKosten, fixkostenListe, jahr := dd.Apartments, dd.PeriodenKosten, dd.FixkostenListe, dd.Jahr

		var cards []dashboardJahresCard
		var verlaufSpalten []dashboardVerlaufSpalte
		for _, a := range apartments {
			cards = append(cards, buildJahresCard(a.ID, a.Name, a.QM, a.FlurstueckGroesse, jahr, periodenKosten, fixkostenListe))
			verlaufSpalten = append(verlaufSpalten, buildDashboardVerlauf(a.ID, a.Name, periodenKosten, fixkostenListe))
		}

		// Wallboxen/PV-Anlage (Ticket #67) - whole-house, rein informative
		// Entities neben den Wohnung-Tabs, analog zum Prototyp
		// (docs/prototypes/fixkosten-prototype.html): eigene Jahressumme +
		// Monatsverlauf, kein Fixkosten-Anteil, keine Wohnungs-Zuteilung.
		wallboxCard := buildSimpleJahresCard(wallboxSeries, jahr, periodenKosten)
		wallboxVerlauf := buildSimpleVerlauf(wallboxSeries, periodenKosten)
		pvCard := buildSimpleJahresCard(pvSeries, jahr, periodenKosten)
		pvVerlauf := buildSimpleVerlauf(pvSeries, periodenKosten)

		latestVersion, updateAvailable := checkForUpdate(version)

		data := struct {
			Base               string
			Aktuell            string
			HasAnyData         bool
			AnzeigeJahr        int
			AnzeigeJahrLaufend bool
			Cards              []dashboardJahresCard
			VerlaufSpalten     []dashboardVerlaufSpalte
			WallboxCard        dashboardSimpleCard
			WallboxVerlauf     dashboardSimpleSpalte
			PVCard             dashboardSimpleCard
			PVVerlauf          dashboardSimpleSpalte
			LogikOptions       []logikOption
			Version            string
			BuildDate          string
			UpdateAvailable    bool
			LatestVersion      string
		}{
			Base:               requestBase(r),
			Aktuell:            "dashboard",
			HasAnyData:         true,
			AnzeigeJahr:        jahr,
			AnzeigeJahrLaufend: jahr == time.Now().Year(),
			Cards:              cards,
			VerlaufSpalten:     verlaufSpalten,
			WallboxCard:        wallboxCard,
			WallboxVerlauf:     wallboxVerlauf,
			PVCard:             pvCard,
			PVVerlauf:          pvVerlauf,
			LogikOptions:       logikOptions,
			Version:            version,
			BuildDate:          buildDate,
			UpdateAvailable:    updateAvailable,
			LatestVersion:      latestVersion,
		}

		if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
