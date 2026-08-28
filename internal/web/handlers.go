// Package web serves the Nebenkosten-Energierechner's HTML wizard and
// read views. Server-rendered html/template, no SPA build step - see
// https://github.com/larknafets/nebenkosten-energierechner/issues/4.
package web

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/larknafets/nebenkosten-energierechner/internal/calc"
	"github.com/larknafets/nebenkosten-energierechner/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// Each page gets its own *template.Template (layout + exactly one page
// template) - all pages define a "content" block with the same name, so
// parsing them together into one shared template set would let the last
// one silently overwrite the others.
var (
	wizardTemplate = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/wizard.html"))
	letzteTemplate = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/letzte.html"))
)

// meterDisplay describes how one meter's reading is labelled on the
// "letzte Ablesung" view. Order matches the wizard's step grouping.
type meterDisplay struct {
	Key   string
	Label string
	Unit  string
}

var meterDisplays = []meterDisplay{
	{"strom_gesamt", "Strom Gesamt (Netzbezug)", "kWh"},
	{"strom_wohnung2", "Strom Wohnung 2", "kWh"},
	{"strom_waermepumpe", "Strom Wärmepumpe", "kWh"},
	{"strom_wallbox", "Strom Wallboxen", "kWh"},
	{"wasser_gesamt", "Wasser Gesamt", "m³"},
	{"wasser_wohnung2", "Wasser Wohnung 2", "m³"},
	{"wasser_warmwasseraufbereitung", "Wasser Warmwasseraufbereitung", "m³"},
	{"waerme_wohnung1", "Wärme Wohnung 1", "MWh"},
	{"waerme_wohnung2", "Wärme Wohnung 2", "MWh"},
}

// NewMux wires up the wizard and read routes.
func NewMux(db *sql.DB) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex(db))
	mux.HandleFunc("GET /ablesungen/neu", handleWizardForm(db))
	mux.HandleFunc("POST /ablesungen", handleCreateAblesung(db))
	mux.HandleFunc("GET /ablesungen/letzte", handleLetzteAblesung(db))
	return mux
}

func handleIndex(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ablesungen/letzte", http.StatusFound)
	}
}

func handleWizardForm(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			ReadingDate string
			Apartments  []store.Apartment
		}{
			ReadingDate: time.Now().Format("2006-01-02"),
			Apartments:  apartments,
		}

		if err := wizardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func handleCreateAblesung(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		readings := make(map[string]float64, len(store.MeterKeys))
		for _, key := range store.MeterKeys {
			v, err := strconv.ParseFloat(r.FormValue(key), 64)
			if err != nil {
				http.Error(w, "invalid value for "+key, http.StatusBadRequest)
				return
			}
			readings[key] = v
		}

		strompreis, err1 := strconv.ParseFloat(r.FormValue("strompreis"), 64)
		frischwasserPreis, err2 := strconv.ParseFloat(r.FormValue("frischwasser_preis"), 64)
		abwasserPreis, err3 := strconv.ParseFloat(r.FormValue("abwasser_preis"), 64)
		if err1 != nil || err2 != nil || err3 != nil {
			http.Error(w, "invalid price value", http.StatusBadRequest)
			return
		}

		personen := make(map[int64]int64, len(apartments))
		qm := make(map[int64]float64, len(apartments))
		for _, a := range apartments {
			idStr := strconv.FormatInt(a.ID, 10)
			p, err := strconv.ParseInt(r.FormValue("personen_"+idStr), 10, 64)
			if err != nil {
				http.Error(w, "invalid Personenzahl for apartment "+idStr, http.StatusBadRequest)
				return
			}
			personen[a.ID] = p

			q, err := strconv.ParseFloat(r.FormValue("qm_"+idStr), 64)
			if err != nil {
				http.Error(w, "invalid Wohnfläche for apartment "+idStr, http.StatusBadRequest)
				return
			}
			qm[a.ID] = q
		}

		_, err = store.CreatePeriod(db, store.PeriodInput{
			ReadingDate:       r.FormValue("reading_date"),
			Strompreis:        strompreis,
			FrischwasserPreis: frischwasserPreis,
			AbwasserPreis:     abwasserPreis,
			Readings:          readings,
			Personen:          personen,
			QM:                qm,
		})
		if err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/ablesungen/letzte", http.StatusFound)
	}
}

func handleLetzteAblesung(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		period, err := store.GetLatestPeriod(db)
		if err != nil {
			http.Error(w, "latest period: "+err.Error(), http.StatusInternalServerError)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var meters []struct {
			Label string
			Value float64
			Unit  string
		}
		personen := map[int64]int64{}
		if period != nil {
			personen = period.PersonenByApartment
			for _, m := range meterDisplays {
				meters = append(meters, struct {
					Label string
					Value float64
					Unit  string
				}{m.Label, period.Readings[m.Key], m.Unit})
			}
		}

		var strom *calc.StromErgebnis
		var kostenNote string
		if period != nil {
			strom, err = calc.Strom(db, period.ID)
			if err == store.ErrNoPreviousPeriod {
				kostenNote = "Kosten können erst ab der zweiten Ablesung berechnet werden (Verbrauch braucht eine Vorperiode)."
			} else if err != nil {
				http.Error(w, "strom kosten: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		data := struct {
			Period     *store.LatestPeriod
			Apartments []store.Apartment
			Personen   map[int64]int64
			Meters     []struct {
				Label string
				Value float64
				Unit  string
			}
			Strom      *calc.StromErgebnis
			KostenNote string
		}{
			Period:     period,
			Apartments: apartments,
			Personen:   personen,
			Meters:     meters,
			Strom:      strom,
			KostenNote: kostenNote,
		}

		if err := letzteTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
