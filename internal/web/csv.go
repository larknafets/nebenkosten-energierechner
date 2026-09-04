package web

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// csvHeader is the canonical CSV column order for both export (Ticket #53)
// and import (Ticket #54) - reading_date, every meter key (Zählerstände,
// not Verbrauch), the period-level prices/Gewichtung, then Personen per
// apartment (fixed ids 1/2, see store's seed()). No qm_1/qm_2 columns
// (Issue #61 moved Wohnungsgröße off the Ablesung onto /stammdaten - hard
// cut, no backward compatibility with the old format).
var csvHeader = append(append([]string{"reading_date", "monat"}, store.MeterKeys...),
	"strompreis", "frischwasser_preis", "abwasser_preis", "heizung_gewichtung", "einspeisung_preis",
	"personen_1", "personen_2",
)

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
			row = append(row, p.ReadingDate, p.Monat)
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

	monat := strings.TrimSpace(cell("monat"))
	if _, err := time.Parse("2006-01-02", monat); err != nil {
		return store.PeriodInput{}, fmt.Errorf("Zeile %d: ungültiger Abrechnungsmonat %q (Format JJJJ-MM-01)", line, monat)
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
		Monat:                   monat,
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
