// Command catalog runs the staff application: it starts catalog updates
// from the supplier and serves the staff page and JSON API.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/app"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

func main() {
	defaults := importer.DefaultConfig()
	var cfg app.Config
	flag.StringVar(&cfg.Addr, "addr", env("ADDR", ":8080"), "listen address")
	flag.StringVar(&cfg.DatabaseURL, "database-url",
		env("DATABASE_URL", "postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable"), "PostgreSQL URL")
	flag.StringVar(&cfg.Supplier.BaseURL, "supplier-url", env("SUPPLIER_URL", "http://localhost:9090"), "supplier base URL")
	flag.IntVar(&cfg.Supplier.Attempts, "supplier-attempts", 3, "tries per supplier request")
	flag.DurationVar(&cfg.Supplier.Timeout, "supplier-timeout", 5*time.Second, "timeout per supplier request")
	flag.IntVar(&cfg.Importer.PageSize, "page-size", defaults.PageSize, "batches requested from the supplier at once")
	flag.IntVar(&cfg.Importer.MaxRefills, "max-refills", defaults.MaxRefills, "Window 2 refills before an update completes")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Ctrl+C or SIGTERM cancels ctx and starts a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("cannot start", "error", err)
		os.Exit(1)
	}
	if err := a.Run(ctx); err != nil {
		log.Error("stopped with error", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
