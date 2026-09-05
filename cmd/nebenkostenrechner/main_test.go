package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateDBPathFrom covers the one-time copy that keeps existing HA
// add-on installs' data when DB_PATH moves off its previous default.
func TestMigrateDBPathFrom(t *testing.T) {
	t.Run("same path is a no-op", func(t *testing.T) {
		if err := migrateDBPathFrom(legacyDBPath, legacyDBPath); err != nil {
			t.Fatalf("migrateDBPathFrom: %v", err)
		}
	})

	t.Run("no legacy file, fresh install", func(t *testing.T) {
		dir := t.TempDir()
		newPath := filepath.Join(dir, "config", "nebenkosten.db")
		oldPath := filepath.Join(dir, "old", "nebenkosten.db")
		if err := migrateDBPathFrom(newPath, oldPath); err != nil {
			t.Fatalf("migrateDBPathFrom: %v", err)
		}
		if _, err := os.Stat(newPath); !os.IsNotExist(err) {
			t.Fatalf("expected no file at %s, got err=%v", newPath, err)
		}
	})

	t.Run("new path already has a db, leaves it alone", func(t *testing.T) {
		dir := t.TempDir()
		newPath := filepath.Join(dir, "nebenkosten.db")
		oldPath := filepath.Join(dir, "old", "nebenkosten.db")
		if err := os.WriteFile(newPath, []byte("existing"), 0o644); err != nil {
			t.Fatalf("seed new path: %v", err)
		}
		if err := migrateDBPathFrom(newPath, oldPath); err != nil {
			t.Fatalf("migrateDBPathFrom: %v", err)
		}
		got, err := os.ReadFile(newPath)
		if err != nil {
			t.Fatalf("read new path: %v", err)
		}
		if string(got) != "existing" {
			t.Fatalf("expected existing content preserved, got %q", got)
		}
	})

	t.Run("legacy db present, copies it to new path", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old", "nebenkosten.db")
		newPath := filepath.Join(dir, "config", "nebenkosten.db")
		if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
			t.Fatalf("mkdir old: %v", err)
		}
		if err := os.WriteFile(oldPath, []byte("legacy data"), 0o644); err != nil {
			t.Fatalf("seed old path: %v", err)
		}
		if err := migrateDBPathFrom(newPath, oldPath); err != nil {
			t.Fatalf("migrateDBPathFrom: %v", err)
		}
		got, err := os.ReadFile(newPath)
		if err != nil {
			t.Fatalf("read new path: %v", err)
		}
		if string(got) != "legacy data" {
			t.Fatalf("expected copied legacy content, got %q", got)
		}
		if _, err := os.Stat(oldPath); err != nil {
			t.Fatalf("expected legacy file to remain at %s: %v", oldPath, err)
		}
	})
}
