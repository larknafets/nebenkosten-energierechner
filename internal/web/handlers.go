// Package web serves the Nebenkostenrechner's HTML wizard and
// read views. Server-rendered html/template, no SPA build step - see
// https://github.com/larknafets/nebenkostenrechner/issues/4.
package web

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
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
	stammdatenTemplate       = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/stammdaten.html"))

	fixkostenListeTemplate  = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/fixkosten.html"))
	fixkostenFormTemplate   = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/fixkosten_form.html"))
	fixkostenDetailTemplate = template.Must(template.New("layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/fixkosten_detail.html"))

	// widget-layout Templates (Issue #77 ff.) - eigene Shell statt "layout",
	// teilen sich nur "styles" (layout.html) mit dem Rest der App.
	widgetJahressummeTemplate     = template.Must(template.New("widget_layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/widget_layout.html", "templates/widget_jahressumme.html"))
	widgetVerbrauchswerteTemplate = template.Must(template.New("widget_layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/widget_layout.html", "templates/widget_verbrauchswerte.html"))
	widgetUebersichtTemplate      = template.Must(template.New("widget_layout.html").Funcs(templateFuncs).ParseFS(templateFS, "templates/layout.html", "templates/widget_layout.html", "templates/widget_uebersicht.html"))
)

// meterDisplay describes how one meter's reading is labelled on the
// Ablesung detail view. Order matches the wizard's step grouping.
type meterDisplay struct {
	Key   string
	Label string
	Unit  string
}

// kategorieIconPaths maps a Jahressummen-Karte Kategorie-Zeile's exact
// Label to its MDI-Icon path data - shown before the value, colored via the
// surrounding .v-<Farbe> span (fill:currentColor). Keyed by Label rather
// than Farbe since 2 Labels share the "pv" Farbe (PV-Anteil/Einspeise-
// vergütung) but want visually distinct icons.
var kategorieIconPaths = map[string]string{
	"Fixkosten":          "M3,22L4.5,20.5L6,22L7.5,20.5L9,22L10.5,20.5L12,22L13.5,20.5L15,22L16.5,20.5L18,22L19.5,20.5L21,22V2L19.5,3.5L18,2L16.5,3.5L15,2L13.5,3.5L12,2L10.5,3.5L9,2L7.5,3.5L6,2L4.5,3.5L3,2M18,9H6V7H18M18,13H6V11H18M18,17H6V15H18V17Z",
	"Strom":              "M11 15H6L13 1V9H18L11 23V15Z",
	"Stromkosten":        "M11 15H6L13 1V9H18L11 23V15Z",
	"Heizung/Ww":         "M17.66 11.2C17.43 10.9 17.15 10.64 16.89 10.38C16.22 9.78 15.46 9.35 14.82 8.72C13.33 7.26 13 4.85 13.95 3C13 3.23 12.17 3.75 11.46 4.32C8.87 6.4 7.85 10.07 9.07 13.22C9.11 13.32 9.15 13.42 9.15 13.55C9.15 13.77 9 13.97 8.8 14.05C8.57 14.15 8.33 14.09 8.14 13.93C8.08 13.88 8.04 13.83 8 13.76C6.87 12.33 6.69 10.28 7.45 8.64C5.78 10 4.87 12.3 5 14.47C5.06 14.97 5.12 15.47 5.29 15.97C5.43 16.57 5.7 17.17 6 17.7C7.08 19.43 8.95 20.67 10.96 20.92C13.1 21.19 15.39 20.8 17.03 19.32C18.86 17.66 19.5 15 18.56 12.72L18.43 12.46C18.22 12 17.66 11.2 17.66 11.2M14.5 17.5C14.22 17.74 13.76 18 13.4 18.1C12.28 18.5 11.16 17.94 10.5 17.28C11.69 17 12.4 16.12 12.61 15.23C12.78 14.43 12.46 13.77 12.33 13C12.21 12.26 12.23 11.63 12.5 10.94C12.69 11.32 12.89 11.7 13.13 12C13.9 13 15.11 13.44 15.37 14.8C15.41 14.94 15.43 15.08 15.43 15.23C15.46 16.05 15.1 16.95 14.5 17.5H14.5Z",
	"Wasser":             "M12,20A6,6 0 0,1 6,14C6,10 12,3.25 12,3.25C12,3.25 18,10 18,14A6,6 0 0,1 12,20Z",
	"PV-Anteil":          "M11.45,2V5.55L15,3.77L11.45,2M10.45,8L8,10.46L11.75,11.71L10.45,8M2,11.45L3.77,15L5.55,11.45H2M10,2H2V10C2.57,10.17 3.17,10.25 3.77,10.25C7.35,10.26 10.26,7.35 10.27,3.75C10.26,3.16 10.17,2.57 10,2M17,22V16H14L19,7V13H22L17,22Z",
	"Einspeisevergütung": "M11.39 5.45L9.61 4.55L10.87 2H19.34L20.61 4.55L18.83 5.44L18.11 4H12.11L11.39 5.45M21.73 8H17.2L16.41 5H13.81L13 8H8.5L7.21 10.55L9 11.44L9.73 10H20.5L21.21 11.45L23 10.56L21.73 8M20.88 22H18.81L18.57 21.1L15.11 15.9L11.64 21.1L11.41 22H9.34L12.23 11H14.3L13.94 12.35L15.11 14.1L16.27 12.35L15.92 11H18L20.88 22M14.5 15L13.61 13.65L12.43 18.13L14.5 15M17.79 18.12L16.61 13.64L15.71 15L17.79 18.12M9 16L5 12V15H1V17H5V20L9 16Z",
}

