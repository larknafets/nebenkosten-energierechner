package web

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestParseFixkostenMonat(t *testing.T) {
	got, err := parseFixkostenMonat("2026-09")
	if err != nil {
		t.Fatalf("parseFixkostenMonat: %v", err)
	}
	if got != "2026-09-01" {
		t.Errorf("parseFixkostenMonat(2026-09) = %q, want 2026-09-01", got)
	}

	if _, err := parseFixkostenMonat("not-a-month"); err == nil {
		t.Error("parseFixkostenMonat(not-a-month): want error, got nil")
	}
	if _, err := parseFixkostenMonat(""); err == nil {
		t.Error("parseFixkostenMonat(\"\"): want error, got nil")
	}
}

func TestMonatForInput(t *testing.T) {
	if got := monatForInput("2026-09-01"); got != "2026-09" {
		t.Errorf("monatForInput(2026-09-01) = %q, want 2026-09", got)
	}
	if got := monatForInput("garbage"); got != "" {
		t.Errorf("monatForInput(garbage) = %q, want empty string", got)
	}
}

func TestBuildFixkostenPositionRows(t *testing.T) {
	kostenpositionen := []store.Kostenposition{
		{ID: 1, Key: "grundsteuer", Label: "Grundsteuer"},
		{ID: 10, Key: "strom_grundpreis", Label: "Grundgebühr Strom"},
		{ID: 2, Key: "gebaeudevers", Label: "Wohngebäudeversicherung"}, // no Jahresdaten -> must be skipped
	}
	jahresdaten := map[int64]store.KostenpositionJahr{
		1:  {KostenpositionID: 1, Logik: store.LogikQM, Typ: store.TypJaehrlich, Jahreswert: 480},
		10: {KostenpositionID: 10, Logik: store.LogikWohneinheit, Typ: store.TypMonatlich},
	}
	values := map[int64]float64{10: 39.90}

	rows, err := buildFixkostenPositionRows(openTestDB(t), kostenpositionen, jahresdaten, values, 2026)
	if err != nil {
		t.Fatalf("buildFixkostenPositionRows: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (Position ohne Jahresdaten wird uebersprungen)", len(rows))
	}

	if rows[0].ID != 1 || !rows[0].IsJaehrlich || rows[0].Value != 40 {
		t.Errorf("rows[0] = %+v, want id:1 IsJaehrlich:true Value:40 (480/12)", rows[0])
	}
	if rows[0].LogikLabel != logikLabels[store.LogikQM] {
		t.Errorf("rows[0].LogikLabel = %q, want %q", rows[0].LogikLabel, logikLabels[store.LogikQM])
	}

	if rows[1].ID != 10 || rows[1].IsJaehrlich || rows[1].Value != 39.90 {
		t.Errorf("rows[1] = %+v, want id:10 IsJaehrlich:false Value:39.90 (aus values, nicht /12)", rows[1])
	}
}

func TestBuildFixkostenPositionRows_MonatlichOhneWert(t *testing.T) {
	kostenpositionen := []store.Kostenposition{{ID: 10, Key: "strom_grundpreis", Label: "Grundgebühr Strom"}}
	jahresdaten := map[int64]store.KostenpositionJahr{
		10: {KostenpositionID: 10, Logik: store.LogikWohneinheit, Typ: store.TypMonatlich},
	}

	rows, err := buildFixkostenPositionRows(openTestDB(t), kostenpositionen, jahresdaten, nil, 2026)
	if err != nil {
		t.Fatalf("buildFixkostenPositionRows: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Value != 0 {
		t.Errorf("Value ohne Vorwert und ohne Jahreswert-Historie = %v, want 0", rows[0].Value)
	}
}

// TestBuildFixkostenPositionRows_TypWechselFallback reproduces Issue #69:
// eine Position war 2025 "jährlich" mit Jahreswert 1200, wechselt 2026 auf
// "monatlich" - ohne expliziten Wert in values (weil die letzte Eingabe zum
// Zeitpunkt "jährlich" war und parseFixkostenInput sie damals übersprungen
// hat) muss die Vorbelegung auf den letzten bekannten Jahreswert/12
// zurückfallen, nicht auf 0.
func TestBuildFixkostenPositionRows_TypWechselFallback(t *testing.T) {
	db := openTestDB(t)
	if err := store.UpsertKostenpositionenJahr(db, 2025, map[int64]store.KostenpositionJahrInput{
		10: {Logik: store.LogikWohneinheit, Typ: store.TypJaehrlich, Jahreswert: 1200},
	}); err != nil {
		t.Fatalf("seed 2025 kostenpositionen_jahr: %v", err)
	}

	kostenpositionen := []store.Kostenposition{{ID: 10, Key: "strom_grundpreis", Label: "Grundgebühr Strom"}}
	jahresdaten := map[int64]store.KostenpositionJahr{
		10: {KostenpositionID: 10, Logik: store.LogikWohneinheit, Typ: store.TypMonatlich},
	}

	rows, err := buildFixkostenPositionRows(db, kostenpositionen, jahresdaten, nil, 2026)
	if err != nil {
		t.Fatalf("buildFixkostenPositionRows: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Value != 100 {
		t.Errorf("Value = %v, want 100 (letzter bekannter Jahreswert 1200 / 12)", rows[0].Value)
	}
}

func TestLogikLabels_CoversAllLogikKonstanten(t *testing.T) {
	for _, logik := range []string{store.LogikWohneinheit, store.LogikFlurstueck, store.LogikQM, store.LogikPersonen} {
		if logikLabels[logik] == "" {
			t.Errorf("logikLabels missing entry for %q", logik)
		}
	}
}
