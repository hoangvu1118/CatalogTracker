package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// stub implements Updater and Reader with fixed data.
type stub struct {
	updates   map[int64]importer.Update
	startErr  error
	started   []bool // from_beginning of each Start call
	latest    int64  // supplier's newest batch
	latestErr error
	fetched   int64 // newest batch downloaded by the running update
}

func newStub() *stub {
	return &stub{updates: map[int64]importer.Update{
		1: {ID: 1, Status: importer.StatusCompleted, Bookmark: 8, BatchesDone: 8, StartedAt: time.Unix(0, 0)},
		2: {ID: 2, Status: importer.StatusFailed, Bookmark: 3, Target: 8, BatchesDone: 3, Error: "supplier unavailable", StartedAt: time.Unix(0, 0)},
	}}
}

func (s *stub) Start(_ context.Context, fromBeginning bool) (importer.Update, error) {
	s.started = append(s.started, fromBeginning)
	if s.startErr != nil {
		return importer.Update{}, s.startErr
	}
	return importer.Update{ID: 3, Status: importer.StatusRunning}, nil
}

func (s *stub) Resume(_ context.Context, id int64) (importer.Update, error) {
	u, ok := s.updates[id]
	if !ok {
		return importer.Update{}, importer.ErrNotFound
	}
	if !u.Status.CanResume() {
		return importer.Update{}, importer.ErrNotResumable
	}
	u.Status = importer.StatusRunning
	return u, nil
}

func (s *stub) GetUpdate(_ context.Context, id int64) (importer.Update, error) {
	u, ok := s.updates[id]
	if !ok {
		return importer.Update{}, importer.ErrNotFound
	}
	return u, nil
}

func (s *stub) ListUpdates(_ context.Context, limit int) ([]importer.Update, error) {
	var out []importer.Update
	for id := int64(len(s.updates)); id >= 1 && len(out) < limit; id-- {
		out = append(out, s.updates[id])
	}
	return out, nil
}

func (s *stub) LastBookmark(context.Context) (int64, error) {
	var b int64
	for _, u := range s.updates {
		b = max(b, u.Bookmark)
	}
	return b, nil
}

func (s *stub) Latest(context.Context) (int64, error) {
	return s.latest, s.latestErr
}

func (s *stub) Fetched(int64) int64 {
	return s.fetched
}

func (s *stub) UnfinishedUpdate(context.Context) (importer.Update, bool, error) {
	return s.updates[2], true, nil
}

func (s *stub) ChangeCounts(_ context.Context, id int64) (map[catalog.Outcome]int, error) {
	if id == 1 {
		return map[catalog.Outcome]int{catalog.OutcomeCreated: 7, catalog.OutcomeStale: 3}, nil
	}
	return map[catalog.Outcome]int{}, nil
}