// kategorieIcon renders the given Kategorie-Zeile Label's MDI-Icon as
// inline SVG (fill:currentColor, so it takes on the surrounding
// .v-<Farbe> span's text color) - empty for an unmapped Label.
func kategorieIcon(label string) template.HTML {
	path, ok := kategorieIconPaths[label]
	if !ok {
		return ""
	}
	return template.HTML(`<svg viewBox="0 0 24 24" width="13" height="13" style="vertical-align:-2px;margin-right:3px" fill="currentColor"><path d="` + path + `"/></svg>`)
}

var templateFuncs = template.FuncMap{
	"de":            formatDecimalDE,
	"de0":           formatDecimalDE0,
	"de1":           formatDecimalDE1,
	"de2":           formatDecimalDE2,
	"deEUR":         formatEuroDE,
	"deDatum":       formatDatumDE,
	"deDatumZeit":   formatDatumZeitDE,
	"kategorieIcon": kategorieIcon,
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
	{"strom_einspeisung", "Einspeisung (PV)", "kWh"},
}

// parseFormFloat reads and parses one form field, returning a German
// user-facing error naming fieldLabel/apartmentID on failure - shared by
// /stammdaten's per-apartment Wohnungsgröße/Flurstücksgröße fields.
func parseFormFloat(r *http.Request, name, fieldLabel, apartmentID string) (float64, error) {
	v, err := strconv.ParseFloat(r.FormValue(name), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s for apartment %s", fieldLabel, apartmentID)
	}
	return v, nil
}

