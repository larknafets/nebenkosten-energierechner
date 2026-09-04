package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

// csv_test.go covers the handler seam (status code, headers, redirect) -
// parseImportCSV/parseImportRow's row-level logic stays leaf-tested above.

func seedPeriodInput() store.PeriodInput {
	readings := make(map[string]float64, len(store.MeterKeys))
	for _, key := range store.MeterKeys {
		readings[key] = 0
	}
	return store.PeriodInput{
		ReadingDate:             "2026-06-01",
		Monat:                   "2026-06-01",
		Strompreis:              0.22,
		FrischwasserPreis:       1.46,
		AbwasserPreis:           4.87,
		HeizungWaermeGewichtung: 0.7,
		EinspeisungPreis:        0.08,
		Readings:                readings,
		Personen:                map[int64]int64{1: 2, 2: 1},
	}
}

func TestHandleExportCSV(t *testing.T) {
	db := openTestDB(t)
	if _, err := store.CreatePeriod(db, seedPeriodInput()); err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ablesungen/export.csv", nil)
	w := httptest.NewRecorder()
	handleExportCSV(db)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv prefix", ct)
	}
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Error("body missing UTF-8 BOM")
	}
	if !strings.Contains(string(body), "2026-06-01") {
		t.Error("body missing seeded ReadingDate")
	}
}

func csvUploadRequest(t *testing.T, csvText string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("csv", "ablesungen.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	part.Write([]byte(csvText))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/ablesungen/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestHandleImportCSV_Success(t *testing.T) {
	db := openTestDB(t)
	csvText := strings.Join(csvHeader, ";") + "\n" +
		csvRow("2026-06-01", "100") + "\n" +
		csvRow("2026-07-01", "210") + "\n"

	w := httptest.NewRecorder()
	handleImportCSV(db)(w, csvUploadRequest(t, csvText))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusFound, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "imported=2") {
		t.Errorf("Location = %q, want imported=2", loc)
	}

	periods, err := store.AllPeriods(db)
	if err != nil {
		t.Fatalf("AllPeriods: %v", err)
	}
	if len(periods) != 2 {
		t.Errorf("len(periods) = %d, want 2", len(periods))
	}
}

func TestHandleImportCSV_RejectsWhenDataExists(t *testing.T) {
	db := openTestDB(t)
	if _, err := store.CreatePeriod(db, seedPeriodInput()); err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}

	csvText := strings.Join(csvHeader, ";") + "\n" + csvRow("2026-06-01", "100") + "\n"
	w := httptest.NewRecorder()
	handleImportCSV(db)(w, csvUploadRequest(t, csvText))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImportCSV_BadRowRejectedWithoutPersisting(t *testing.T) {
	db := openTestDB(t)
	csvText := strings.Join(csvHeader, ";") + "\n" + csvRow("2026-06-01", "nicht-numerisch") + "\n"

	w := httptest.NewRecorder()
	handleImportCSV(db)(w, csvUploadRequest(t, csvText))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	periods, err := store.AllPeriods(db)
	if err != nil {
		t.Fatalf("AllPeriods: %v", err)
	}
	if len(periods) != 0 {
		t.Errorf("len(periods) = %d, want 0 (bad row must abort whole import)", len(periods))
	}
}
