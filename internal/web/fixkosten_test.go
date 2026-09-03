package web

import (
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

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
		{ID: 10, Key: "strom_grundpreis", Label: "Grundpreis Strom"},
		{ID: 2, Key: "gebaeudevers", Label: "Wohngebäudeversicherung"}, // no Jahresdaten -> must be skipped
	}
	jahresdaten := map[int64]store.KostenpositionJahr{
		1:  {KostenpositionID: 1, Logik: store.LogikQM, Typ: store.TypJaehrlich, Jahreswert: 480},
		10: {KostenpositionID: 10, Logik: store.LogikWohneinheit, Typ: store.TypMonatlich},
	}
	values := map[int64]float64{10: 39.90}

	rows := buildFixkostenPositionRows(kostenpositionen, jahresdaten, values)

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
	kostenpositionen := []store.Kostenposition{{ID: 10, Key: "strom_grundpreis", Label: "Grundpreis Strom"}}
	jahresdaten := map[int64]store.KostenpositionJahr{
		10: {KostenpositionID: 10, Logik: store.LogikWohneinheit, Typ: store.TypMonatlich},
	}

	rows := buildFixkostenPositionRows(kostenpositionen, jahresdaten, nil)

	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Value != 0 {
		t.Errorf("Value ohne Vorwert = %v, want 0", rows[0].Value)
	}
}

func TestLogikLabels_CoversAllLogikKonstanten(t *testing.T) {
	for _, logik := range []string{store.LogikWohneinheit, store.LogikFlurstueck, store.LogikQM, store.LogikPersonen} {
		if logikLabels[logik] == "" {
			t.Errorf("logikLabels missing entry for %q", logik)
		}
	}
}
