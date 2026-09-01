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
	"math"
	"net/http"
	"strconv"
	"strings"
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
	wizardTemplate     = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/wizard.html"))
	ablesungTemplate   = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/ablesung.html"))
	ablesungenTemplate = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/ablesungen.html"))
	dashboardTemplate  = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/dashboard.html"))

	berechnungslogikTemplate = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/berechnungslogik.html"))
)

// meterDisplay describes how one meter's reading is labelled on the
// Ablesung detail view. Order matches the wizard's step grouping.
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

var germanMonthsShort = [...]string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

// germanPeriodLabelShort is germanPeriodLabel's abbreviated form (e.g. "Nov
// 2026"), for the Verlauf month labels (Ticket #19) where every row needs
// to fit next to a bar.
func germanPeriodLabelShort(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return fmt.Sprintf("%s %d", germanMonthsShort[t.Month()-1], t.Year())
}

// formatDecimalDE renders a float64 the way germanPeriodLabel renders a
// date: German convention, decimal comma instead of point (Ticket #36),
// rounded to at most 2 decimal places (Ticket #37 - raw calc.go values
// like WWAnteil/WaermeMWh carry floating-point noise past 2 places, e.g.
// "25,333333333333332"). Not padded - 'f'/-1 on the rounded value keeps
// "0,7" as "0,7", not "0,70"; use formatEuroDE where padding to exactly 2
// places is wanted (EUR amounts).
func formatDecimalDE(x float64) string {
	rounded := math.Round(x*100) / 100
	return strings.ReplaceAll(strconv.FormatFloat(rounded, 'f', -1, 64), ".", ",")
}

// formatEuroDE renders a float64 as a German-formatted EUR amount, always
// padded to exactly 2 decimal places (Ticket #40 - a currency amount reads
// as "45,00", not "45"). EUR amounts are already Round2'd (kaufmännisch,
// Issue #8) before reaching here, so the fixed 2-place formatting doesn't
// change the value, only pads its display.
func formatEuroDE(x float64) string {
	return strings.ReplaceAll(strconv.FormatFloat(x, 'f', 2, 64), ".", ",")
}

// formatDatumDE renders a period's ReadingDate ("YYYY-MM-DD") in the German
// DD.MM.YYYY form (Ticket #36). Falls back to the raw string if it isn't a
// parseable date, same convention as germanPeriodLabel.
func formatDatumDE(readingDate string) string {
	t, err := time.Parse("2006-01-02", readingDate)
	if err != nil {
		return readingDate
	}
	return t.Format("02.01.2006")
}

var templateFuncs = template.FuncMap{
	"de":      formatDecimalDE,
	"deEUR":   formatEuroDE,
	"deDatum": formatDatumDE,
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
	mux.HandleFunc("GET /ablesungen", handleAblesungenListe(db))
	mux.HandleFunc("GET /ablesungen/neu", handleWizardForm(db))
	mux.HandleFunc("POST /ablesungen", handleCreateAblesung(db))
	mux.HandleFunc("GET /ablesungen/{id}", handleAblesungDetail(db))
	mux.HandleFunc("GET /ablesungen/{id}/bearbeiten", handleEditWizardForm(db))
	mux.HandleFunc("POST /ablesungen/{id}", handleUpdateAblesung(db))
	mux.HandleFunc("POST /ablesungen/{id}/loeschen", handleDeleteAblesung(db))
	mux.HandleFunc("GET /dashboard", handleDashboard(db))
	mux.HandleFunc("GET /berechnungslogik", handleBerechnungslogik())
	return mux
}

// ingressBase turns Home Assistant's X-Ingress-Path header (the path
// prefix its Supervisor proxy strips before forwarding the request here -
// see Ticket #22) into a base to prepend onto every generated link, form
// action, and redirect, so they still resolve through the proxy. Empty for
// direct access (no header) - trailing slash trimmed so callers can always
// just concatenate "<base>/some/path" without a double slash.
func ingressBase(header string) string {
	return strings.TrimSuffix(header, "/")
}

func requestBase(r *http.Request) string {
	return ingressBase(r.Header.Get("X-Ingress-Path"))
}

func handleIndex(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, requestBase(r)+"/dashboard", http.StatusFound)
	}
}

