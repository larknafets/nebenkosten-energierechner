// Command nebenkostenrechner runs the Nebenkostenrechner web server.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/larknafets/nebenkostenrechner/internal/store"
	"github.com/larknafets/nebenkostenrechner/internal/web"
)

// version and buildDate are set via -ldflags at build time (Docker build
// args and GoReleaser, Ticket #48) - both stay empty for a local `go run`,
// which hides the Dashboard's version badge entirely.
var (
	version   = ""
	buildDate = ""
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/nebenkosten.db"
	}

	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer db.Close()

	mux := web.NewMux(db, version, buildDate)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// widgetAddr serves NewWidgetMux's 2 read-only HA-Widget-Routen
	// (Issue #77 ff.) on a 2nd, Ingress-freies Port - gedacht für ein
	// Lovelace "Webpage card" Iframe, das HA-Ingress-Sessions nicht
	// zuverlässig einbetten kann. Läuft immer mit (wie der Haupt-Server),
	// eigener Default-Port analog zu LISTEN_ADDR.
	widgetAddr := os.Getenv("WIDGET_LISTEN_ADDR")
	if widgetAddr == "" {
		widgetAddr = ":8081"
	}
	go func() {
		log.Printf("widget routes listening on %s", widgetAddr)
		if err := http.ListenAndServe(widgetAddr, web.NewWidgetMux(db)); err != nil {
			log.Fatalf("widget serve: %v", err)
		}
	}()

	log.Printf("listening on %s (db: %s)", addr, dbPath)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
