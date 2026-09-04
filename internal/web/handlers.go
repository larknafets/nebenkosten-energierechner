// Package web serves the Nebenkostenrechner's HTML wizard and
// read views. Server-rendered html/template, no SPA build step - see
// https://github.com/larknafets/nebenkostenrechner/issues/4.
package web

import (
	"bufio"
	"database/sql"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
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

// groupThousandsDE inserts "." as the German thousands separator into a
// German-formatted decimal string's integer part (comma already the
// decimal separator, e.g. "2345,43" -> "2.345,43"; "-12345" -> "-12.345").
// Shared by every formatDecimalDE*/formatEuroDE variant below.
func groupThousandsDE(s string) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, dec, hasDec := strings.Cut(s, ",")

	var grouped strings.Builder
	n := len(intPart)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteByte(intPart[i])
	}

	out := grouped.String()
	if hasDec {
		out += "," + dec
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatDecimalDE renders a float64 the way germanPeriodLabel renders a
// date: German convention, decimal comma instead of point (Ticket #36),
// rounded to at most 2 decimal places (Ticket #37 - raw calc.go values
// like WWAnteil/WaermeMWh carry floating-point noise past 2 places, e.g.
// "25,333333333333332"). Not padded - 'f'/-1 on the rounded value keeps
// "0,7" as "0,7", not "0,70"; use formatEuroDE where padding to exactly 2
// places is wanted (EUR amounts). Thousands grouped with "." (e.g.
// "2.345,43").
func formatDecimalDE(x float64) string {
	rounded := math.Round(x*100) / 100
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(rounded, 'f', -1, 64), ".", ","))
}

// formatEuroDE renders a float64 as a German-formatted EUR amount, always
// padded to exactly 2 decimal places (Ticket #40 - a currency amount reads
// as "45,00", not "45"). EUR amounts are already Round2'd (kaufmännisch,
// Issue #8) before reaching here, so the fixed 2-place formatting doesn't
// change the value, only pads its display. Thousands grouped with "."
// (e.g. "2.345,00").
func formatEuroDE(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 2, 64), ".", ","))
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

// formatDecimalDE0 renders a float64 rounded to a whole number, no decimal
// places at all (preview: Jahressummen-Karte's Wohnungsgröße/
// Flurstücksgröße-Badges). Thousands grouped with "." (e.g. "2.345").
func formatDecimalDE0(x float64) string {
	return groupThousandsDE(strconv.FormatFloat(math.Round(x), 'f', 0, 64))
}

// formatDecimalDE1 renders a float64 German-formatted, always padded to
// exactly 1 decimal place (Ticket #75 - the Personen-Schnitt reads as
// "2,5", not "2" or "2,50"). Thousands grouped with "." (e.g. "2.345,0").
func formatDecimalDE1(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 1, 64), ".", ","))
}

