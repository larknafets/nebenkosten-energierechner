package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestJahrFromMonat(t *testing.T) {
	jahr, err := JahrFromMonat("2026-09-01")
	if err != nil {
		t.Fatalf("JahrFromMonat: %v", err)
	}
	if jahr != 2026 {
		t.Errorf("JahrFromMonat(2026-09-01) = %d, want 2026", jahr)
	}

	if _, err := JahrFromMonat("garbage"); err == nil {
		t.Error("JahrFromMonat(garbage): want error, got nil")
	}
}

func TestKostenpositionen_Seed(t *testing.T) {
	db := openTestDB(t)

	kps, err := Kostenpositionen(db)
	if err != nil {
		t.Fatalf("Kostenpositionen: %v", err)
	}
	if len(kps) != 14 {
		t.Fatalf("Kostenpositionen = %d, want 14", len(kps))
	}

	wantKeys := []string{
		"grundsteuer", "gebaeudevers", "deich_grund", "deich_bau", "kreisverband",
		"abfall_haushalt", "abfall_personen", "abfall_biomuell", "abfall_restmuell",
		"strom_grundpreis", "trinkwasser", "abwasser", "internet", "wp_wartung",
	}
	for i, want := range wantKeys {
		if kps[i].Key != want {
			t.Errorf("Kostenpositionen[%d].Key = %q, want %q", i, kps[i].Key, want)
		}
		if kps[i].Label == "" {
			t.Errorf("Kostenpositionen[%d] (%s) has empty Label", i, kps[i].Key)
		}
	}
}