// wizardData is the Ablesung form's template data - shared by "erfassen"
// (a fresh Ablesung, prefilled from the previous period as a convenience)
// and "korrigieren" (Ticket #34: editing the latest Ablesung in place,
// prefilled with its own current values). HasPrevious/PreviousReadings/
// PreviousReadingDate/OutlierAvg always describe the genuine previous
// period - the negative-Verbrauch/Ausreißer-Warnung baseline the new
// values are checked against - never the Ablesung being edited itself.
type wizardData struct {
	Base                string
	FormAction          string
	IsEdit              bool
	ReadingDate         string
	Apartments          []store.Apartment
	HasPrevious         bool
	PreviousReadings    map[string]float64
	PreviousReadingDate string
	HasOutlierBaseline  bool
	OutlierAvg          map[string]float64

	// EditReadings prefills the meter inputs' value= in edit mode with the
	// Ablesung's own current Zählerstände - distinct from PreviousReadings
	// above, which stays the actual previous period for the warning
	// comparison.
	EditReadings map[string]float64

	// Prefill for the "Preise & Personen" step (Ticket #28): the previous
	// period's values in create mode, or (Ticket #34) the Ablesung's own
	// current values in edit mode - either way just an editable starting
	// value, not a data-prev warning target like the meter readings above.
	PreviousStrompreis        float64
	PreviousFrischwasserPreis float64
	PreviousAbwasserPreis     float64
	PreviousPersonen          map[int64]int64
	// PreviousHeizungGewichtung always has a valid value (defaulting to
	// 0.7, Ticket #27's default) since the radio group needs exactly one
	// option checked - unlike the blank-when-absent price fields above,
	// this can't just be left empty.
	PreviousHeizungGewichtung float64
}

