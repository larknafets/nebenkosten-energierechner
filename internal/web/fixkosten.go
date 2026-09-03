package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/calc"
	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// logikLabels renders a Kostenposition's Logik as the German label shown
// throughout the Fixkosten/Stammdaten UI - shared by the form, detail, and
// Stammdaten Kostenpositionen-Jahre templates.
var logikLabels = map[string]string{
	store.LogikWohneinheit: "Je Wohneinheit",
	store.LogikFlurstueck:  "Je anteiliges Flurstück",
	store.LogikQM:          "Je anteilige Wohnungsgröße",
	store.LogikPersonen:    "Je Anzahl Personen",
}

// logikOption is one <select> choice for a Kostenposition's Logik.
type logikOption struct{ Value, Label string }

// logikOptions is logikLabels in a stable, display order - the Stammdaten
// Kostenpositionen-Jahre Logik-Dropdown's option list.
var logikOptions = []logikOption{
	{store.LogikWohneinheit, logikLabels[store.LogikWohneinheit]},
	{store.LogikFlurstueck, logikLabels[store.LogikFlurstueck]},
	{store.LogikQM, logikLabels[store.LogikQM]},
	{store.LogikPersonen, logikLabels[store.LogikPersonen]},
}

// parseFixkostenMonat turns the form's <input type="month"> value ("YYYY-MM")
// into the stored "YYYY-MM-01" convention (same day-1 convention as
// PeriodInput.ReadingDate, minus the day-of-month the user never picks).
func parseFixkostenMonat(raw string) (string, error) {
	t, err := time.Parse("2006-01", raw)
	if err != nil {
		return "", fmt.Errorf("ungültiger Monat %q", raw)
	}
	return t.Format("2006-01") + "-01", nil
}

// monatForInput is parseFixkostenMonat's inverse, for prefilling the
// <input type="month"> value from a stored "YYYY-MM-01" Monat.
func monatForInput(monat string) string {
	t, err := time.Parse("2006-01-02", monat)
	if err != nil {
		return ""
	}
	return t.Format("2006-01")
}

// fixkostenPositionRow is one Kostenposition's row on the Fixkosten-Formular
// - jährlich Positionen are read-only (Monatswert computed from Stammdaten),
// monatlich Positionen are editable, prefilled from Value.
type fixkostenPositionRow struct {
	ID          int64
	Label       string
	LogikLabel  string
	IsJaehrlich bool
	Value       float64
}

// fixkostenFormData is the Fixkosten-Formular's template data - shared by
// "neu" (prefilled from the latest Fixkosten-Eingabe) and "bearbeiten"
// (prefilled with the Eingabe's own current values), same split as
// wizardData for Ablesungen.
type fixkostenFormData struct {
	Base             string
	FormAction       string
	IsEdit           bool
	Monat            string // "YYYY-MM", <input type="month"> value
	Apartments       []store.Apartment
	PreviousPersonen map[int64]int64
	Positionen       []fixkostenPositionRow
	JahrNotAngelegt  bool
	Jahr             int
}

// buildFixkostenPositionRows assembles one row per Kostenposition for the
// given Jahr, using values (explizite Werte, keyed by kostenposition id) to
// prefill monatlich rows - the latest Eingabe's Werte in "neu" mode, the
// Eingabe's own Werte in "bearbeiten" mode.
func buildFixkostenPositionRows(kostenpositionen []store.Kostenposition, jahresdaten map[int64]store.KostenpositionJahr, values map[int64]float64) []fixkostenPositionRow {
	rows := make([]fixkostenPositionRow, 0, len(kostenpositionen))
	for _, kp := range kostenpositionen {
		kj, ok := jahresdaten[kp.ID]
		if !ok {
			continue
		}
		row := fixkostenPositionRow{
			ID:          kp.ID,
			Label:       kp.Label,
			LogikLabel:  logikLabels[kj.Logik],
			IsJaehrlich: kj.Typ == store.TypJaehrlich,
		}
		if row.IsJaehrlich {
			row.Value = kj.Jahreswert / 12
		} else {
			row.Value = values[kp.ID]
		}
		rows = append(rows, row)
	}
	return rows
}

