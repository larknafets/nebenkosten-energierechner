package web

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// widgetEntity resolves a Widget-Route's {entity} Pfadsegment to either an
// Apartment-ID (Wohnung 1/2) or one of the whole-house simpleSeries
// (Wallboxen/PV-Anlage) - exactly the 4 Entitäten the Dashboard's
// Jahressummen-Karten/Wohnung-Tabs already show. "" for an unknown slug.
func widgetEntity(slug string) (apartmentID int64, simple *simpleSeries) {
	switch slug {
	case "wohnung-1":
		return 1, nil
	case "wohnung-2":
		return 2, nil
	case "wallboxen":
		return 0, &wallboxSeries
	case "pv-anlage":
		return 0, &pvSeries
	}
	return 0, nil
}

// findApartment returns the apartment with the given id, or the zero value
// if absent (only reachable if the DB's fixed 2-apartment seed data was
// somehow removed).
func findApartment(apartments []store.Apartment, id int64) store.Apartment {
	for _, a := range apartments {
		if a.ID == id {
			return a
		}
	}
	return store.Apartment{}
}

// handleWidgetJahressumme serves the Ingress-freie HA-Widget-Route (Issue
// #77 ff.): exactly one Entity's Jahressummen-Karte, ohne Nav/Footer/
// Theme-Toggle - gedacht für ein Lovelace "Webpage card" Iframe. {entity}
// ist eine der 4 festen Wohnung-Tab-Entitäten (siehe widgetEntity).
func handleWidgetJahressumme(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartmentID, simple := widgetEntity(r.PathValue("entity"))
		if apartmentID == 0 && simple == nil {
			http.NotFound(w, r)
			return
		}

		dd, err := loadDashboardData(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			HasAnyData         bool
			AnzeigeJahr        int
			AnzeigeJahrLaufend bool
			Card               *dashboardJahresCard
			SimpleCard         *dashboardSimpleCard
		}{HasAnyData: dd.HasAnyData, AnzeigeJahr: dd.Jahr, AnzeigeJahrLaufend: dd.Jahr == time.Now().Year()}

		if dd.HasAnyData {
			if simple != nil {
				c := buildSimpleJahresCard(*simple, dd.Jahr, dd.PeriodenKosten)
				data.SimpleCard = &c
			} else {
				a := findApartment(dd.Apartments, apartmentID)
				c := buildJahresCard(a.ID, a.Name, a.QM, a.FlurstueckGroesse, dd.Jahr, dd.PeriodenKosten, dd.FixkostenListe)
				data.Card = &c
			}
		}

		if err := widgetJahressummeTemplate.ExecuteTemplate(w, "widget-layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleWidgetVerbrauchswerte serves the 2. Ingress-freie HA-Widget-Route:
// exactly ein Entity's Monatsverlauf-Panel (Verbrauch/Verbrauchswerte/
// Fixkosten/Kombiniert-Umschalter bleibt nutzbar, nur die Entity ist fest).
func handleWidgetVerbrauchswerte(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartmentID, simple := widgetEntity(r.PathValue("entity"))
		if apartmentID == 0 && simple == nil {
			http.NotFound(w, r)
			return
		}

		dd, err := loadDashboardData(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			HasAnyData    bool
			Verlauf       *dashboardVerlaufSpalte
			SimpleVerlauf *dashboardSimpleSpalte
			LogikOptions  []logikOption
		}{HasAnyData: dd.HasAnyData, LogikOptions: logikOptions}

		if dd.HasAnyData {
			if simple != nil {
				v := buildSimpleVerlauf(*simple, dd.PeriodenKosten)
				data.SimpleVerlauf = &v
			} else {
				a := findApartment(dd.Apartments, apartmentID)
				v := buildDashboardVerlauf(a.ID, a.Name, dd.PeriodenKosten, dd.FixkostenListe)
				data.Verlauf = &v
			}
		}

		if err := widgetVerbrauchswerteTemplate.ExecuteTemplate(w, "widget-layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleWidgetUebersicht serves die 3. HA-Widget-Route (Issue #78): die
// Jahressumme-Karte und das Verbrauchswerte-Panel derselben Entity
// untereinander, in einer Seite - reuses beide Body-Templates
// ("widget-jahressumme-body"/"widget-verbrauchswerte-body", widget_layout.
// html) statt sie ein 3. Mal zu duplizieren, und lädt die Daten nur einmal.
func handleWidgetUebersicht(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartmentID, simple := widgetEntity(r.PathValue("entity"))
		if apartmentID == 0 && simple == nil {
			http.NotFound(w, r)
			return
		}

		dd, err := loadDashboardData(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			HasAnyData         bool
			AnzeigeJahr        int
			AnzeigeJahrLaufend bool
			Card               *dashboardJahresCard
			SimpleCard         *dashboardSimpleCard
			Verlauf            *dashboardVerlaufSpalte
			SimpleVerlauf      *dashboardSimpleSpalte
			LogikOptions       []logikOption
		}{HasAnyData: dd.HasAnyData, AnzeigeJahr: dd.Jahr, AnzeigeJahrLaufend: dd.Jahr == time.Now().Year(), LogikOptions: logikOptions}

		if dd.HasAnyData {
			if simple != nil {
				c := buildSimpleJahresCard(*simple, dd.Jahr, dd.PeriodenKosten)
				data.SimpleCard = &c
				v := buildSimpleVerlauf(*simple, dd.PeriodenKosten)
				data.SimpleVerlauf = &v
			} else {
				a := findApartment(dd.Apartments, apartmentID)
				c := buildJahresCard(a.ID, a.Name, a.QM, a.FlurstueckGroesse, dd.Jahr, dd.PeriodenKosten, dd.FixkostenListe)
				data.Card = &c
				v := buildDashboardVerlauf(a.ID, a.Name, dd.PeriodenKosten, dd.FixkostenListe)
				data.Verlauf = &v
			}
		}

		if err := widgetUebersichtTemplate.ExecuteTemplate(w, "widget-layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// NewWidgetMux serves ONLY die 3 read-only HA-Widget-Routen, deliberately
// separate from NewMux's full app (Ablesungen/Fixkosten bearbeiten/
// löschen etc.) - gedacht, auf einem 2. Port außerhalb von Ingress zu
// laufen (siehe cmd/nebenkostenrechner), also ohne jede Auth: kleinstmögliche
// Angriffsfläche, kein Zugriff auf mutierende Routen.
func NewWidgetMux(db *sql.DB) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /widget/jahressumme/{entity}", handleWidgetJahressumme(db))
	mux.HandleFunc("GET /widget/verbrauchswerte/{entity}", handleWidgetVerbrauchswerte(db))
	mux.HandleFunc("GET /widget/uebersicht/{entity}", handleWidgetUebersicht(db))
	return mux
}