// outlierAvg computes the Ausreißer-Warnung baseline (Ticket #13) from up
// to 4 recent periods (newest first): the average of the 3 consumption
// diffs between them. ok is false if fewer than 4 are available.
func outlierAvg(recent []store.PeriodReadings) (avg map[string]float64, ok bool) {
	if len(recent) < 4 {
		return nil, false
	}
	avg = make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		sum := 0.0
		for i := 0; i < 3; i++ {
			sum += recent[i].Readings[key] - recent[i+1].Readings[key]
		}
		avg[key] = sum / 3
	}
	return avg, true
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

		previousPeriod, err := store.GetLatestPeriod(db)
		if err != nil {
			http.Error(w, "latest period: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := wizardData{
			Base:                      requestBase(r),
			FormAction:                requestBase(r) + "/ablesungen",
			ReadingDate:               time.Now().Format("2006-01-02"),
			Apartments:                apartments,
			PreviousHeizungGewichtung: 0.7,
		}
		if len(recent) > 0 {
			data.HasPrevious = true
			data.PreviousReadings = recent[0].Readings
			data.PreviousReadingDate = recent[0].ReadingDate
		}
		if previousPeriod != nil {
			data.PreviousStrompreis = previousPeriod.Strompreis
			data.PreviousFrischwasserPreis = previousPeriod.FrischwasserPreis
			data.PreviousAbwasserPreis = previousPeriod.AbwasserPreis
			data.PreviousPersonen = previousPeriod.PersonenByApartment
			data.PreviousHeizungGewichtung = previousPeriod.HeizungWaermeGewichtung
		}
		data.OutlierAvg, data.HasOutlierBaseline = outlierAvg(recent)

		if err := wizardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleEditWizardForm serves the "korrigieren" form for an arbitrary
// Ablesung (Ticket #44 - generalized from Ticket #34's latest-only version),
// prefilled with its own current values. The negative-Verbrauch/
// Ausreißer-Warnung baseline always compares against the genuine previous
// period - the one chronologically before the Ablesung being edited,
// regardless of whether newer Ablesungen exist after it.
func handleEditWizardForm(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		target, err := store.GetPeriodDetails(db, periodID)
		if err != nil {
			http.Error(w, "period: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if target == nil {
			http.NotFound(w, r)
			return
		}

		recent, err := store.PeriodReadingsBefore(db, periodID, 4)
		if err != nil {
			http.Error(w, "recent periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := wizardData{
			Base:                      requestBase(r),
			FormAction:                fmt.Sprintf("%s/ablesungen/%d", requestBase(r), target.ID),
			IsEdit:                    true,
			ReadingDate:               target.ReadingDate,
			Apartments:                apartments,
			EditReadings:              target.Readings,
			PreviousStrompreis:        target.Strompreis,
			PreviousFrischwasserPreis: target.FrischwasserPreis,
			PreviousAbwasserPreis:     target.AbwasserPreis,
			PreviousPersonen:          target.PersonenByApartment,
			PreviousHeizungGewichtung: target.HeizungWaermeGewichtung,
		}
		if len(recent) > 0 {
			data.HasPrevious = true
			data.PreviousReadings = recent[0].Readings
			data.PreviousReadingDate = recent[0].ReadingDate
		}
		data.OutlierAvg, data.HasOutlierBaseline = outlierAvg(recent)

		if err := wizardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// heizungGewichtungOptions are the only allowed Heizungs-Split-Gewichtungen
// (Ticket #27) - a fixed choice, not a free-text field, so an invalid or
// out-of-range value can't silently skew every apartment's Heizungskosten.
var heizungGewichtungOptions = map[string]float64{"0.7": 0.7, "0.6": 0.6, "0.5": 0.5}

// parseHeizungGewichtung validates the Wizard's Heizung-Gewichtung form
// value against heizungGewichtungOptions.
func parseHeizungGewichtung(raw string) (float64, error) {
	v, ok := heizungGewichtungOptions[raw]
	if !ok {
		return 0, fmt.Errorf("invalid Heizung-Gewichtung %q (muss 0.7, 0.6 oder 0.5 sein)", raw)
	}
	return v, nil
}

// parsePeriodInput parses an Ablesung form (shared by handleCreateAblesung
// and handleUpdateAblesung - Ticket #34, same fields either way, only what
// happens with the result differs).
func parsePeriodInput(r *http.Request, apartments []store.Apartment) (store.PeriodInput, error) {
	readings := make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		v, err := strconv.ParseFloat(r.FormValue(key), 64)
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("invalid value for %s", key)
		}
		readings[key] = v
	}

	strompreis, err1 := strconv.ParseFloat(r.FormValue("strompreis"), 64)
	frischwasserPreis, err2 := strconv.ParseFloat(r.FormValue("frischwasser_preis"), 64)
	abwasserPreis, err3 := strconv.ParseFloat(r.FormValue("abwasser_preis"), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return store.PeriodInput{}, fmt.Errorf("invalid price value")
	}

	heizungGewichtung, err := parseHeizungGewichtung(r.FormValue("heizung_gewichtung"))
	if err != nil {
		return store.PeriodInput{}, err
	}

	personen := make(map[int64]int64, len(apartments))
	qm := make(map[int64]float64, len(apartments))
	for _, a := range apartments {
		idStr := strconv.FormatInt(a.ID, 10)
		p, err := strconv.ParseInt(r.FormValue("personen_"+idStr), 10, 64)
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("invalid Personenzahl for apartment %s", idStr)
		}
		personen[a.ID] = p

		q, err := strconv.ParseFloat(r.FormValue("qm_"+idStr), 64)
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("invalid Wohnfläche for apartment %s", idStr)
		}
		qm[a.ID] = q
	}

	return store.PeriodInput{
		ReadingDate:             r.FormValue("reading_date"),
		Strompreis:              strompreis,
		FrischwasserPreis:       frischwasserPreis,
		AbwasserPreis:           abwasserPreis,
		HeizungWaermeGewichtung: heizungGewichtung,
		Readings:                readings,
		Personen:                personen,
		QM:                      qm,
	}, nil
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

		in, err := parsePeriodInput(r, apartments)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		periodID, err := store.CreatePeriod(db, in)
		if err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("%s/ablesungen/%d", requestBase(r), periodID), http.StatusFound)
	}
}

// dateNeighborBounds finds, among `all` periods excluding periodID, the
// closest ReadingDate below and above `currentDate` - the range a
// correction may move periodID's own date within without silently
// reordering it past a neighbor (Ticket #44 review finding: generalizing
// "korrigieren" to any period means a date change can now shift which
// period is whose Vorperiode for everyone in between, not just itself).
func dateNeighborBounds(all []store.PeriodSummary, periodID int64, currentDate string) (prev, next string, hasPrev, hasNext bool) {
	for _, p := range all {
		if p.ID == periodID {
			continue
		}
		if p.ReadingDate <= currentDate && (!hasPrev || p.ReadingDate > prev) {
			prev, hasPrev = p.ReadingDate, true
		}
		if p.ReadingDate >= currentDate && (!hasNext || p.ReadingDate < next) {
			next, hasNext = p.ReadingDate, true
		}
	}
	return
}