// formatDecimalDE2 renders a float64 German-formatted, always padded to
// exactly 2 decimal places (Ticket #76 - a displayed Verbrauchswert reads as
// "0,70"/"107,00", not "0,7"/"107", analog to formatEuroDE but without a
// currency unit). Thousands grouped with "." (e.g. "2.345,43").
func formatDecimalDE2(x float64) string {
	return groupThousandsDE(strings.ReplaceAll(strconv.FormatFloat(x, 'f', 2, 64), ".", ","))
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

// formatDatumZeitDE renders a build timestamp (RFC3339, as set via -ldflags
// at build time - Ticket #48) in the German DD.MM.YYYY, HH:MM form. Falls
// back to the raw string if it isn't a parseable RFC3339 timestamp.
func formatDatumZeitDE(buildDate string) string {
	t, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		return buildDate
	}
	return t.Local().Format("02.01.2006, 15:04")
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

// csvHeader is the canonical CSV column order for both export (Ticket #53)
// and import (Ticket #54) - reading_date, every meter key (Zählerstände,
// not Verbrauch), the period-level prices/Gewichtung, then Personen per
// apartment (fixed ids 1/2, see store's seed()). No qm_1/qm_2 columns
// (Issue #61 moved Wohnungsgröße off the Ablesung onto /stammdaten - hard
// cut, no backward compatibility with the old format).
var csvHeader = append(append([]string{"reading_date"}, store.MeterKeys...),
	"strompreis", "frischwasser_preis", "abwasser_preis", "heizung_gewichtung", "einspeisung_preis",
	"personen_1", "personen_2",
)

// parseDecimalDE is formatDecimalDE's inverse: German decimal-comma input
// ("25,33") to float64. CSV cells always use this convention (Ticket #54).
func parseDecimalDE(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
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
	Base                string
	Aktuell             string
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

	// Variant is Ticket #83's throwaway UI-prototype switch ("a"/"b"/"c",
	// via ?variant=), picking which Monat-Override mockup renders next to
	// Ablesedatum - PROTOTYPE ONLY, remove with the rest of this branch.
	Variant string
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
			Aktuell:                   "ablesungen",
			FormAction:                requestBase(r) + "/ablesungen",
			ReadingDate:               time.Now().Format("2006-01-02"),
			Apartments:                apartments,
			PreviousHeizungGewichtung: 0.7,
			NoPeriods:                 previousPeriod == nil,
			Variant:                   r.URL.Query().Get("variant"),
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
			switch {
			case errors.As(err, &tooEarly):
				http.Error(w, fmt.Sprintf("Ablesedatum muss nach der Vorperiode (%s) liegen", tooEarly.Neighbor), http.StatusBadRequest)
			case errors.As(err, &tooLate):
				http.Error(w, fmt.Sprintf("Ablesedatum muss vor der Folgeperiode (%s) liegen", tooLate.Neighbor), http.StatusBadRequest)
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
type kosten struct {
	Strom       *calc.StromErgebnis
	Wasser      *calc.WasserErgebnis
	Heizung     *calc.HeizungErgebnis
	Einspeisung *calc.EinspeisungErgebnis
	KostenNote  string
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

	einspeisung, err := calc.Einspeisung(db, periodID)
	if err != nil {
		return kosten{}, fmt.Errorf("einspeisung: %w", err)
	}

	return kosten{Strom: strom, Wasser: wasser, Heizung: heizung, Einspeisung: einspeisung}, nil
}

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

// periodOverviewRows builds one periodOverviewRow per period, same order/
// predecessor rule as periodListItems.
func periodOverviewRows(periods []store.PeriodSummary) []periodOverviewRow {
	out := make([]periodOverviewRow, len(periods))
	for i, p := range periods {
		var zeitraum string
		if i+1 < len(periods) {
			zeitraum = fmt.Sprintf("%s–%s", formatDatumDE(periods[i+1].ReadingDate), formatDatumDE(p.ReadingDate))
		} else {
			zeitraum = "keine Vorperiode"
		}
		out[i] = periodOverviewRow{ID: p.ID, ReadingDate: formatDatumDE(p.ReadingDate), Zeitraum: zeitraum}
	}
	return out
}

// protoRow/protoMonatGroup are Ticket #83's throwaway UI-prototype fixture
// types for the Ablesungen-Übersicht Monat-Gruppierung mockup - the real
// `monat` field doesn't exist in the schema yet (Ticket #81 is spec-only),
// so the variant tables render this hardcoded example instead of live data.
// PROTOTYPE ONLY, remove with the rest of this branch.
type protoRow struct {
	ReadingDate string
	Zeitraum    string
}
type protoMonatGroup struct {
	MonatLabel string
	Rows       []protoRow
	Shaded     bool
}

func protoMonatFixture() []protoMonatGroup {
	return []protoMonatGroup{
		{MonatLabel: "August 2026", Rows: []protoRow{
			{ReadingDate: "31.08.2026", Zeitraum: "01.08.–31.08.2026"},
		}},
		{MonatLabel: "September 2026", Shaded: true, Rows: []protoRow{
			{ReadingDate: "01.09.2026", Zeitraum: "01.09.–01.09.2026"},
			{ReadingDate: "15.09.2026", Zeitraum: "02.09.–15.09.2026"},
		}},
		{MonatLabel: "Oktober 2026", Rows: []protoRow{
			{ReadingDate: "01.10.2026", Zeitraum: "16.09.–01.10.2026"},
		}},
	}
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
		variant := r.URL.Query().Get("variant")

		data := struct {
			Base          string
			Aktuell       string
			Periods       []periodOverviewRow
			ImportedCount int
			Warnings      []string
			Variant       string
			MonatGruppen  []protoMonatGroup
		}{
			Base:          requestBase(r),
			Aktuell:       "ablesungen",
			Periods:       periodOverviewRows(periods),
			ImportedCount: importedCount,
			Warnings:      r.URL.Query()["warning"],
			Variant:       variant,
		}
		if variant != "" {
			data.MonatGruppen = protoMonatFixture()
		}

		if err := ablesungenTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleExportCSV streams every Ablesung as CSV (Ticket #53) - Excel-DE
// dialect (Semikolon, Komma-Dezimal, UTF-8 mit BOM), same csvHeader the
// import (Ticket #54) reads back.
func handleExportCSV(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		details, err := store.AllPeriodDetails(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="ablesungen.csv"`)
		w.Write([]byte{0xEF, 0xBB, 0xBF})

		cw := csv.NewWriter(w)
		cw.Comma = ';'
		if err := cw.Write(csvHeader); err != nil {
			return
		}
		for _, p := range details {
			row := make([]string, 0, len(csvHeader))
			row = append(row, p.ReadingDate)
			for _, key := range store.MeterKeys {
				row = append(row, formatDecimalDE(p.Readings[key]))
			}
			row = append(row,
				formatDecimalDE(p.Strompreis),
				formatDecimalDE(p.FrischwasserPreis),
				formatDecimalDE(p.AbwasserPreis),
				formatDecimalDE(p.HeizungWaermeGewichtung),
				formatDecimalDE(p.EinspeisungPreis),
				formatDecimalDE(float64(p.PersonenByApartment[1])),
				formatDecimalDE(float64(p.PersonenByApartment[2])),
			)
			if err := cw.Write(row); err != nil {
				return
			}
		}
		cw.Flush()
	}
}

// importMaxBytes caps the CSV upload (Ticket #54) - single-user app, no
// real threat model, just a guard against an accidental huge file.
const importMaxBytes = 2 << 20 // 2 MiB

// importRow pairs a parsed PeriodInput with its original CSV line number,
// so warnings can still point at the uploaded file after the rows are
// re-sorted into chronological order.
type importRow struct {
	input store.PeriodInput
	line  int
}

// handleImportCSV bootstraps a completely empty database from a CSV in the
// csvHeader format (Ticket #54) - rejected if any Ablesung already exists,
// even though the form button is already hidden in that case (defense in
// depth). A hard error in any row aborts the whole import (alles oder
// nichts); negative-Verbrauch/Ausreißer warnings never block, just get
// reported afterwards on the Ablesungen-Übersicht.
func handleImportCSV(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, err := store.AllPeriods(db)
		if err != nil {
			http.Error(w, "periods: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(existing) > 0 {
			http.Error(w, "Import nur möglich, solange noch keine Ablesung existiert", http.StatusBadRequest)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
		if err := r.ParseMultipartForm(importMaxBytes); err != nil {
			http.Error(w, "Datei zu groß oder ungültig (Limit 2 MB): "+err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("csv")
		if err != nil {
			http.Error(w, "keine CSV-Datei hochgeladen", http.StatusBadRequest)
			return
		}
		defer file.Close()

		rows, err := parseImportCSV(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sort.Slice(rows, func(i, j int) bool { return rows[i].input.ReadingDate < rows[j].input.ReadingDate })

		inputs := make([]store.PeriodInput, len(rows))
		for i, row := range rows {
			inputs[i] = row.input
		}

		ids, err := store.ImportPeriods(db, inputs)
		if err != nil {
			http.Error(w, "import: "+err.Error(), http.StatusInternalServerError)
			return
		}

		warnings := importWarnings(rows, ids)
		redirectURL := fmt.Sprintf("%s/ablesungen?imported=%d", requestBase(r), len(ids))
		for _, msg := range warnings {
			redirectURL += "&warning=" + url.QueryEscape(msg)
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// parseImportCSV reads csvHeader-formatted CSV (Semikolon, Komma-Dezimal,
// optionales UTF-8-BOM) and validates every row into a PeriodInput. Returns
// the first hard error encountered (missing/kaputte Werte, ungültiges
// Datum/Heizungs-Gewichtung) - the caller aborts the whole import on any
// error, so there's no point collecting more than one.
func parseImportCSV(file io.Reader) ([]importRow, error) {
	reader := bufio.NewReader(file)
	if bom, err := reader.Peek(3); err == nil && bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		reader.Discard(3)
	}

	cr := csv.NewReader(reader)
	cr.Comma = ';'

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("CSV: Kopfzeile konnte nicht gelesen werden: %w", err)
	}
	colIdx := make(map[string]int, len(header))
	for i, name := range header {
		colIdx[strings.TrimSpace(name)] = i
	}
	for _, want := range csvHeader {
		if _, ok := colIdx[want]; !ok {
			return nil, fmt.Errorf("CSV: Spalte %q fehlt", want)
		}
	}

	var rows []importRow
	line := 1
	for {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("Zeile %d: %v", line, err)
		}

		in, err := parseImportRow(record, colIdx, line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, importRow{input: in, line: line})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("CSV enthält keine Ablesungen")
	}
	return rows, nil
}

func parseImportRow(record []string, colIdx map[string]int, line int) (store.PeriodInput, error) {
	cell := func(col string) string { return record[colIdx[col]] }

	readingDate := strings.TrimSpace(cell("reading_date"))
	if _, err := time.Parse("2006-01-02", readingDate); err != nil {
		return store.PeriodInput{}, fmt.Errorf("Zeile %d: ungültiges Ablesedatum %q (Format JJJJ-MM-TT)", line, readingDate)
	}

	readings := make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		v, err := parseDecimalDE(cell(key))
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("Zeile %d: ungültiger Wert für %s: %q", line, key, cell(key))
		}
		readings[key] = v
	}

	strompreis, err1 := parseDecimalDE(cell("strompreis"))
	frischwasserPreis, err2 := parseDecimalDE(cell("frischwasser_preis"))
	abwasserPreis, err3 := parseDecimalDE(cell("abwasser_preis"))
	einspeisungPreis, err4 := parseDecimalDE(cell("einspeisung_preis"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return store.PeriodInput{}, fmt.Errorf("Zeile %d: ungültiger Preiswert", line)
	}

	heizungGewichtung, err := parseHeizungGewichtung(strings.ReplaceAll(cell("heizung_gewichtung"), ",", "."))
	if err != nil {
		return store.PeriodInput{}, fmt.Errorf("Zeile %d: %v", line, err)
	}

	personen := make(map[int64]int64, 2)
	for _, id := range [2]int64{1, 2} {
		personenCol := fmt.Sprintf("personen_%d", id)
		p, err := parseDecimalDE(cell(personenCol))
		if err != nil {
			return store.PeriodInput{}, fmt.Errorf("Zeile %d: ungültige Personenzahl für Wohnung %d: %q", line, id, cell(personenCol))
		}
		personen[id] = int64(p)
	}

	return store.PeriodInput{
		ReadingDate:             readingDate,
		Strompreis:              strompreis,
		FrischwasserPreis:       frischwasserPreis,
		AbwasserPreis:           abwasserPreis,
		HeizungWaermeGewichtung: heizungGewichtung,
		EinspeisungPreis:        einspeisungPreis,
		Readings:                readings,
		Personen:                personen,
	}, nil
}

// importWarnings reproduces the wizard's client-side negative-Verbrauch/
// Ausreißer checks server-side (Ticket #54) - the bulk import has no
// per-field JS to run them, but a bulk import of historical data is exactly
// where a typo is easiest to miss. rows must already be chronologically
// sorted, ids in the same order (ImportPeriods preserves it).
func importWarnings(rows []importRow, ids []int64) []string {
	var warnings []string
	var history []store.PeriodReadings // newest-first, capped at 4

	for i, row := range rows {
		if i > 0 {
			prev := history[0]
			for _, key := range store.MeterKeys {
				newVal, prevVal := row.input.Readings[key], prev.Readings[key]
				if newVal < prevVal {
					warnings = append(warnings, fmt.Sprintf("Zeile %d (%s): negativer Verbrauch bei %s (%s < Vorstand %s)",
						row.line, row.input.ReadingDate, key, formatDecimalDE(newVal), formatDecimalDE(prevVal)))
				}
			}
			if avg, ok := outlierAvg(history); ok {
				for _, key := range store.MeterKeys {
					a := avg[key]
					if a == 0 {
						continue
					}
					consumption := row.input.Readings[key] - prev.Readings[key]
					if math.Abs(consumption-a) > 0.5*math.Abs(a) {
						warnings = append(warnings, fmt.Sprintf("Zeile %d (%s): Ausreißer bei %s (Verbrauch %s weicht >50%% vom Schnitt der letzten 3 Ablesungen %s ab)",
							row.line, row.input.ReadingDate, key, formatDecimalDE(consumption), formatDecimalDE(a)))
					}
				}
			}
		}

		history = append([]store.PeriodReadings{{ID: ids[i], ReadingDate: row.input.ReadingDate, Readings: row.input.Readings}}, history...)
		if len(history) > 4 {
			history = history[:4]
		}
	}
	return warnings
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

// kategorie is one cost position (Strom, Heizung, or Wasser), together with
// its share of that apartment's total (ProzentGesamt, the bar segment's
// width) and the raw consumption shown in brackets next to the EUR amount
// (Ticket #18).
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

	// Verbrauch2/Einheit2 is a second, optional Mengenangabe shown behind
	// Verbrauch/Einheit in der Verbrauchswerte-Ansicht - nur für Heizung/
	// Warmwasser gesetzt: Verbrauch ist dort der tatsächliche (rohe, ohne
	// PV-Abzug) WP-Strom in kWh, Verbrauch2 der rohe Wärmemengenzähler-
	// Verbrauch (MWh, reine Raumheizung) zum Vergleich. Einheit2 == "" heißt:
	// kein zweiter Wert.
	Verbrauch2 float64
	Einheit2   string
}

// kategorien builds the given apartment's cost breakdown for the period.
// Wohnung 1's Strom has no own cost position - its Netzbezug stays implicit
// (see calc.Strom) - so only Wohnung 2 gets a Strom-Kategorie. Frischwasser
// and Abwasser are combined into a single Wasser-Kategorie since they share
// one raw m³ consumption (no separate Abwasserzähler, see calc.Wasser).
func kategorien(apartmentID int64, k kosten) []kategorie {
	var list []kategorie
	if apartmentID == 2 {
		list = append(list, kategorie{Label: "Strom", Kosten: k.Strom.KostenW2, Verbrauch: k.Strom.W2VerbrauchKWh, Einheit: "kWh", Farbe: "strom"})
	}

	heizungKosten, wpVerbrauchKWh, waermeMWh := k.Heizung.KostenHeizungW1, k.Heizung.WPVerbrauchW1KWh, k.Heizung.WaermeW1MWh
	frischwasserKosten, abwasserKosten, wasserM3 := k.Wasser.KostenFrischwasserW1, k.Wasser.KostenAbwasserW1, k.Wasser.FrischwasserW1
	if apartmentID == 2 {
		heizungKosten, wpVerbrauchKWh, waermeMWh = k.Heizung.KostenHeizungW2, k.Heizung.WPVerbrauchW2KWh, k.Heizung.WaermeW2MWh
		frischwasserKosten, abwasserKosten, wasserM3 = k.Wasser.KostenFrischwasserW2, k.Wasser.KostenAbwasserW2, k.Wasser.FrischwasserW2
	}
	list = append(list,
		kategorie{Label: "Heizung/Warmwasser", Kosten: heizungKosten, Verbrauch: wpVerbrauchKWh, Einheit: "kWh", Farbe: "heizung", Verbrauch2: waermeMWh, Einheit2: "MWh"},
		kategorie{Label: "Wasser", Kosten: calc.Round2(frischwasserKosten + abwasserKosten), Verbrauch: wasserM3, Einheit: "m³", Farbe: "wasser"},
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

// periodKosten is one period's already-computed kosten, for the
// Jahressummen-Karten and Monatsverlauf. ReadingDate stays in its raw
// "YYYY-MM-DD" form (not pre-formatted) since downstream needs it for both
// the month label and calendar-year grouping. Personen is this period's
// Ablesung-Personenzahl je Wohnung (Ticket #75's Personen-Schnitt averages
// this across a Jahr's Perioden).
type periodKosten struct {
	ReadingDate string
	K           kosten
	Personen    map[int64]int64
}

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
		periodenKosten = append(periodenKosten, periodKosten{ReadingDate: p.ReadingDate, K: pk, Personen: personen})
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
		}

		if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

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
