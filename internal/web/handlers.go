// Package web serves the Nebenkosten-Energierechner's HTML wizard and
// read views. Server-rendered html/template, no SPA build step - see
// https://github.com/larknafets/nebenkosten-energierechner/issues/4.
package web

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
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
	wizardTemplate    = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/wizard.html"))
	letzteTemplate    = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/letzte.html"))
	dashboardTemplate = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/dashboard.html"))
)

// meterDisplay describes how one meter's reading is labelled on the
// "letzte Ablesung" view. Order matches the wizard's step grouping.
type meterDisplay struct {
	Key   string
	Label string
	Unit  string
}

var germanMonths = [...]string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// germanPeriodLabel renders a period's ReadingDate ("YYYY-MM-DD") as its
// German month name and year (e.g. "November 2026"), for the Dashboard
// heading (Ticket #17 Nachtrag - no "Dashboard -" prefix). Falls back to the
// raw string if it isn't a parseable date.
func germanPeriodLabel(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return fmt.Sprintf("%s %d", germanMonths[t.Month()-1], t.Year())
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
	mux.HandleFunc("GET /dashboard", handleDashboard(db))
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

		// 4 periods give the 3 consumption diffs the Ausreißer-Warnung
		// baseline averages over (Ticket #13); the newest of them also
		// doubles as "previous" for the negative-Verbrauch/gap checks
		// (Ticket #12).
		recent, err := store.RecentPeriodReadings(db, 4)
		if err != nil {
			http.Error(w, "recent periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			ReadingDate         string
			Apartments          []store.Apartment
			HasPrevious         bool
			PreviousReadings    map[string]float64
			PreviousReadingDate string
			HasOutlierBaseline  bool
			OutlierAvg          map[string]float64
		}{
			ReadingDate: time.Now().Format("2006-01-02"),
			Apartments:  apartments,
		}
		if len(recent) > 0 {
			data.HasPrevious = true
			data.PreviousReadings = recent[0].Readings
			data.PreviousReadingDate = recent[0].ReadingDate
		}
		if len(recent) >= 4 {
			avg := make(map[string]float64, len(store.MeterKeys))
			for _, key := range store.MeterKeys {
				sum := 0.0
				for i := 0; i < 3; i++ {
					sum += recent[i].Readings[key] - recent[i+1].Readings[key]
				}
				avg[key] = sum / 3
			}
			data.HasOutlierBaseline = true
			data.OutlierAvg = avg
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

// kosten bundles the 3 cost calculations for one period, together with a
// user-facing note for the "no previous period yet" case both the "letzte
// Ablesung" and Dashboard views need to show identically.
type kosten struct {
	Strom      *calc.StromErgebnis
	Wasser     *calc.WasserErgebnis
	Heizung    *calc.HeizungErgebnis
	KostenNote string
}

func berechneKosten(db *sql.DB, periodID int64) (kosten, error) {
	strom, err := calc.Strom(db, periodID)
	if errors.Is(err, store.ErrNoPreviousPeriod) {
		return kosten{KostenNote: "Kosten können erst ab der zweiten Ablesung berechnet werden (Verbrauch braucht eine Vorperiode)."}, nil
	} else if err != nil {
		return kosten{}, fmt.Errorf("strom kosten: %w", err)
	}

	wasser, err := calc.Wasser(db, periodID)
	if err != nil {
		return kosten{}, fmt.Errorf("wasser kosten: %w", err)
	}

	heizung, err := calc.Heizung(db, periodID)
	if err != nil {
		return kosten{}, fmt.Errorf("heizung kosten: %w", err)
	}

	return kosten{Strom: strom, Wasser: wasser, Heizung: heizung}, nil
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

		var k kosten
		if period != nil {
			k, err = berechneKosten(db, period.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
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
			Wasser     *calc.WasserErgebnis
			Heizung    *calc.HeizungErgebnis
			KostenNote string
		}{
			Period:     period,
			Apartments: apartments,
			Personen:   personen,
			Meters:     meters,
			Strom:      k.Strom,
			Wasser:     k.Wasser,
			Heizung:    k.Heizung,
			KostenNote: k.KostenNote,
		}

		if err := letzteTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// dashboardCard is one apartment's stat card on the Dashboard Grundansicht
// (Ticket #17): its Gesamtbetrag for the period and the badge showing that
// period's Wohnfläche + Personenzahl.
type dashboardCard struct {
	ApartmentName string
	QM            float64
	Personen      int64
	Gesamtbetrag  float64
}

// gesamtbetrag sums every cost position billed to the given apartment for
// the period. Wohnung 1's Strom has no own cost position - its Netzbezug
// stays implicit (see calc.Strom) - so only Wohnung 2 has a Strom position.
func gesamtbetrag(apartmentID int64, k kosten) float64 {
	total := k.Heizung.KostenHeizungW1 + k.Wasser.KostenFrischwasserW1 + k.Wasser.KostenAbwasserW1
	if apartmentID == 2 {
		total = k.Strom.KostenW2 + k.Heizung.KostenHeizungW2 + k.Wasser.KostenFrischwasserW2 + k.Wasser.KostenAbwasserW2
	}
	return calc.Round2(total)
}

func handleDashboard(db *sql.DB) http.HandlerFunc {
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

		var periodLabel string
		var cards []dashboardCard
		var kostenNote string
		if period != nil {
			periodLabel = germanPeriodLabel(period.ReadingDate)

			k, err := berechneKosten(db, period.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			kostenNote = k.KostenNote

			if k.KostenNote == "" {
				for _, a := range apartments {
					cards = append(cards, dashboardCard{
						ApartmentName: a.Name,
						QM:            a.QM,
						Personen:      period.PersonenByApartment[a.ID],
						Gesamtbetrag:  gesamtbetrag(a.ID, k),
					})
				}
			}
		}

		data := struct {
			Period      *store.LatestPeriod
			PeriodLabel string
			Cards       []dashboardCard
			KostenNote  string
		}{
			Period:      period,
			PeriodLabel: periodLabel,
			Cards:       cards,
			KostenNote:  kostenNote,
		}

		if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
