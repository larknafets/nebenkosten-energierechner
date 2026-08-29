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

		heizungGewichtung, err := parseHeizungGewichtung(r.FormValue("heizung_gewichtung"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
			ReadingDate:             r.FormValue("reading_date"),
			Strompreis:              strompreis,
			FrischwasserPreis:       frischwasserPreis,
			AbwasserPreis:           abwasserPreis,
			HeizungWaermeGewichtung: heizungGewichtung,
			Readings:                readings,
			Personen:                personen,
			QM:                      qm,
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
// verlaufMonate).
type verlaufSegment struct {
	Farbe                 string
	Kosten                float64
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
			segmente = append(segmente, verlaufSegment{Farbe: kat.Farbe, Kosten: kat.Kosten, ProzentNeuestesGesamt: pct})
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
			Period         *store.LatestPeriod
			PeriodLabel    string
			Cards          []dashboardCard
			KostenNote     string
			HasVerlauf     bool
			VerlaufSpalten []verlaufColumn
		}{
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
