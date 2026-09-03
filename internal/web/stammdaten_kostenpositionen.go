package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// stammdatenPositionRow is one Kostenposition's row within one Jahr-Block on
// /stammdaten.
type stammdatenPositionRow struct {
	ID         int64
	Label      string
	Logik      string
	Typ        string
	Jahreswert float64
}

// stammdatenJahrBlock is one Jahr's Kostenpositionen-Daten, as shown on
// /stammdaten - newest first, only the newest gets a "Jahr löschen" button
// (Issue #60 Story 16, matching the prototype).
type stammdatenJahrBlock struct {
	Jahr        int
	IstNeuestes bool
	Positionen  []stammdatenPositionRow
}

// kostenpositionDefaultsByID indexes store.KostenpositionDefaults for O(1)
// lookup - the fallback used when a Jahr is missing a row for some
// Kostenposition (shouldn't normally happen, see UpsertKostenpositionenJahr's
// doc comment; defensive only).
func kostenpositionDefaultsByID() map[int64]store.KostenpositionDefault {
	out := make(map[int64]store.KostenpositionDefault, len(store.KostenpositionDefaults))
	for _, kd := range store.KostenpositionDefaults {
		out[kd.ID] = kd
	}
	return out
}

// buildStammdatenJahrBloecke loads every Jahr's Kostenpositionen-Daten
// (newest first, from store.KostenpositionenJahre's own order) and the
// Jahr after the newest one - the "+ Jahr anlegen" button's target.
func buildStammdatenJahrBloecke(db *sql.DB, kostenpositionen []store.Kostenposition) (blocks []stammdatenJahrBlock, nextJahr int, err error) {
	jahre, err := store.KostenpositionenJahre(db)
	if err != nil {
		return nil, 0, fmt.Errorf("kostenpositionen jahre: %w", err)
	}

	defaults := kostenpositionDefaultsByID()
	blocks = make([]stammdatenJahrBlock, 0, len(jahre))
	for i, jahr := range jahre {
		jahresdaten, err := store.KostenpositionenJahr(db, jahr)
		if err != nil {
			return nil, 0, fmt.Errorf("kostenpositionen jahr %d: %w", jahr, err)
		}

		rows := make([]stammdatenPositionRow, 0, len(kostenpositionen))
		for _, kp := range kostenpositionen {
			row := stammdatenPositionRow{ID: kp.ID, Label: kp.Label}
			if kj, ok := jahresdaten[kp.ID]; ok {
				row.Logik, row.Typ, row.Jahreswert = kj.Logik, kj.Typ, kj.Jahreswert
			} else if kd, ok := defaults[kp.ID]; ok {
				row.Logik, row.Typ = kd.Logik, kd.Typ
			}
			rows = append(rows, row)
		}

		blocks = append(blocks, stammdatenJahrBlock{Jahr: jahr, IstNeuestes: i == 0, Positionen: rows})
	}

	nextJahr = time.Now().Year()
	if len(jahre) > 0 {
		nextJahr = jahre[0] + 1
	}
	return blocks, nextJahr, nil
}

// handleCreateKostenpositionenJahr creates the next Jahr, prefilled from the
// newest existing Jahr (or the 14 Positionen's fixed defaults if none exists
// yet) - "Jahr anlegen" (Issue #60 Story 15).
func handleCreateKostenpositionenJahr(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jahre, err := store.KostenpositionenJahre(db)
		if err != nil {
			http.Error(w, "kostenpositionen jahre: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var quelle map[int64]store.KostenpositionJahr
		neuesJahr := time.Now().Year()
		if len(jahre) > 0 {
			neuesJahr = jahre[0] + 1
			quelle, err = store.KostenpositionenJahr(db, jahre[0])
			if err != nil {
				http.Error(w, "kostenpositionen jahr: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		in := make(map[int64]store.KostenpositionJahrInput, len(store.KostenpositionDefaults))
		for _, kd := range store.KostenpositionDefaults {
			if kj, ok := quelle[kd.ID]; ok {
				in[kd.ID] = store.KostenpositionJahrInput{Logik: kj.Logik, Typ: kj.Typ, Jahreswert: kj.Jahreswert}
			} else {
				in[kd.ID] = store.KostenpositionJahrInput{Logik: kd.Logik, Typ: kd.Typ}
			}
		}

		if err := store.UpsertKostenpositionenJahr(db, neuesJahr, in); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/stammdaten", http.StatusFound)
	}
}

// parseKostenpositionenJahrInput parses one Jahr-Block's form fields
// (logik_<id>/typ_<id>/jahreswert_<id> per Kostenposition).
func parseKostenpositionenJahrInput(r *http.Request, kostenpositionen []store.Kostenposition) (map[int64]store.KostenpositionJahrInput, error) {
	in := make(map[int64]store.KostenpositionJahrInput, len(kostenpositionen))
	for _, kp := range kostenpositionen {
		idStr := strconv.FormatInt(kp.ID, 10)

		logik := r.FormValue("logik_" + idStr)
		if _, ok := logikLabels[logik]; !ok {
			return nil, fmt.Errorf("ungültige Berechnungslogik für %s", kp.Label)
		}

		typ := r.FormValue("typ_" + idStr)
		if typ != store.TypJaehrlich && typ != store.TypMonatlich {
			return nil, fmt.Errorf("ungültiger Typ für %s", kp.Label)
		}

		jahreswert, err := parseFormFloat(r, "jahreswert_"+idStr, "Jahreswert", idStr)
		if err != nil {
			return nil, fmt.Errorf("ungültiger Jahreswert für %s", kp.Label)
		}

		in[kp.ID] = store.KostenpositionJahrInput{Logik: logik, Typ: typ, Jahreswert: jahreswert}
	}
	return in, nil
}

// handleUpdateKostenpositionenJahr saves one Jahr-Block's corrections
// (Issue #60 Story 16).
func handleUpdateKostenpositionenJahr(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jahr, err := strconv.Atoi(r.PathValue("jahr"))
		if err != nil {
			http.Error(w, "invalid jahr", http.StatusBadRequest)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
			return
		}

		kostenpositionen, err := store.Kostenpositionen(db)
		if err != nil {
			http.Error(w, "kostenpositionen: "+err.Error(), http.StatusInternalServerError)
			return
		}

		in, err := parseKostenpositionenJahrInput(r, kostenpositionen)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := store.UpsertKostenpositionenJahr(db, jahr, in); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/stammdaten", http.StatusFound)
	}
}

// handleDeleteKostenpositionenJahr removes an entire Jahr's Kostenpositionen-
// Daten (Issue #60 Story 16).
func handleDeleteKostenpositionenJahr(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jahr, err := strconv.Atoi(r.PathValue("jahr"))
		if err != nil {
			http.Error(w, "invalid jahr", http.StatusBadRequest)
			return
		}

		if err := store.DeleteKostenpositionenJahr(db, jahr); err != nil {
			http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, requestBase(r)+"/stammdaten", http.StatusFound)
	}
}