func (s *stub) ListChanges(_ context.Context, id int64, outcome catalog.Outcome, limit int) ([]importer.ChangeRecord, error) {
	all := []importer.ChangeRecord{
		{UpdateID: id, BatchSeq: 1, SKU: "MUG-BLUE", Version: 1, Outcome: catalog.OutcomeCreated, Detail: "Blue Mug, 10.00 EUR"},
		{UpdateID: id, BatchSeq: 4, SKU: "LAMP-RED", Version: 1, Outcome: catalog.OutcomeStale, Detail: "v1 is older than stored v2"},
	}
	var out []importer.ChangeRecord
	for _, c := range all {
		if (outcome == "" || c.Outcome == outcome) && len(out) < limit {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *stub) ListProblems(_ context.Context, id int64, openOnly bool) ([]importer.Problem, error) {
	resolved := time.Unix(10, 0)
	all := []importer.Problem{
		{ID: 1, UpdateID: 1, SKU: "VASE-GLASS", Version: 2, Kind: importer.ProblemInvalidChange, Message: "missing name", ResolvedAt: &resolved},
		{ID: 2, UpdateID: 2, Kind: importer.ProblemSupplierUnavailable, Message: "503"},
	}
	var out []importer.Problem
	for _, p := range all {
		if (id == 0 || p.UpdateID == id) && (!openOnly || p.ResolvedAt == nil) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *stub) ListProducts(_ context.Context, status catalog.Status) ([]catalog.Product, error) {
	all := []catalog.Product{
		{SKU: "LAMP-RED", Name: "Red Lamp", Status: catalog.StatusWithdrawn, Version: 2},
		{SKU: "MUG-BLUE", Name: "Blue Mug", PriceCents: 1500, Currency: "EUR", Status: catalog.StatusActive, Version: 3},
	}
	var out []catalog.Product
	for _, p := range all {
		if status == "" || p.Status == status {
			out = append(out, p)
		}
	}
	return out, nil
}

func call(t *testing.T, s *stub, method, path, body string, out any) int {
	t.Helper()
	mux := http.NewServeMux()
	New(s, s, WindowSource{Feed: s, Progress: s, PageSize: 2, MaxRefills: 3}).Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	mux.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: bad JSON %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

func TestStartUpdate(t *testing.T) {
	s := newStub()
	var u updateJSON
	if code := call(t, s, "POST", "/api/updates", "", &u); code != http.StatusAccepted || u.ID != 3 || u.Status != "running" {
		t.Fatalf("code %d, update %+v", code, u)
	}
	call(t, s, "POST", "/api/updates", `{"from_beginning": true}`, nil)
	if fmt.Sprint(s.started) != "[false true]" {
		t.Errorf("from_beginning values = %v", s.started)
	}
	if code := call(t, s, "POST", "/api/updates", `not json`, nil); code != http.StatusBadRequest {
		t.Errorf("bad body: code %d", code)
	}
}

func TestStartWhileUnfinishedTellsWhichToResume(t *testing.T) {
	s := newStub()
	s.startErr = importer.ErrUnfinishedUpdate
	var resp map[string]any
	code := call(t, s, "POST", "/api/updates", "", &resp)
	if code != http.StatusConflict || resp["unfinished_update_id"] != float64(2) {
		t.Fatalf("code %d, body %v", code, resp)
	}
}

func TestResumeUpdate(t *testing.T) {
	s := newStub()
	cases := []struct {
		path string
		want int
	}{
		{"/api/updates/2/resume", http.StatusAccepted},
		{"/api/updates/1/resume", http.StatusConflict}, // completed
		{"/api/updates/9/resume", http.StatusNotFound},
		{"/api/updates/x/resume", http.StatusBadRequest},
	}
	for _, c := range cases {
		if code := call(t, s, "POST", c.path, "", nil); code != c.want {
			t.Errorf("%s: code %d, want %d", c.path, code, c.want)
		}
	}
}

func TestGetUpdateShowsProgressCountsAndProblems(t *testing.T) {
	s := newStub()

	var failed updateJSON
	call(t, s, "GET", "/api/updates/2", "", &failed)
	if failed.Status != "failed" || !failed.CanResume || failed.NextBatch != 4 || failed.OpenProblems != 1 || failed.Error == "" {
		t.Errorf("failed update = %+v", failed)
	}

	var done updateJSON
	call(t, s, "GET", "/api/updates/1", "", &done)
	if done.CanResume || done.Changes["created"] != 7 || done.Changes["stale"] != 3 || done.Changes["updated"] != 0 || done.OpenProblems != 0 {
		t.Errorf("completed update = %+v", done)
	}

	if code := call(t, s, "GET", "/api/updates/9", "", nil); code != http.StatusNotFound {
		t.Errorf("missing update: code %d", code)
	}
}

func TestListEndpoints(t *testing.T) {
	s := newStub()

	var updates []updateJSON
	call(t, s, "GET", "/api/updates", "", &updates)
	if len(updates) != 2 || updates[0].ID != 2 {
		t.Errorf("updates = %+v", updates)
	}

	var changes []changeJSON
	call(t, s, "GET", "/api/updates/1/changes?outcome=stale", "", &changes)
	if len(changes) != 1 || changes[0].SKU != "LAMP-RED" || changes[0].BatchSeq != 4 {
		t.Errorf("stale changes = %+v", changes)
	}

	var problems []problemJSON
	call(t, s, "GET", "/api/problems?open=true", "", &problems)
	if len(problems) != 1 || problems[0].Kind != "supplier_unavailable" || !problems[0].Open {
		t.Errorf("open problems = %+v", problems)
	}
	call(t, s, "GET", "/api/updates/1/problems", "", &problems)
	if len(problems) != 1 || problems[0].Open {
		t.Errorf("update 1 problems = %+v", problems)
	}

	var products []productJSON
	call(t, s, "GET", "/api/products?status=withdrawn", "", &products)
	if len(products) != 1 || products[0].SKU != "LAMP-RED" {
		t.Errorf("withdrawn products = %+v", products)
	}
	if code := call(t, s, "GET", "/api/products?status=gone", "", nil); code != http.StatusBadRequest {
		t.Errorf("bad status filter: code %d", code)
	}
}

func TestWindowWhileRunning(t *testing.T) {
	s := newStub()
	s.updates[2] = importer.Update{ID: 2, Status: importer.StatusRunning, Bookmark: 3, Target: 8, Refills: 1}
	s.latest = 11
	s.fetched = 5

	var w windowJSON
	call(t, s, "GET", "/api/window", "", &w)

	// Window 2 = batches 4–8: 4–5 downloaded (4 being saved), 6–8 still at
	// the supplier. Window 1 = batches 9–11.
	want := windowJSON{UpdateID: 2, Status: "running", Bookmark: 3, Target: 8, Processing: 4, Fetched: 5,
		PageSize: 2, Refills: 1, MaxRefills: 3, SupplierLatest: 11}
	if w != want {
		t.Errorf("got %+v, want %+v", w, want)
	}

	// Between pages nothing is in memory: nothing is being saved.
	s.fetched = 3
	call(t, s, "GET", "/api/window", "", &w)
	if w.Processing != 0 {
		t.Errorf("waiting for the next page: processing = %d, want 0", w.Processing)
	}
}

func TestWindowWhenFailedKeepsWindowTwo(t *testing.T) {
	s := newStub()
	s.latest = 8

	var w windowJSON
	call(t, s, "GET", "/api/window", "", &w)

	if w.Status != "failed" || w.Bookmark != 3 || w.Target != 8 || w.Processing != 0 {
		t.Errorf("got %+v: failed update keeps batches 4–8 waiting, none being saved", w)
	}
}

func TestWindowWhenUpToDate(t *testing.T) {
	s := newStub()
	delete(s.updates, 2)
	s.latest = 8

	var w windowJSON
	call(t, s, "GET", "/api/window", "", &w)

	if w.Status != "completed" || w.Bookmark != 8 || w.Target != 0 || w.SupplierLatest != 8 {
		t.Errorf("got %+v: both windows should be empty", w)
	}
}

func TestWindowWhenSupplierIsDown(t *testing.T) {
	s := newStub()
	s.updates = map[int64]importer.Update{}
	s.latestErr = fmt.Errorf("503")

	var w windowJSON
	if code := call(t, s, "GET", "/api/window", "", &w); code != http.StatusOK {
		t.Fatalf("code %d: the page must still get an answer", code)
	}
	if w.Status != "idle" || w.SupplierError == "" {
		t.Errorf("got %+v", w)
	}
}