// handleFixkostenForm serves the "neu" Fixkosten-Formular, prefilled from
// the latest Fixkosten-Eingabe (Issue #60 Story 2/9) - today's month as the
// default Monat, same convention as the Ablesung-Wizard's ReadingDate
// default.
func handleFixkostenForm(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		latest, err := store.GetLatestFixkostenEingabe(db)
		if err != nil {
			http.Error(w, "latest fixkosten eingabe: "+err.Error(), http.StatusInternalServerError)
			return
		}

		monat := time.Now().Format("2006-01")
		jahr := time.Now().Year()

		kostenpositionen, err := store.Kostenpositionen(db)
		if err != nil {
			http.Error(w, "kostenpositionen: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jahresdaten, err := store.KostenpositionenJahr(db, jahr)
		if err != nil {
			http.Error(w, "kostenpositionen jahresdaten: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := fixkostenFormData{
			Base:            requestBase(r),
			FormAction:      requestBase(r) + "/fixkosten",
			Monat:           monat,
			Apartments:      apartments,
			JahrNotAngelegt: len(jahresdaten) == 0,
			Jahr:            jahr,
		}
		if latest != nil {
			data.PreviousPersonen = latest.Personen
			data.Positionen = buildFixkostenPositionRows(kostenpositionen, jahresdaten, latest.Werte)
		} else {
			data.Positionen = buildFixkostenPositionRows(kostenpositionen, jahresdaten, nil)
		}

		if err := fixkostenFormTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleFixkostenEditForm serves the "bearbeiten" Fixkosten-Formular for an
// existing Eingabe, prefilled with its own current values.
func handleFixkostenEditForm(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eingabeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid fixkosten eingabe id", http.StatusBadRequest)
			return
		}

		target, err := store.GetFixkostenEingabeDetails(db, eingabeID)
		if err != nil {
			http.Error(w, "fixkosten eingabe: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if target == nil {
			http.NotFound(w, r)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		jahr, err := store.JahrFromMonat(target.Monat)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		kostenpositionen, err := store.Kostenpositionen(db)
		if err != nil {
			http.Error(w, "kostenpositionen: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jahresdaten, err := store.KostenpositionenJahr(db, jahr)
		if err != nil {
			http.Error(w, "kostenpositionen jahresdaten: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data := fixkostenFormData{
			Base:             requestBase(r),
			FormAction:       fmt.Sprintf("%s/fixkosten/%d", requestBase(r), target.ID),
			IsEdit:           true,
			Monat:            monatForInput(target.Monat),
			Apartments:       apartments,
			PreviousPersonen: target.Personen,
			Positionen:       buildFixkostenPositionRows(kostenpositionen, jahresdaten, target.Werte),
			JahrNotAngelegt:  len(jahresdaten) == 0,
			Jahr:             jahr,
		}

		if err := fixkostenFormTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// parseFixkostenInput parses a Fixkosten-Formular (shared by
// handleCreateFixkosten and handleUpdateFixkosten). Werte are only read for
// monatlich-typed Kostenpositionen in the Monat's Jahr - jährlich rows are
// disabled inputs and never submitted, and re-deriving Typ from the DB
// (rather than trusting the submitted form) keeps a stale/tampered form
// from writing a Wert for a jährlich position.
func parseFixkostenInput(r *http.Request, db *sql.DB, apartments []store.Apartment) (store.FixkostenInput, error) {
	monat, err := parseFixkostenMonat(r.FormValue("monat"))
	if err != nil {
		return store.FixkostenInput{}, err
	}

	jahr, err := store.JahrFromMonat(monat)
	if err != nil {
		return store.FixkostenInput{}, err
	}
	jahresdaten, err := store.KostenpositionenJahr(db, jahr)
	if err != nil {
		return store.FixkostenInput{}, fmt.Errorf("kostenpositionen jahresdaten: %w", err)
	}
	if len(jahresdaten) == 0 {
		return store.FixkostenInput{}, fmt.Errorf("für Jahr %d sind noch keine Kostenpositionen in den Stammdaten angelegt", jahr)
	}
	kostenpositionen, err := store.Kostenpositionen(db)
	if err != nil {
		return store.FixkostenInput{}, fmt.Errorf("kostenpositionen: %w", err)
	}

	werte := make(map[int64]float64, len(kostenpositionen))
	for _, kp := range kostenpositionen {
		kj, ok := jahresdaten[kp.ID]
		if !ok || kj.Typ != store.TypMonatlich {
			continue
		}
		v, err := strconv.ParseFloat(r.FormValue(fmt.Sprintf("wert_%d", kp.ID)), 64)
		if err != nil {
			return store.FixkostenInput{}, fmt.Errorf("ungültiger Wert für %s", kp.Label)
		}
		werte[kp.ID] = v
	}

	personen := make(map[int64]int64, len(apartments))
	for _, a := range apartments {
		p, err := strconv.ParseInt(r.FormValue("personen_"+strconv.FormatInt(a.ID, 10)), 10, 64)
		if err != nil {
			return store.FixkostenInput{}, fmt.Errorf("invalid Personenzahl for apartment %d", a.ID)
		}
		personen[a.ID] = p
	}

	return store.FixkostenInput{Monat: monat, Personen: personen, Werte: werte}, nil
}

func handleCreateFixkosten(db *sql.DB) http.HandlerFunc {
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

		in, err := parseFixkostenInput(r, db, apartments)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		eingabeID, err := store.CreateFixkostenEingabe(db, in)
		if err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("%s/fixkosten/%d", requestBase(r), eingabeID), http.StatusFound)
	}
}

func handleUpdateFixkosten(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eingabeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid fixkosten eingabe id", http.StatusBadRequest)
			return
		}

		existing, err := store.GetFixkostenEingabeDetails(db, eingabeID)
		if err != nil {
			http.Error(w, "fixkosten eingabe: "+err.Error(), http.StatusInternalServerError)
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

		in, err := parseFixkostenInput(r, db, apartments)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := store.UpdateFixkostenEingabe(db, eingabeID, in); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("%s/fixkosten/%d", requestBase(r), eingabeID), http.StatusFound)
	}
}

func handleDeleteFixkosten(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eingabeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid fixkosten eingabe id", http.StatusBadRequest)
			return
		}

		if err := store.DeleteFixkostenEingabe(db, eingabeID); err != nil {
			if errors.Is(err, store.ErrFixkostenEingabeNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/fixkosten", http.StatusFound)
	}
}

// fixkostenListItem is one Fixkosten-Eingabe row in the /fixkosten
// Übersicht and the detail view's "andere Eingabe anzeigen" dropdown.
type fixkostenListItem struct {
	ID       int64
	Label    string
	SummeW1  float64
	SummeW2  float64
	HasSumme bool // false when the Eingabe's Jahr has no Kostenpositionen (ErrNoKostenpositionenJahr)
}

func handleFixkostenListe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eingaben, err := store.AllFixkostenEingaben(db)
		if err != nil {
			http.Error(w, "fixkosten eingaben: "+err.Error(), http.StatusInternalServerError)
			return
		}

		items := make([]fixkostenListItem, 0, len(eingaben))
		for _, e := range eingaben {
			item := fixkostenListItem{ID: e.ID, Label: germanPeriodLabel(e.Monat)}
			erg, err := calc.Fixkosten(db, e.ID)
			if err != nil {
				if !errors.Is(err, store.ErrNoKostenpositionenJahr) {
					http.Error(w, "fixkosten: "+err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				item.HasSumme = true
				item.SummeW1 = erg.KostenW1
				item.SummeW2 = erg.KostenW2
			}
			items = append(items, item)
		}

		data := struct {
			Base     string
			Eingaben []fixkostenListItem
		}{
			Base:     requestBase(r),
			Eingaben: items,
		}

		if err := fixkostenListeTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// fixkostenDetailPosition is one Kostenposition's row on the Detailansicht.
type fixkostenDetailPosition struct {
	Label      string
	LogikLabel string
	KostenW1   float64
	KostenW2   float64
}

func handleFixkostenDetail(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eingabeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid fixkosten eingabe id", http.StatusBadRequest)
			return
		}

		eingabe, err := store.GetFixkostenEingabeDetails(db, eingabeID)
		if err != nil {
			http.Error(w, "fixkosten eingabe: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if eingabe == nil {
			http.NotFound(w, r)
			return
		}

		apartments, err := store.Apartments(db)
		if err != nil {
			http.Error(w, "apartments: "+err.Error(), http.StatusInternalServerError)
			return
		}

		allEingaben, err := store.AllFixkostenEingaben(db)
		if err != nil {
			http.Error(w, "fixkosten eingaben: "+err.Error(), http.StatusInternalServerError)
			return
		}
		allItems := make([]fixkostenListItem, len(allEingaben))
		for i, e := range allEingaben {
			allItems[i] = fixkostenListItem{ID: e.ID, Label: germanPeriodLabel(e.Monat)}
		}

		var positionen []fixkostenDetailPosition
		var kostenW1, kostenW2 float64
		var kostenNote string
		erg, err := calc.Fixkosten(db, eingabeID)
		if err != nil {
			if !errors.Is(err, store.ErrNoKostenpositionenJahr) {
				http.Error(w, "fixkosten: "+err.Error(), http.StatusInternalServerError)
				return
			}
			jahr, jahrErr := store.JahrFromMonat(eingabe.Monat)
			if jahrErr != nil {
				http.Error(w, jahrErr.Error(), http.StatusInternalServerError)
				return
			}
			kostenNote = fmt.Sprintf("Für Jahr %d sind noch keine Kostenpositionen in den Stammdaten angelegt.", jahr)
		} else {
			for _, p := range erg.Positionen {
				positionen = append(positionen, fixkostenDetailPosition{
					Label:      p.Label,
					LogikLabel: logikLabels[p.Logik],
					KostenW1:   p.KostenW1,
					KostenW2:   p.KostenW2,
				})
			}
			kostenW1, kostenW2 = erg.KostenW1, erg.KostenW2
		}

		data := struct {
			Base        string
			Eingabe     *store.FixkostenEingabeDetails
			MonatLabel  string
			AllEingaben []fixkostenListItem
			Apartments  []store.Apartment
			Positionen  []fixkostenDetailPosition
			KostenW1    float64
			KostenW2    float64
			KostenNote  string
		}{
			Base:        requestBase(r),
			Eingabe:     eingabe,
			MonatLabel:  germanPeriodLabel(eingabe.Monat),
			AllEingaben: allItems,
			Apartments:  apartments,
			Positionen:  positionen,
			KostenW1:    kostenW1,
			KostenW2:    kostenW2,
			KostenNote:  kostenNote,
		}

		if err := fixkostenDetailTemplate.ExecuteTemplate(w, "layout", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
