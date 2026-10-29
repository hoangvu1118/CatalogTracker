// Package app builds the application from its parts and runs it.
// It is the only package that knows about every concrete implementation.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoangvu1118/catalogtracker/internal/httpapi"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
	"github.com/hoangvu1118/catalogtracker/internal/storage"
	"github.com/hoangvu1118/catalogtracker/internal/supplier"
	"github.com/hoangvu1118/catalogtracker/internal/web"
)

// Config is everything the application needs to start.
type Config struct {
	Addr        string
	DatabaseURL string
	Supplier    supplier.ClientConfig
	Importer    importer.Config
}

// App is the running application.
type App struct {
	cfg     Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	Store   *storage.Store
	Service *importer.Service
	// latest asks the supplier for its newest batch for the window view,
	// with one quick attempt so the page never waits on retries.
	latest *supplier.Client
}

// New connects to the database, applies migrations, wires the parts
// together and marks updates cut off by a previous crash as interrupted.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	pool, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := storage.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	store := storage.NewStore(pool)
	feed := supplier.NewClient(cfg.Supplier)
	service := importer.NewService(feed, store, cfg.Importer, log)

	if _, err := service.Recover(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	latest := supplier.NewClient(supplier.ClientConfig{BaseURL: cfg.Supplier.BaseURL, Timeout: 2 * time.Second, Attempts: 1})
	return &App{cfg: cfg, log: log, pool: pool, Store: store, Service: service, latest: latest}, nil
}

// Handler returns the application's HTTP routes.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	httpapi.New(a.Service, a.Store, httpapi.WindowSource{
		Feed:       a.latest,
		Progress:   a.Service,
		PageSize:   a.cfg.Importer.PageSize,
		MaxRefills: a.cfg.Importer.MaxRefills,
	}).Register(mux)
	web.Register(mux)
	return mux
}

// Run listens on cfg.Addr until ctx is cancelled, then shuts down.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Addr)
	if err != nil {
		return err
	}
	return a.Serve(ctx, ln)
}

// Serve serves HTTP on ln until ctx is cancelled. Shutdown order: stop
// accepting requests, stop running updates (they become interrupted and
// can be resumed after the next start), close the database.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- server.Serve(ln) }()
	a.log.Info("listening", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		a.close()
		return err
	case <-ctx.Done():
	}

	a.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	a.close()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (a *App) close() {
	a.Service.Shutdown()
	a.pool.Close()
}