// NewMux wires up the wizard and read routes.
func NewMux(db *sql.DB, version, buildDate string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex(db))
	mux.HandleFunc("GET /ablesungen", handleAblesungenListe(db))
	mux.HandleFunc("GET /ablesungen/export.csv", handleExportCSV(db))
	mux.HandleFunc("GET /ablesungen/neu", handleWizardForm(db))
	mux.HandleFunc("POST /ablesungen", handleCreateAblesung(db))
	mux.HandleFunc("POST /ablesungen/import", handleImportCSV(db))
	mux.HandleFunc("GET /ablesungen/{id}", handleAblesungDetail(db))
	mux.HandleFunc("GET /ablesungen/{id}/bearbeiten", handleEditWizardForm(db))
	mux.HandleFunc("POST /ablesungen/{id}", handleUpdateAblesung(db))
	mux.HandleFunc("POST /ablesungen/{id}/loeschen", handleDeleteAblesung(db))
	mux.HandleFunc("GET /dashboard", handleDashboard(db, version, buildDate))
	mux.HandleFunc("GET /berechnungslogik", handleBerechnungslogik())
	mux.HandleFunc("GET /stammdaten", handleStammdatenForm(db))
	mux.HandleFunc("POST /stammdaten", handleUpdateStammdaten(db))
	mux.HandleFunc("POST /stammdaten/jahre", handleCreateKostenpositionenJahr(db))
	mux.HandleFunc("POST /stammdaten/jahre/{jahr}", handleUpdateKostenpositionenJahr(db))
	mux.HandleFunc("POST /stammdaten/jahre/{jahr}/loeschen", handleDeleteKostenpositionenJahr(db))
	mux.HandleFunc("GET /fixkosten", handleFixkostenListe(db))
	mux.HandleFunc("GET /fixkosten/neu", handleFixkostenForm(db))
	mux.HandleFunc("POST /fixkosten", handleCreateFixkosten(db))
	mux.HandleFunc("GET /fixkosten/{id}", handleFixkostenDetail(db))
	mux.HandleFunc("GET /fixkosten/{id}/bearbeiten", handleFixkostenEditForm(db))
	mux.HandleFunc("POST /fixkosten/{id}", handleUpdateFixkosten(db))
	mux.HandleFunc("POST /fixkosten/{id}/loeschen", handleDeleteFixkosten(db))
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
	Base        string
	Aktuell     string
	FormAction  string
	IsEdit      bool
	ReadingDate string
	// Monat is the Abrechnungsmonat field's value ("YYYY-MM", <input
	// type="month">'s format), separate from ReadingDate - vorbelegt aus
	// dem Ablesedatum in create mode, aus der Ablesung's eigenem Wert in
	// edit mode (Issue #86).
	Monat               string
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
	PreviousEinspeisungPreis  float64
	PreviousPersonen          map[int64]int64
	// PreviousHeizungGewichtung always has a valid value (defaulting to
	// 0.7, Ticket #27's default) since the radio group needs exactly one
	// option checked - unlike the blank-when-absent price fields above,
	// this can't just be left empty.
	PreviousHeizungGewichtung float64

	// NoPeriods gates the CSV-Import button (Ticket #54) - only offered as
	// a bootstrap path into a genuinely empty database, never set in edit
	// mode (handleEditWizardForm leaves it at its zero value, false).
	NoPeriods bool
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
			Aktuell:                   "ablesungen",
			FormAction:                requestBase(r) + "/ablesungen",
			ReadingDate:               time.Now().Format("2006-01-02"),
			Monat:                     time.Now().Format("2006-01"),
			Apartments:                apartments,
			PreviousHeizungGewichtung: 0.7,
			NoPeriods:                 previousPeriod == nil,
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
			data.PreviousEinspeisungPreis = previousPeriod.EinspeisungPreis
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
			Aktuell:                   "ablesungen",
			FormAction:                fmt.Sprintf("%s/ablesungen/%d", requestBase(r), target.ID),
			IsEdit:                    true,
			ReadingDate:               target.ReadingDate,
			Monat:                     string(monatInputFromStored(target.Monat)),
			Apartments:                apartments,
			EditReadings:              target.Readings,
			PreviousStrompreis:        target.Strompreis,
			PreviousFrischwasserPreis: target.FrischwasserPreis,
			PreviousAbwasserPreis:     target.AbwasserPreis,
			PreviousEinspeisungPreis:  target.EinspeisungPreis,
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
// parsePeriodInput parses an Ablesung form (shared by handleCreateAblesung
// and handleUpdateAblesung - Ticket #34, same fields either way, only what
// happens with the result differs).
// monatInput is an <input type="month">'s value ("YYYY-MM") - the wizard-
// facing Abrechnungsmonat format, distinct from periods.monat's persisted
// "YYYY-MM-01" (Issue #86 code review: this was scattered as ad-hoc string
// slicing/concatenation across parsePeriodInput and the edit form).
type monatInput string

