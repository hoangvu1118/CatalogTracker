// Package e2e tests the whole system through its HTTP API: the real
// application and database, with the fictional supplier misbehaving.
// Every scenario ends by comparing the catalog with fake.ExpectedFor,
// which computes the right answer without the importer.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/app"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
	"github.com/hoangvu1118/catalogtracker/internal/supplier"
	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
	"github.com/hoangvu1118/catalogtracker/internal/testdb"
)

// API shapes, decoded independently of the application's own types.

type update struct {
	ID            int64          `json:"id"`
	Status        string         `json:"status"`
	CanResume     bool           `json:"can_resume"`
	StartBookmark int64          `json:"start_bookmark"`
	Bookmark      int64          `json:"bookmark"`
	NextBatch     int64          `json:"next_batch"`
	Target        int64          `json:"target"`
	Refills       int            `json:"refills"`
	BatchesDone   int            `json:"batches_done"`
	Changes       map[string]int `json:"changes"`
	OpenProblems  int            `json:"open_problems"`
	Error         string         `json:"error"`
	FinishedAt    *time.Time     `json:"finished_at"`
}

func (u update) applied() int {
	return u.Changes["created"] + u.Changes["updated"] + u.Changes["withdrawn"] + u.Changes["restocked"]
}

type product struct {
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	Currency   string `json:"currency"`
	Status     string `json:"status"`
	Version    int64  `json:"version"`
}

type problem struct {
	SKU     string `json:"sku"`
	Version int64  `json:"version"`
	Kind    string `json:"kind"`
	Open    bool   `json:"open"`
}

type change struct {
	Batch   int64  `json:"batch"`
	SKU     string `json:"sku"`
	Version int64  `json:"version"`
	Outcome string `json:"outcome"`
}

// env is one running system: database, fictional supplier and application.
type env struct {
	t        *testing.T
	feed     fake.Feed
	supplier *fake.Server
	cfg      app.Config
	base     string
	stopApp  func()
}

type setup struct {
	supplier fake.Options
	importer importer.Config
	attempts int
}

func newEnv(t *testing.T, feed fake.Feed, s setup) *env {
	t.Helper()
	sup := fake.NewServer(feed, s.supplier)
	supSrv := httptest.NewServer(sup)
	t.Cleanup(supSrv.Close)

	if s.importer.PageSize == 0 {
		s.importer = importer.DefaultConfig()
	}
	if s.attempts == 0 {
		s.attempts = 2
	}
	e := &env{
		t:        t,
		feed:     feed,
		supplier: sup,
		cfg: app.Config{
			DatabaseURL: testdb.URL(t),
			Supplier:    supplier.ClientConfig{BaseURL: supSrv.URL, Attempts: s.attempts, Backoff: 2 * time.Millisecond, Timeout: 5 * time.Second},
			Importer:    s.importer,
		},
	}
	e.startApp()
	t.Cleanup(func() { e.stopApp() })
	return e
}

// startApp starts the application, as after a (re)start of the process.
func (e *env) startApp() {
	e.t.Helper()
	a, err := app.New(context.Background(), e.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		e.t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Serve(ctx, ln); close(done) }()
	e.base = "http://" + ln.Addr().String()
	stopped := false
	e.stopApp = func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
}

// restartApp stops the application (like Ctrl+C) and starts it again.
func (e *env) restartApp() {
	e.stopApp()
	e.startApp()
}

func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.base+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (e *env) start(fromBeginning bool) update {
	e.t.Helper()
	var u update
	if code := e.do("POST", "/api/updates", map[string]bool{"from_beginning": fromBeginning}, &u); code != http.StatusAccepted {
		e.t.Fatalf("start: status %d", code)
	}
	return u
}

func (e *env) resume(id int64) update {
	e.t.Helper()
	var u update
	if code := e.do("POST", fmt.Sprintf("/api/updates/%d/resume", id), nil, &u); code != http.StatusAccepted {
		e.t.Fatalf("resume %d: status %d", id, code)
	}
	return u
}

func (e *env) get(id int64) update {
	e.t.Helper()
	var u update
	e.do("GET", fmt.Sprintf("/api/updates/%d", id), nil, &u)
	return u
}

// wait polls until the update is no longer running.
func (e *env) wait(id int64) update {
	e.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if u := e.get(id); u.Status != "running" {
			return u
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("update %d still running after 60s", id)
	return update{}
}

// runToCompletion starts an update and resumes it until it completes.
func (e *env) runToCompletion(fromBeginning bool) update {
	e.t.Helper()
	u := e.wait(e.start(fromBeginning).ID)
	for i := 0; u.Status != "completed"; i++ {
		if !u.CanResume || i > 200 {
			e.t.Fatalf("update %d stuck: %+v", u.ID, u)
		}
		u = e.wait(e.resume(u.ID).ID)
	}
	return u
}

func (e *env) products() map[string]product {
	e.t.Helper()
	var list []product
	e.do("GET", "/api/products", nil, &list)
	out := make(map[string]product, len(list))
	for _, p := range list {
		out[p.SKU] = p
	}
	return out
}

func (e *env) openProblems() []problem {
	e.t.Helper()
	var list []problem
	e.do("GET", "/api/problems?open=true", nil, &list)
	return list
}

func (e *env) changes(id int64) []change {
	e.t.Helper()
	var list []change
	e.do("GET", fmt.Sprintf("/api/updates/%d/changes?limit=5000", id), nil, &list)
	return list
}

// assertMatchesExpected compares the catalog and open invalid-record
// problems with what the feed says they must be.
func (e *env) assertMatchesExpected(exp fake.Expected) {
	e.t.Helper()
	got := e.products()
	if len(got) != len(exp.Products) {
		e.t.Errorf("catalog has %d products, expected %d", len(got), len(exp.Products))
	}
	for sku, want := range exp.Products {
		p, ok := got[sku]
		switch {
		case !ok:
			e.t.Errorf("%s: missing", sku)
		case p.Status != string(want.Status) || p.Version != want.Version:
			e.t.Errorf("%s: got %s v%d, expected %s v%d", sku, p.Status, p.Version, want.Status, want.Version)
		case want.Status == "active" && (p.Name != want.Name || p.PriceCents != want.PriceCents || p.Currency != want.Currency):
			e.t.Errorf("%s: got %q %d %s, expected %q %d %s", sku, p.Name, p.PriceCents, p.Currency, want.Name, want.PriceCents, want.Currency)
		}
	}

	open := map[fake.ProblemKey]bool{}
	for _, p := range e.openProblems() {
		if p.Kind == "invalid_change" {
			open[fake.ProblemKey{SKU: p.SKU, Version: p.Version}] = true
		} else {
			e.t.Errorf("unexpected open problem: %+v", p)
		}
	}
	if fmt.Sprint(sortedKeys(open)) != fmt.Sprint(sortedKeys(exp.OpenProblems)) {
		e.t.Errorf("open problems = %v, expected %v", sortedKeys(open), sortedKeys(exp.OpenProblems))
	}
}

func sortedKeys(m map[fake.ProblemKey]bool) []string {
	var out []string
	for k := range m {
		out = append(out, fmt.Sprintf("%s@v%d", k.SKU, k.Version))
	}
	sort.Strings(out)
	return out
}

func storyFeed(t *testing.T) fake.Feed {
	t.Helper()
	f, err := fake.LoadFile("../testdata/feeds/acme-story.json")
	if err != nil {
		t.Fatal(err)
	}
	return f
}