func TestKostenpositionenJahr_UpsertRoundtrip(t *testing.T) {
	db := openTestDB(t)

	in := map[int64]KostenpositionJahrInput{
		1:  {Logik: LogikQM, Typ: TypJaehrlich, Jahreswert: 450.60},
		10: {Logik: LogikWohneinheit, Typ: TypMonatlich, Jahreswert: 0},
	}
	if err := UpsertKostenpositionenJahr(db, 2026, in); err != nil {
		t.Fatalf("UpsertKostenpositionenJahr: %v", err)
	}

	got, err := KostenpositionenJahr(db, 2026)
	if err != nil {
		t.Fatalf("KostenpositionenJahr: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("KostenpositionenJahr(2026) = %d entries, want 2", len(got))
	}
	kj1 := got[1]
	if kj1.Logik != LogikQM || kj1.Typ != TypJaehrlich || kj1.Jahreswert != 450.60 {
		t.Errorf("position 1 = %+v, want Logik:%s Typ:%s Jahreswert:450.60", kj1, LogikQM, TypJaehrlich)
	}
	if _, ok := got[2]; ok {
		t.Errorf("position 2 has no data for 2026, want absent from map")
	}

	// Re-upsert (correction) overwrites in place, doesn't duplicate.
	if err := UpsertKostenpositionenJahr(db, 2026, map[int64]KostenpositionJahrInput{
		1: {Logik: LogikQM, Typ: TypJaehrlich, Jahreswert: 500},
	}); err != nil {
		t.Fatalf("UpsertKostenpositionenJahr (correction): %v", err)
	}
	got, err = KostenpositionenJahr(db, 2026)
	if err != nil {
		t.Fatalf("KostenpositionenJahr after correction: %v", err)
	}
	if got[1].Jahreswert != 500 {
		t.Errorf("position 1 Jahreswert after correction = %v, want 500", got[1].Jahreswert)
	}
	if len(got) != 2 {
		t.Errorf("KostenpositionenJahr(2026) after correction = %d entries, want 2 (no duplicate row)", len(got))
	}
}

func TestKostenpositionenJahre_NewestFirst(t *testing.T) {
	db := openTestDB(t)

	for _, jahr := range []int{2025, 2027, 2026} {
		if err := UpsertKostenpositionenJahr(db, jahr, map[int64]KostenpositionJahrInput{
			1: {Logik: LogikQM, Typ: TypJaehrlich, Jahreswert: 100},
		}); err != nil {
			t.Fatalf("UpsertKostenpositionenJahr(%d): %v", jahr, err)
		}
	}

	jahre, err := KostenpositionenJahre(db)
	if err != nil {
		t.Fatalf("KostenpositionenJahre: %v", err)
	}
	want := []int{2027, 2026, 2025}
	if len(jahre) != len(want) {
		t.Fatalf("KostenpositionenJahre = %v, want %v", jahre, want)
	}
	for i, w := range want {
		if jahre[i] != w {
			t.Errorf("KostenpositionenJahre[%d] = %d, want %d", i, jahre[i], w)
		}
	}
}

func TestDeleteKostenpositionenJahr(t *testing.T) {
	db := openTestDB(t)

	if err := UpsertKostenpositionenJahr(db, 2026, map[int64]KostenpositionJahrInput{
		1: {Logik: LogikQM, Typ: TypJaehrlich, Jahreswert: 100},
		2: {Logik: LogikFlurstueck, Typ: TypJaehrlich, Jahreswert: 200},
	}); err != nil {
		t.Fatalf("UpsertKostenpositionenJahr: %v", err)
	}

	if err := DeleteKostenpositionenJahr(db, 2026); err != nil {
		t.Fatalf("DeleteKostenpositionenJahr: %v", err)
	}

	got, err := KostenpositionenJahr(db, 2026)
	if err != nil {
		t.Fatalf("KostenpositionenJahr after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("KostenpositionenJahr(2026) after delete = %d entries, want 0 (whole Jahr removed)", len(got))
	}

	jahre, err := KostenpositionenJahre(db)
	if err != nil {
		t.Fatalf("KostenpositionenJahre after delete: %v", err)
	}
	if len(jahre) != 0 {
		t.Errorf("KostenpositionenJahre after delete = %v, want empty", jahre)
	}
}

func TestLatestJaehrlichWert(t *testing.T) {
	db := openTestDB(t)

	t.Run("keine Historie -> ok false", func(t *testing.T) {
		_, ok, err := LatestJaehrlichWert(db, 1, 2026)
		if err != nil {
			t.Fatalf("LatestJaehrlichWert: %v", err)
		}
		if ok {
			t.Errorf("LatestJaehrlichWert ok = true, want false (no data at all)")
		}
	})

	// 2025: jaehrlich mit Jahreswert 120. 2026: Typ-Wechsel auf monatlich
	// (kein Jahreswert mehr relevant, aber die Zeile existiert weiterhin).
	if err := UpsertKostenpositionenJahr(db, 2025, map[int64]KostenpositionJahrInput{
		1: {Logik: LogikWohneinheit, Typ: TypJaehrlich, Jahreswert: 120},
	}); err != nil {
		t.Fatalf("UpsertKostenpositionenJahr(2025): %v", err)
	}
	if err := UpsertKostenpositionenJahr(db, 2026, map[int64]KostenpositionJahrInput{
		1: {Logik: LogikWohneinheit, Typ: TypMonatlich, Jahreswert: 0},
	}); err != nil {
		t.Fatalf("UpsertKostenpositionenJahr(2026): %v", err)
	}

	t.Run("faellt auf letztes jaehrliches Jahr zurueck", func(t *testing.T) {
		wert, ok, err := LatestJaehrlichWert(db, 1, 2026)
		if err != nil {
			t.Fatalf("LatestJaehrlichWert: %v", err)
		}
		if !ok || wert != 120 {
			t.Errorf("LatestJaehrlichWert(..., 2026) = (%v, %v), want (120, true)", wert, ok)
		}
	})

	t.Run("maxJahr vor der Historie -> ok false", func(t *testing.T) {
		_, ok, err := LatestJaehrlichWert(db, 1, 2024)
		if err != nil {
			t.Fatalf("LatestJaehrlichWert: %v", err)
		}
		if ok {
			t.Errorf("LatestJaehrlichWert(..., 2024) ok = true, want false (2025-Zeile liegt danach)")
		}
	})
}

func mustCreateFixkostenEingabe(t *testing.T, db *sql.DB, monat string, werte map[int64]float64) int64 {
	t.Helper()
	id, err := CreateFixkostenEingabe(db, FixkostenInput{
		Monat:    monat,
		Personen: map[int64]int64{1: 2, 2: 1},
		Werte:    werte,
	})
	if err != nil {
		t.Fatalf("CreateFixkostenEingabe %s: %v", monat, err)
	}
	return id
}

func TestFixkostenEingabe_CRUD_Roundtrip(t *testing.T) {
	db := openTestDB(t)

	id := mustCreateFixkostenEingabe(t, db, "2026-09-01", map[int64]float64{10: 12.50, 13: 39.90})

	got, err := GetFixkostenEingabeDetails(db, id)
	if err != nil {
		t.Fatalf("GetFixkostenEingabeDetails: %v", err)
	}
	if got == nil {
		t.Fatal("GetFixkostenEingabeDetails: want entry, got nil")
	}
	if got.Monat != "2026-09-01" {
		t.Errorf("Monat = %q, want 2026-09-01", got.Monat)
	}
	if got.Werte[10] != 12.50 || got.Werte[13] != 39.90 {
		t.Errorf("Werte = %v, want {10:12.50, 13:39.90}", got.Werte)
	}
	if got.Personen[1] != 2 || got.Personen[2] != 1 {
		t.Errorf("Personen = %v, want {1:2, 2:1}", got.Personen)
	}

	// Update: neuer Monat, geaenderter Wert, neuer Personenstand.
	if err := UpdateFixkostenEingabe(db, id, FixkostenInput{
		Monat:    "2026-09-02",
		Personen: map[int64]int64{1: 3, 2: 1},
		Werte:    map[int64]float64{10: 15.00, 13: 39.90},
	}); err != nil {
		t.Fatalf("UpdateFixkostenEingabe: %v", err)
	}
	got, err = GetFixkostenEingabeDetails(db, id)
	if err != nil {
		t.Fatalf("GetFixkostenEingabeDetails after update: %v", err)
	}
	if got.Monat != "2026-09-02" {
		t.Errorf("Monat after update = %q, want 2026-09-02", got.Monat)
	}
	if got.Werte[10] != 15.00 {
		t.Errorf("Werte[10] after update = %v, want 15.00", got.Werte[10])
	}
	if got.Personen[1] != 3 {
		t.Errorf("Personen[1] after update = %v, want 3", got.Personen[1])
	}

	latest, err := GetLatestFixkostenEingabe(db)
	if err != nil {
		t.Fatalf("GetLatestFixkostenEingabe: %v", err)
	}
	if latest == nil || latest.ID != id {
		t.Fatalf("GetLatestFixkostenEingabe = %+v, want id %d", latest, id)
	}
}

func TestGetFixkostenEingabeDetails_NotFound(t *testing.T) {
	db := openTestDB(t)
	got, err := GetFixkostenEingabeDetails(db, 999)
	if err != nil {
		t.Fatalf("GetFixkostenEingabeDetails: %v", err)
	}
	if got != nil {
		t.Errorf("GetFixkostenEingabeDetails(999) = %+v, want nil", got)
	}
}

func TestGetLatestFixkostenEingabe_NoEingaben(t *testing.T) {
	db := openTestDB(t)
	got, err := GetLatestFixkostenEingabe(db)
	if err != nil {
		t.Fatalf("GetLatestFixkostenEingabe: %v", err)
	}
	if got != nil {
		t.Errorf("GetLatestFixkostenEingabe on empty db = %+v, want nil", got)
	}
}

func TestUpdateFixkostenEingabe_NotFound(t *testing.T) {
	db := openTestDB(t)
	err := UpdateFixkostenEingabe(db, 999, FixkostenInput{Monat: "2026-09-01"})
	if !errors.Is(err, ErrFixkostenEingabeNotFound) {
		t.Fatalf("UpdateFixkostenEingabe on unknown id: err = %v, want ErrFixkostenEingabeNotFound", err)
	}
}

func TestDeleteFixkostenEingabe(t *testing.T) {
	db := openTestDB(t)
	id := mustCreateFixkostenEingabe(t, db, "2026-09-01", map[int64]float64{10: 12.50})

	if err := DeleteFixkostenEingabe(db, id); err != nil {
		t.Fatalf("DeleteFixkostenEingabe: %v", err)
	}

	got, err := GetFixkostenEingabeDetails(db, id)
	if err != nil {
		t.Fatalf("GetFixkostenEingabeDetails after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetFixkostenEingabeDetails after delete = %+v, want nil", got)
	}

	all, err := AllFixkostenEingaben(db)
	if err != nil {
		t.Fatalf("AllFixkostenEingaben: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("AllFixkostenEingaben after delete = %d, want 0", len(all))
	}
}

func TestDeleteFixkostenEingabe_NotFound(t *testing.T) {
	db := openTestDB(t)
	err := DeleteFixkostenEingabe(db, 999)
	if !errors.Is(err, ErrFixkostenEingabeNotFound) {
		t.Fatalf("DeleteFixkostenEingabe on unknown id: err = %v, want ErrFixkostenEingabeNotFound", err)
	}
}

func TestAllFixkostenEingaben_NewestFirst(t *testing.T) {
	db := openTestDB(t)
	mustCreateFixkostenEingabe(t, db, "2026-07-01", nil)
	newest := mustCreateFixkostenEingabe(t, db, "2026-09-01", nil)
	mustCreateFixkostenEingabe(t, db, "2026-08-01", nil)

	all, err := AllFixkostenEingaben(db)
	if err != nil {
		t.Fatalf("AllFixkostenEingaben: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("AllFixkostenEingaben = %d, want 3", len(all))
	}
	if all[0].ID != newest || all[0].Monat != "2026-09-01" {
		t.Errorf("AllFixkostenEingaben[0] = %+v, want newest (2026-09-01)", all[0])
	}
}
