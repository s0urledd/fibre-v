// observer-api serves the read-only JSON API over the observer store.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

func main() {
	var (
		dataDir = flag.String("data-dir", "./sentinel-data", "dir holding observer.db")
		dbPath  = flag.String("db", "", "SQLite database path (default <data-dir>/observer.db)")
		listen  = flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
		vantage = flag.String("vantage", "local", "vantage name rendered on every response")
	)
	flag.Parse()
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "observer.db")
	}
	log := scan.NewLogger(200)
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.New(st, *vantage),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("api up: listen=%s db=%s vantage=%s", *listen, *dbPath, *vantage)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
	log.Printf("stopped")
}
