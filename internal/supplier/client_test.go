package supplier_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/supplier"
	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
)

func story(t *testing.T) fake.Feed {
	t.Helper()
	f, err := fake.LoadFile("../../testdata/feeds/acme-story.json")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func client(url string) *supplier.Client {
	return supplier.NewClient(supplier.ClientConfig{BaseURL: url, Attempts: 4, Backoff: time.Millisecond})
}

func TestClientReadsTheFeed(t *testing.T) {
	srv := httptest.NewServer(fake.NewServer(story(t), fake.Options{}))
	defer srv.Close()
	c := client(srv.URL)
	ctx := context.Background()

	latest, err := c.Latest(ctx)
	if err != nil || latest != 8 {
		t.Fatalf("latest = %d, %v; want 8", latest, err)
	}
	batches, err := c.Batches(ctx, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 || batches[0].Seq != 3 || batches[2].Seq != 5 {
		t.Fatalf("got %d batches starting at %d", len(batches), batches[0].Seq)
	}
	first := batches[0].Changes[0]
	if first.SKU != "MUG-BLUE" || first.Version != 3 || first.PriceCents != 1500 || first.Currency != "EUR" {
		t.Errorf("first change = %+v", first)
	}
	if got, _ := c.Batches(ctx, 8, 5); len(got) != 0 {
		t.Errorf("after the last batch: got %d batches, want 0", len(got))
	}
}

func TestClientRetriesTemporaryErrors(t *testing.T) {
	// Half of the requests fail; with retries every call still succeeds.
	srv := httptest.NewServer(fake.NewServer(story(t), fake.Options{FailRate: 0.5, Seed: 7}))
	defer srv.Close()
	c := supplier.NewClient(supplier.ClientConfig{BaseURL: srv.URL, Attempts: 10, Backoff: time.Millisecond})

	for i := 0; i < 20; i++ {
		if _, err := c.Batches(context.Background(), 0, 2); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestClientGivesUpDuringOutage(t *testing.T) {
	fs := fake.NewServer(story(t), fake.Options{})
	fs.SetOutage(true)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fs.ServeHTTP(w, r)
	}))
	defer srv.Close()

	_, err := client(srv.URL).Latest(context.Background())
	if err == nil {
		t.Fatal("expected an error during an outage")
	}
	if hits.Load() != 4 {
		t.Errorf("requests = %d, want 4 attempts", hits.Load())
	}
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := client(srv.URL).Latest(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want 1 (no retry on 404)", hits.Load())
	}
}

func TestClientStopsWhenCancelled(t *testing.T) {
	srv := httptest.NewServer(fake.NewServer(story(t), fake.Options{Delay: time.Second}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := client(srv.URL).Latest(ctx); err == nil {
		t.Fatal("expected an error")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("client did not stop promptly after cancellation")
	}
}

func TestClientUnreachableSupplier(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more

	if _, err := client(url).Latest(context.Background()); err == nil {
		t.Fatal("expected an error when the supplier is unreachable")
	}
}