// monatInputFromStored converts periods.monat ("YYYY-MM-01") to its
// <input type="month"> value ("YYYY-MM"). Returns "" if stored is too
// short to safely take the first 7 characters from - CreatePeriod never
// validates Monat (only UpdatePeriod does, see checkMonatNeighbors), so a
// malformed value can in principle reach the edit form; a blank field beats
// a panic.
func monatInputFromStored(stored string) monatInput {
	if len(stored) < 7 {
		return ""
	}
	return monatInput(stored[:7])
}

// toStored converts the wizard field's value back to periods.monat's
// format, appending "-01" to a bare "YYYY-MM" if needed. Already-
// normalized input passes through unchanged.
func (m monatInput) toStored() string {
	if len(m) == 7 {
		return string(m) + "-01"
	}
	return string(m)
}

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
	einspeisungPreis, err4 := strconv.ParseFloat(r.FormValue("einspeisung_preis"), 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return store.PeriodInput{}, fmt.Errorf("invalid price value")
	}

	heizungGewichtung, err := parseHeizungGewichtung(r.FormValue("heizung_gewichtung"))
	if err != nil {
		return store.PeriodInput{}, err
	}

	personen := make(map[int64]int64, len(apartments))
	for _, a := range apartments {
		idStr := strconv.FormatInt(a.ID, 10)
		p, err := strconv.ParseInt(r.FormValue("personen_"+idStr), 10, 64)
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("invalid Personenzahl for apartment %s", idStr)
		}
		personen[a.ID] = p
	}

	return store.PeriodInput{
		ReadingDate:             r.FormValue("reading_date"),
		Monat:                   monatInput(r.FormValue("monat")).toStored(),
		Strompreis:              strompreis,
		FrischwasserPreis:       frischwasserPreis,
		AbwasserPreis:           abwasserPreis,
		HeizungWaermeGewichtung: heizungGewichtung,
		EinspeisungPreis:        einspeisungPreis,
		Readings:                readings,
		Personen:                personen,
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

// handleUpdateAblesung corrects an existing Ablesung in place (Ticket #34,
// generalized to any period by Ticket #44 - no restriction to the latest
// one anymore, see store.UpdatePeriod). The neighbor-date reorder guard
// lives in store.UpdatePeriod itself; this handler only translates its
// typed errors into the German user-facing messages.
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

		if err := store.UpdatePeriod(db, periodID, in); err != nil {
			var tooEarly *store.PeriodDateTooEarlyError
			var tooLate *store.PeriodDateTooLateError
			var monatTooEarly *store.PeriodMonatTooEarlyError
			var monatTooLate *store.PeriodMonatTooLateError
			switch {
			case errors.As(err, &tooEarly):
				http.Error(w, fmt.Sprintf("Ablesedatum muss nach der Vorperiode (%s) liegen", tooEarly.Neighbor), http.StatusBadRequest)
			case errors.As(err, &tooLate):
				http.Error(w, fmt.Sprintf("Ablesedatum muss vor der Folgeperiode (%s) liegen", tooLate.Neighbor), http.StatusBadRequest)
			case errors.As(err, &monatTooEarly):
				http.Error(w, fmt.Sprintf("Abrechnungsmonat darf nicht vor dem der Vorperiode (%s) liegen", monatTooEarly.Neighbor), http.StatusBadRequest)
			case errors.As(err, &monatTooLate):
				http.Error(w, fmt.Sprintf("Abrechnungsmonat darf nicht nach dem der Folgeperiode (%s) liegen", monatTooLate.Neighbor), http.StatusBadRequest)
			default:
				http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			}
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
// periodListItem is one period's Ablesedatum together with its Zeitraum,
// combined into a single label (the gap to the chronologically previous
// period, "keine Vorperiode" for the oldest) - the Ablesung-Detail "andere
// Ablesung anzeigen" dropdown, where a single-line option leaves no room
// for 2 columns.
type periodListItem struct {
	ID    int64
	Label string
}

// periodListItems builds one periodListItem per period. periods must be in
// store.AllPeriods' own order (newest first) - the predecessor of
// periods[i] is then periods[i+1], the next-older one.
func periodListItems(periods []store.PeriodSummary) []periodListItem {
	out := make([]periodListItem, len(periods))
	for i, p := range periods {
		var label string
		if i+1 < len(periods) {
			label = fmt.Sprintf("%s (%s–%s)", formatDatumDE(p.ReadingDate), formatDatumDE(periods[i+1].ReadingDate), formatDatumDE(p.ReadingDate))
		} else {
			label = fmt.Sprintf("%s (keine Vorperiode)", formatDatumDE(p.ReadingDate))
		}
		out[i] = periodListItem{ID: p.ID, Label: label}
	}
	return out
}

// periodOverviewRow is one period's Ablesedatum and Zeitraum as 2 separate
// values - the Ablesungen-Übersicht's table, which has room for its own
// column per field (unlike the dropdown's single-line option).
type periodOverviewRow struct {
	ID          int64
	ReadingDate string
	Zeitraum    string
}

// periodMonatGroup is every Ablesung of one Abrechnungsmonat (Issue #86),
// newest first - the Ablesungen-Übersicht's rowspan-Spalte (Ticket #83
// Variante B): a Monat with 1 Ablesung renders a single row, a Monat with
// several (untermonatige Ablesungen) spans the group under one Monat-cell.
type periodMonatGroup struct {
	MonatLabel string
	Rows       []periodOverviewRow
}

// periodOverviewGroups builds one periodMonatGroup per distinct Monat,
// newest first, same order/predecessor rule as periodListItems for each
// row's Zeitraum. periods must already be newest-first (store.AllPeriods'
// own order).
func periodOverviewGroups(periods []store.PeriodSummary) []periodMonatGroup {
	var out []periodMonatGroup
	var currentMonat string
	for i, p := range periods {
		var zeitraum string
		if i+1 < len(periods) {
			zeitraum = fmt.Sprintf("%s–%s", formatDatumDE(periods[i+1].ReadingDate), formatDatumDE(p.ReadingDate))
		} else {
			zeitraum = "keine Vorperiode"
		}
		row := periodOverviewRow{ID: p.ID, ReadingDate: formatDatumDE(p.ReadingDate), Zeitraum: zeitraum}

		if len(out) > 0 && p.Monat == currentMonat {
			out[len(out)-1].Rows = append(out[len(out)-1].Rows, row)
			continue
		}
		currentMonat = p.Monat
		out = append(out, periodMonatGroup{MonatLabel: germanPeriodLabel(p.Monat), Rows: []periodOverviewRow{row}})
	}
	return out
}

// handleAblesungenListe lists every recorded period (Ticket #43), newest
// first, linking each to its detail view. ImportedCount/Warnings surface the
// CSV import's result (Ticket #54) - passed via query params since the app
// has no session/flash mechanism.
func handleAblesungenListe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periods, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		importedCount, _ := strconv.Atoi(r.URL.Query().Get("imported"))

		data := struct {
			Base          string
			Aktuell       string
			MonatGruppen  []periodMonatGroup
			ImportedCount int
			Warnings      []string
		}{
			Base:          requestBase(r),
			Aktuell:       "ablesungen",
			MonatGruppen:  periodOverviewGroups(periods),
			ImportedCount: importedCount,
			Warnings:      r.URL.Query()["warning"],
		}

		if err := ablesungenTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// formatMeterDiff renders one Zähler's absolute change since the previous
// Ablesung (Ticket #73), "+"-prefixed when it rose (the normal case - a
// Zählerstand only falls after a meter swap/correction, where the bare
// minus sign from formatDecimalDE2 already reads correctly), padded to 2
// Nachkommastellen (Ticket #76) like every other displayed Verbrauchswert.
func formatMeterDiff(current, previous float64) string {
	diff := current - previous
	sign := ""
	if diff > 0 {
		sign = "+"
	}
	return sign + formatDecimalDE2(diff)
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

		// ZeitraumStart is the immediately preceding period's ReadingDate -
		// the values shown here are the *consumption over that interval*,
		// not just a single point-in-time reading, so the detail view
		// spells out the whole span, not only its end date.
		var zeitraumStart string
		vorperiode, err := store.PeriodReadingsBefore(db, period.ID, 1)
		if err != nil {
			http.Error(w, "vorperiode: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(vorperiode) > 0 {
			zeitraumStart = vorperiode[0].ReadingDate
		}

		// Diff is each Zähler's absolute change since the previous Ablesung
		// ("+23,00" / "-5,00"), formatted ready-to-print - empty for the
		// oldest period (no Vorperiode to diff against).
		var meters []struct {
			Label string
			Value float64
			Unit  string
			Diff  string
		}
		for _, m := range meterDisplays {
			entry := struct {
				Label string
				Value float64
				Unit  string
				Diff  string
			}{Label: m.Label, Value: period.Readings[m.Key], Unit: m.Unit}
			if len(vorperiode) > 0 {
				entry.Diff = formatMeterDiff(period.Readings[m.Key], vorperiode[0].Readings[m.Key])
			}
			meters = append(meters, entry)
		}

		k, err := berechneKosten(db, period.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			Base          string
			Aktuell       string
			Period        *store.LatestPeriod
			AllPeriods    []periodListItem
			Apartments    []store.Apartment
			Personen      map[int64]int64
			ZeitraumStart string
			Meters        []struct {
				Label string
				Value float64
				Unit  string
				Diff  string
			}
			Strom       *calc.StromErgebnis
			Wasser      *calc.WasserErgebnis
			Heizung     *calc.HeizungErgebnis
			Einspeisung *calc.EinspeisungErgebnis
			KostenNote  string
		}{
			Base:          requestBase(r),
			Aktuell:       "ablesungen",
			Period:        period,
			AllPeriods:    periodListItems(allPeriods),
			Apartments:    apartments,
			Personen:      period.PersonenByApartment,
			ZeitraumStart: zeitraumStart,
			Meters:        meters,
			Strom:         k.Strom,
			Wasser:        k.Wasser,
			Heizung:       k.Heizung,
			Einspeisung:   k.Einspeisung,
			KostenNote:    k.KostenNote,
		}

		if err := ablesungTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleBerechnungslogik serves a static, informational explanation of the
// cost formulas (Ticket #33) - no DB access, the content never depends on
// any period's data.
func handleBerechnungslogik() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct{ Base, Aktuell string }{Base: requestBase(r), Aktuell: "berechnungslogik"}
		if err := berechnungslogikTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleStammdatenForm serves the /stammdaten page (Issue #61): each
// apartment's current Wohnungsgröße/Flurstücksgröße, editable as live
// values - not historized per Ablesung like the rest of the monthly form.
func handleStammdatenForm(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		kostenpositionen, err := store.Kostenpositionen(db)
		if err != nil {
			http.Error(w, "kostenpositionen: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jahrBloecke, nextJahr, err := buildStammdatenJahrBloecke(db, kostenpositionen)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			Base         string
			Aktuell      string
			Apartments   []store.Apartment
			LogikOptions []logikOption
			JahrBloecke  []stammdatenJahrBlock
			NextJahr     int
		}{
			Base:         requestBase(r),
			Aktuell:      "stammdaten",
			Apartments:   apartments,
			LogikOptions: logikOptions,
			JahrBloecke:  jahrBloecke,
			NextJahr:     nextJahr,
		}

		if err := stammdatenTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleUpdateStammdaten saves every apartment's Wohnungsgröße/
// Flurstücksgröße from the /stammdaten form.
func handleUpdateStammdaten(db *sql.DB) http.HandlerFunc {
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

		in := make(map[int64]store.StammdatenInput, len(apartments))
		for _, a := range apartments {
			idStr := strconv.FormatInt(a.ID, 10)
			qm, err := parseFormFloat(r, "qm_"+idStr, "Wohnungsgröße", idStr)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			flurstueckGroesse, err := parseFormFloat(r, "flurstueck_groesse_"+idStr, "Flurstücksgröße", idStr)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			in[a.ID] = store.StammdatenInput{QM: qm, FlurstueckGroesse: flurstueckGroesse}
		}

		if err := store.UpdateStammdaten(db, in); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/stammdaten", http.StatusFound)
	}
}
