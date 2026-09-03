package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/larknafets/nebenkostenrechner/internal/store"
)

func TestKostenpositionDefaultsByID(t *testing.T) {
	got := kostenpositionDefaultsByID()
	if len(got) != len(store.KostenpositionDefaults) {
		t.Fatalf("len(kostenpositionDefaultsByID()) = %d, want %d", len(got), len(store.KostenpositionDefaults))
	}
	for _, kd := range store.KostenpositionDefaults {
		if got[kd.ID] != kd {
			t.Errorf("kostenpositionDefaultsByID()[%d] = %+v, want %+v", kd.ID, got[kd.ID], kd)
		}
	}
}

func TestParseKostenpositionenJahrInput(t *testing.T) {
	kostenpositionen := []store.Kostenposition{
		{ID: 1, Key: "grundsteuer", Label: "Grundsteuer"},
		{ID: 10, Key: "strom_grundpreis", Label: "Grundpreis Strom"},
	}

	form := url.Values{
		"logik_1":       {store.LogikQM},
		"typ_1":         {store.TypJaehrlich},
		"jahreswert_1":  {"500"},
		"logik_10":      {store.LogikWohneinheit},
		"typ_10":        {store.TypMonatlich},
		"jahreswert_10": {"0"},
	}
	r, err := http.NewRequest(http.MethodPost, "/stammdaten/jahre/2026", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	got, err := parseKostenpositionenJahrInput(r, kostenpositionen)
	if err != nil {
		t.Fatalf("parseKostenpositionenJahrInput: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[1].Logik != store.LogikQM || got[1].Typ != store.TypJaehrlich || got[1].Jahreswert != 500 {
		t.Errorf("got[1] = %+v, want Logik:%s Typ:%s Jahreswert:500", got[1], store.LogikQM, store.TypJaehrlich)
	}
	if got[10].Logik != store.LogikWohneinheit || got[10].Typ != store.TypMonatlich {
		t.Errorf("got[10] = %+v, want Logik:%s Typ:%s", got[10], store.LogikWohneinheit, store.TypMonatlich)
	}
}

func TestParseKostenpositionenJahrInput_UngueltigeLogik(t *testing.T) {
	kostenpositionen := []store.Kostenposition{{ID: 1, Key: "grundsteuer", Label: "Grundsteuer"}}
	form := url.Values{"logik_1": {"garbage"}, "typ_1": {store.TypJaehrlich}, "jahreswert_1": {"100"}}
	r, _ := http.NewRequest(http.MethodPost, "/stammdaten/jahre/2026", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, err := parseKostenpositionenJahrInput(r, kostenpositionen); err == nil {
		t.Error("parseKostenpositionenJahrInput mit ungueltiger Logik: want error, got nil")
	}
}

func TestParseKostenpositionenJahrInput_UngueltigerTyp(t *testing.T) {
	kostenpositionen := []store.Kostenposition{{ID: 1, Key: "grundsteuer", Label: "Grundsteuer"}}
	form := url.Values{"logik_1": {store.LogikQM}, "typ_1": {"garbage"}, "jahreswert_1": {"100"}}
	r, _ := http.NewRequest(http.MethodPost, "/stammdaten/jahre/2026", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, err := parseKostenpositionenJahrInput(r, kostenpositionen); err == nil {
		t.Error("parseKostenpositionenJahrInput mit ungueltigem Typ: want error, got nil")
	}
}