// handleUpdateAblesung corrects an existing Ablesung in place (Ticket #34,
// generalized to any period by Ticket #44 - no restriction to the latest
// one anymore, see store.UpdatePeriod).
func handleUpdateAblesung(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}

		existing, err := store.GetPeriodDetails(db, periodID)
		if err != nil {
			http.Error(w, "period: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if existing == nil {
			http.NotFound(w, r)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		in, err := parsePeriodInput(r, apartments)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		allPeriods, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
			return
		}
		prev, next, hasPrev, hasNext := dateNeighborBounds(allPeriods, periodID, existing.ReadingDate)
		if hasPrev && in.ReadingDate <= prev {
			http.Error(w, fmt.Sprintf("Ablesedatum muss nach der Vorperiode (%s) liegen", prev), http.StatusBadRequest)
			return
		}
		if hasNext && in.ReadingDate >= next {
			http.Error(w, fmt.Sprintf("Ablesedatum muss vor der Folgeperiode (%s) liegen", next), http.StatusBadRequest)
			return
		}

		if err := store.UpdatePeriod(db, periodID, in); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("%s/ablesungen/%d", requestBase(r), periodID), http.StatusFound)
	}
}

// handleDeleteAblesung deletes a period (Ticket #45). Any period is
// deletable, including the last remaining one - no "abgeschlossen" status
// exists in this app; the client-side confirm() dialog is the only
// safety net (see ablesung.html).
func handleDeleteAblesung(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}

		if err := store.DeletePeriod(db, periodID); err != nil {
			if errors.Is(err, store.ErrPeriodNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/ablesungen", http.StatusFound)
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

// handleAblesungenListe lists every recorded period (Ticket #43), newest
// first, linking each to its detail view.
func handleAblesungenListe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periods, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			Base    string
			Periods []store.PeriodSummary
		}{
			Base:    requestBase(r),
			Periods: periods,
		}

		if err := ablesungenTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleAblesungDetail shows one period's Zählerstände and full
// Kostenaufstellung (Ticket #43, generalized from the old "letzte Ablesung"
// view to any period by id), with a dropdown to jump to any other period.
func handleAblesungDetail(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}

		period, err := store.GetPeriodDetails(db, periodID)
		if err != nil {
			http.Error(w, "period: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if period == nil {
			http.NotFound(w, r)
			return
		}

		allPeriods, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
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
		for _, m := range meterDisplays {
			meters = append(meters, struct {
				Label string
				Value float64
				Unit  string
			}{m.Label, period.Readings[m.Key], m.Unit})
		}

		k, err := berechneKosten(db, period.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			Base       string
			Period     *store.LatestPeriod
			AllPeriods []store.PeriodSummary
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
			Base:       requestBase(r),
			Period:     period,
			AllPeriods: allPeriods,
			Apartments: apartments,
			Personen:   period.PersonenByApartment,
			Meters:     meters,
			Strom:      k.Strom,
			Wasser:     k.Wasser,
			Heizung:    k.Heizung,
			KostenNote: k.KostenNote,
		}

		if err := ablesungTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// dashboardCard is one apartment's stat card on the Dashboard (Grundansicht
// #17 + Detailanzeige #18): its Gesamtbetrag and the badge showing that
// period's Wohnfläche + Personenzahl, plus the Strom/Heizung/Wasser
// breakdown the bar and per-category text are rendered from.
type dashboardCard struct {
	ApartmentName string
	QM            float64
	Personen      int64
	Gesamtbetrag  float64
	Kategorien    []kategorie
}

// kategorie is one cost position (Strom, Heizung, or Wasser) on a
// dashboardCard's bar, together with its share of that apartment's
// Gesamtbetrag (ProzentGesamt, the bar segment's width) and the raw
// consumption shown in brackets next to the EUR amount (Ticket #18).
type kategorie struct {
	Label     string
	Kosten    float64
	Verbrauch float64
	Einheit   string
	// Farbe is a bare CSS class suffix (e.g. "strom" -> class "cat-strom"),
	// not interpolated into a style attribute - html/template's CSS
	// sanitizer can't statically verify a dynamic var(...) argument there
	// and replaces it with the ZgotmplZ sentinel instead of rendering it.
	Farbe string

	ProzentGesamt float64
}

// kategorien builds the given apartment's cost breakdown for the period.
// Wohnung 1's Strom has no own cost position - its Netzbezug stays implicit
// (see calc.Strom) - so only Wohnung 2 gets a Strom-Kategorie. Frischwasser
// and Abwasser are combined into a single Wasser-Kategorie since they share
// one raw m³ consumption (no separate Abwasserzähler, see calc.Wasser).
func kategorien(apartmentID int64, k kosten) []kategorie {
	var list []kategorie
	if apartmentID == 2 {
		list = append(list, kategorie{"Strom", k.Strom.KostenW2, k.Strom.W2AnteilKWh, "kWh", "strom", 0})
	}

	heizungKosten, waermeMWh := k.Heizung.KostenHeizungW1, k.Heizung.WaermeW1MWh
	frischwasserKosten, abwasserKosten, wasserM3 := k.Wasser.KostenFrischwasserW1, k.Wasser.KostenAbwasserW1, k.Wasser.FrischwasserW1
	if apartmentID == 2 {
		heizungKosten, waermeMWh = k.Heizung.KostenHeizungW2, k.Heizung.WaermeW2MWh
		frischwasserKosten, abwasserKosten, wasserM3 = k.Wasser.KostenFrischwasserW2, k.Wasser.KostenAbwasserW2, k.Wasser.FrischwasserW2
	}
	list = append(list,
		kategorie{"Heizung", heizungKosten, waermeMWh, "MWh", "heizung", 0},
		kategorie{"Wasser", calc.Round2(frischwasserKosten + abwasserKosten), wasserM3, "m³", "wasser", 0},
	)

	var total float64
	for _, kat := range list {
		total += kat.Kosten
	}
	if total > 0 {
		for i := range list {
			list[i].ProzentGesamt = list[i].Kosten / total * 100
		}
	}
	return list
}

// periodKosten is one period's already-computed kosten, for the Verlauf
// view (Ticket #19). ReadingDate stays in its raw "YYYY-MM-DD" form (not
// pre-formatted) because verlaufMonate needs it for the month label and
// mitJahrestrennern needs it to detect calendar-year boundaries (#20).
type periodKosten struct {
	ReadingDate string
	K           kosten
}

// verlaufSegment is one Verlauf bar's Strom/Heizung/Wasser slice, scaled
// against the newest period's Gesamtbetrag rather than its own (see
// verlaufMonate). Label/Verbrauch/Einheit carry the same category's raw
// consumption (Ticket #39's Verbrauchs-Ansicht) alongside Kosten - the bar
// itself always stays EUR-scaled (ProzentNeuestesGesamt), since kWh/MWh/m³
// can't be stacked into one meaningful width.
type verlaufSegment struct {
	Farbe                 string
	Label                 string
	Kosten                float64
	Verbrauch             float64
	Einheit               string
	ProzentNeuestesGesamt float64
}

// verlaufMonat is one row of the Verlauf bar-per-month view.
type verlaufMonat struct {
	Label        string
	IsCurrent    bool
	Gesamtbetrag float64
	Segmente     []verlaufSegment
}

// verlaufJahrestrenner is the "Jahreswert" separator inserted right before
// a calendar year's December row (Ticket #20).
type verlaufJahrestrenner struct {
	Jahr       int
	Jahreswert float64
}

// verlaufEintrag is one row of a Verlauf column: either a Monat or (if
// Jahrestrenner is set) a year separator. Exactly one of the two is set.
type verlaufEintrag struct {
	Monat         *verlaufMonat
	Jahrestrenner *verlaufJahrestrenner
}

// verlaufColumn is one apartment's Verlauf column: its Eintraege, newest
// first.
type verlaufColumn struct {
	ApartmentName string
	Eintraege     []verlaufEintrag
}

// verlaufMonate builds the given apartment's Verlauf rows, newest first
// (periodenKosten is assumed already in that order - its first entry is
// "current"). Every bar is scaled against the *newest* period's
// Gesamtbetrag, not its own: an older, more expensive month's segments then
// add up past 100% instead of the scale compressing to fit it (Ticket #19 -
// the newest month is the reference to measure/save against).
func verlaufMonate(apartmentID int64, periodenKosten []periodKosten) []verlaufMonat {
	if len(periodenKosten) == 0 {
		return nil
	}

	var neuestesGesamt float64
	for _, kat := range kategorien(apartmentID, periodenKosten[0].K) {
		neuestesGesamt += kat.Kosten
	}

	out := make([]verlaufMonat, 0, len(periodenKosten))
	for i, pk := range periodenKosten {
		kats := kategorien(apartmentID, pk.K)
		segmente := make([]verlaufSegment, 0, len(kats))
		var gesamtbetrag float64
		for _, kat := range kats {
			gesamtbetrag += kat.Kosten
			var pct float64
			if neuestesGesamt > 0 {
				pct = kat.Kosten / neuestesGesamt * 100
			}
			segmente = append(segmente, verlaufSegment{
				Farbe:                 kat.Farbe,
				Label:                 kat.Label,
				Kosten:                kat.Kosten,
				Verbrauch:             kat.Verbrauch,
				Einheit:               kat.Einheit,
				ProzentNeuestesGesamt: pct,
			})
		}
		out = append(out, verlaufMonat{
			Label:        germanPeriodLabelShort(pk.ReadingDate),
			IsCurrent:    i == 0,
			Gesamtbetrag: calc.Round2(gesamtbetrag),
			Segmente:     segmente,
		})
	}
	return out
}

// mitJahrestrennern inserts a verlaufJahrestrenner right before each
// calendar year's December row (Ticket #20): "sobald beim rückwärts
// iterieren ein Dezember-Eintrag ansteht, wird davor ein Trenner +
// Jahreswert eingefügt". A year with no recorded December period (e.g. the
// current, still-running year) gets no separator at all - its Jahreswert
// would be incomplete. monate and periodenKosten must be the same length
// and in the same order (verlaufMonate's output paired with its input).
func mitJahrestrennern(monate []verlaufMonat, periodenKosten []periodKosten) []verlaufEintrag {
	if len(monate) == 0 {
		return nil
	}

	out := make([]verlaufEintrag, 0, len(monate))
	for i, pk := range periodenKosten {
		t, err := time.Parse("2006-01-02", pk.ReadingDate)
		if err == nil && t.Month() == time.December {
			var jahreswert float64
			for j, pk2 := range periodenKosten {
				if t2, err2 := time.Parse("2006-01-02", pk2.ReadingDate); err2 == nil && t2.Year() == t.Year() {
					jahreswert += monate[j].Gesamtbetrag
				}
			}
			out = append(out, verlaufEintrag{Jahrestrenner: &verlaufJahrestrenner{Jahr: t.Year(), Jahreswert: calc.Round2(jahreswert)}})
		}
		m := monate[i]
		out = append(out, verlaufEintrag{Monat: &m})
	}
	return out
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
					kats := kategorien(a.ID, k)
					var total float64
					for _, kat := range kats {
						total += kat.Kosten
					}
					cards = append(cards, dashboardCard{
						ApartmentName: a.Name,
						QM:            a.QM,
						Personen:      period.PersonenByApartment[a.ID],
						Gesamtbetrag:  calc.Round2(total),
						Kategorien:    kats,
					})
				}
			}
		}

		allPeriods, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "all periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Verlauf (Ticket #19) walks newest -> oldest and stops at the first
		// period without a Vorperiode - that's always the very first period
		// ever recorded (every later one has an earlier neighbour to diff
		// against), so it's the natural end of the available history.
		var periodenKosten []periodKosten
		for _, p := range allPeriods {
			pk, err := berechneKosten(db, p.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if pk.KostenNote != "" {
				break
			}
			periodenKosten = append(periodenKosten, periodKosten{ReadingDate: p.ReadingDate, K: pk})
		}

		var verlaufSpalten []verlaufColumn
		for _, a := range apartments {
			verlaufSpalten = append(verlaufSpalten, verlaufColumn{
				ApartmentName: a.Name,
				Eintraege:     mitJahrestrennern(verlaufMonate(a.ID, periodenKosten), periodenKosten),
			})
		}

		data := struct {
			Base           string
			Period         *store.LatestPeriod
			PeriodLabel    string
			Cards          []dashboardCard
			KostenNote     string
			HasVerlauf     bool
			VerlaufSpalten []verlaufColumn
		}{
			Base:           requestBase(r),
			Period:         period,
			PeriodLabel:    periodLabel,
			Cards:          cards,
			KostenNote:     kostenNote,
			HasVerlauf:     len(periodenKosten) > 0,
			VerlaufSpalten: verlaufSpalten,
		}

		if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleBerechnungslogik serves a static, informational explanation of the
// cost formulas (Ticket #33) - no DB access, the content never depends on
// any period's data.
func handleBerechnungslogik() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct{ Base string }{Base: requestBase(r)}
		if err := berechnungslogikTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
