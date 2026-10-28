package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/importer"
	"github.com/hoangvu1118/catalogtracker/internal/supplier"
	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
	"github.com/hoangvu1118/catalogtracker/internal/testdb"
)

func storyFeed(t *testing.T) fake.Feed {
	t.Helper()
	f, err := fake.LoadFile("../../testdata/feeds/acme-story.json")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func config(dbURL, supplierURL string) Config {
	return Config{
		Addr:        "127.0.0.1:0",
		DatabaseURL: dbURL,
		Supplier:    supplier.ClientConfig{BaseURL: supplierURL, Attempts: 2, Backoff: time.Millisecond},
		Importer:    importer.Config{PageSize: 1, MaxRefills: 3},
	}
}

func newApp(t *testing.T, cfg Config) *App {
	t.Helper()
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// serve runs the app until the returned stop function is called.
func serve(t *testing.T, a *App) (baseURL string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()
	return "http://" + ln.Addr().String(), func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}
}

func TestHealthz(t *testing.T) {
	sup := httptest.NewServer(fake.NewServer(storyFeed(t), fake.Options{}))
	defer sup.Close()
	a := newApp(t, config(testdb.URL(t), sup.URL))
	base, stop := serve(t, a)
	defer stop()

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// Ctrl+C during an update: it is saved as interrupted, and after the next
// start it can be resumed and completes, keeping the work already done.
func TestGracefulShutdownInterruptsThenResumeAfterRestart(t *testing.T) {
	sup := httptest.NewServer(fake.NewServer(storyFeed(t), fake.Options{Delay: 40 * time.Millisecond}))
	defer sup.Close()
	cfg := config(testdb.URL(t), sup.URL)
	ctx := context.Background()

	first := newApp(t, cfg)
	_, stop := serve(t, first)
	u, err := first.Service.Start(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond) // a few batches in
	stop()                             // like Ctrl+C

	second := newApp(t, cfg) // restart
	defer second.close()
	got, _ := second.Store.GetUpdate(ctx, u.ID)
	if got.Status != importer.StatusInterrupted {
		t.Fatalf("status after shutdown = %s, want interrupted", got.Status)
	}
	if got.Bookmark == 0 || got.Bookmark >= 8 {
		t.Fatalf("bookmark = %d, want some batches done but not all", got.Bookmark)
	}
	done := got.Bookmark

	if _, err := second.Service.Resume(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	second.Service.Wait()
	got, _ = second.Store.GetUpdate(ctx, u.ID)
	if got.Status != importer.StatusCompleted || got.Bookmark != 8 {
		t.Fatalf("after resume: %+v", got)
	}
	if got.BatchesDone != 8 {
		t.Errorf("batches done = %d, want 8 (%d before + %d after, none twice)", got.BatchesDone, done, 8-done)
	}
}

// A hard crash leaves the update "running" in the database. Starting the
// application again must report it as interrupted, never as running.
func TestStartupRecoversUpdatesLeftRunning(t *testing.T) {
	sup := httptest.NewServer(fake.NewServer(storyFeed(t), fake.Options{}))
	defer sup.Close()
	cfg := config(testdb.URL(t), sup.URL)
	ctx := context.Background()

	crashed := newApp(t, cfg)
	u, err := crashed.Store.CreateUpdate(ctx, importer.Update{Status: importer.StatusRunning, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	crashed.pool.Close() // process gone, nothing else saved

	restarted := newApp(t, cfg)
	defer restarted.close()
	got, _ := restarted.Store.GetUpdate(ctx, u.ID)
	if got.Status != importer.StatusInterrupted {
		t.Fatalf("status = %s, want interrupted", got.Status)
	}
}
