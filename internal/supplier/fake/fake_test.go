package fake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/supplier"
)

func TestGenerateIsRepeatable(t *testing.T) {
	a := Generate(GenerateOptions{Seed: 1})
	b := Generate(GenerateOptions{Seed: 1})
	c := Generate(GenerateOptions{Seed: 2})

	if !reflect.DeepEqual(a, b) {
		t.Error("the same seed must give the same feed")
	}
	if reflect.DeepEqual(a.Batches, c.Batches) {
		t.Error("different seeds should give different feeds")
	}
}

// Every kind of mess the importer must handle appears in generated feeds.
func TestGeneratedFeedsAreMessy(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		f := Generate(GenerateOptions{Seed: seed})
		s := f.Stats
		checks := map[string]int{
			"late": s.Late, "repeated": s.Repeated, "invalid": s.Invalid,
			"version gaps": s.VersionGaps, "withdrawals": s.Withdrawals, "restocks": s.Restocks,
			"shuffled batches": s.Shuffled,
		}
		for name, n := range checks {
			if n == 0 {
				t.Errorf("seed %d: no %s", seed, name)
			}
		}
		if s.Currencies < 2 {
			t.Errorf("seed %d: only %d currency", seed, s.Currencies)
		}
		for i, b := range f.Batches {
			if b.Seq != int64(i+1) {
				t.Fatalf("seed %d: batch %d has seq %d", seed, i, b.Seq)
			}
		}
		if s.Delivered <= s.Changes {
			t.Errorf("seed %d: delivered %d <= changes %d; expected repeats", seed, s.Delivered, s.Changes)
		}
	}
}

// Delivery mess must not change the truth: for each product the newest
// valid version in the feed is the newest version the supplier made.
func TestExpectedMatchesSupplierTruth(t *testing.T) {
	f := Generate(GenerateOptions{Seed: 3})
	exp := ExpectedFor(f)

	newest := map[string]supplier.WireChange{}
	for _, b := range f.Batches {
		for _, w := range b.Changes {
			if w.ToChange().Validate() != nil {
				continue
			}
			if cur, ok := newest[w.SKU]; !ok || w.Version > cur.Version {
				newest[w.SKU] = w
			}
		}
	}
	if len(exp.Products) != len(newest) {
		t.Fatalf("expected %d products, truth has %d", len(exp.Products), len(newest))
	}
	for sku, w := range newest {
		if exp.Products[sku].Version != w.Version {
			t.Errorf("%s: expected v%d, truth v%d", sku, exp.Products[sku].Version, w.Version)
		}
	}
}

func TestStoryFeedExpectations(t *testing.T) {
	f, err := LoadFile("../../../testdata/feeds/acme-story.json")
	if err != nil {
		t.Fatal(err)
	}
	exp := ExpectedFor(f)

	want := map[string]ExpectedProduct{
		"MUG-BLUE":      {Status: catalog.StatusActive, Version: 3, PriceCents: 1500},
		"LAMP-RED":      {Status: catalog.StatusActive, Version: 5, PriceCents: 3200},
		"CHAIR-OAK":     {Status: catalog.StatusActive, Version: 4, PriceCents: 9500},
		"VASE-GLASS":    {Status: catalog.StatusActive, Version: 3, PriceCents: 2700},
		"CUSHION-LINEN": {Status: catalog.StatusActive, Version: 2, PriceCents: 1900},
		"TOWEL-COTTON":  {Status: catalog.StatusActive, Version: 1, PriceCents: 900},
		"KETTLE-STEEL":  {Status: catalog.StatusWithdrawn, Version: 3},
	}
	if len(exp.Products) != len(want) {
		t.Errorf("products = %d, want %d", len(exp.Products), len(want))
	}
	for sku, w := range want {
		got := exp.Products[sku]
		if got.Status != w.Status || got.Version != w.Version || got.PriceCents != w.PriceCents {
			t.Errorf("%s = %+v, want %+v", sku, got, w)
		}
	}
	wantProblems := map[ProblemKey]bool{{"", 1}: true, {"TRAY-BAMBOO", 1}: true}
	if !reflect.DeepEqual(exp.OpenProblems, wantProblems) {
		t.Errorf("open problems = %v, want %v", exp.OpenProblems, wantProblems)
	}
}

func get(t *testing.T, h http.Handler, method, url string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, url, nil))
	if out != nil && rec.Code == http.StatusOK {
		json.Unmarshal(rec.Body.Bytes(), out)
	}
	return rec.Code
}

func TestServerPublishGrowAndOutages(t *testing.T) {
	f := Generate(GenerateOptions{Seed: 1, Batches: 10})
	s := NewServer(f, Options{Publish: 2, Grow: 1})

	var latest supplier.LatestResponse
	get(t, s, "GET", "/feed/latest", &latest)
	if latest.Latest != 2 {
		t.Fatalf("latest = %d, want 2 visible", latest.Latest)
	}

	var resp supplier.BatchesResponse
	get(t, s, "GET", "/feed/batches?after=0&limit=10", &resp)
	if len(resp.Batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(resp.Batches))
	}
	if s.Latest() != 3 {
		t.Errorf("after one read the feed should grow to 3, got %d", s.Latest())
	}

	get(t, s, "POST", "/admin/publish?count=2", nil)
	if s.Latest() != 5 {
		t.Errorf("latest after publish = %d, want 5", s.Latest())
	}

	get(t, s, "POST", "/admin/outage?from=4", nil)
	if code := get(t, s, "GET", "/feed/batches?after=1&limit=2", nil); code != http.StatusOK {
		t.Errorf("batches 2–3 before the outage point: status %d", code)
	}
	var partial supplier.BatchesResponse
	if code := get(t, s, "GET", "/feed/batches?after=2&limit=5", &partial); code != http.StatusOK || len(partial.Batches) != 1 || partial.Batches[0].Seq != 3 {
		t.Errorf("page crossing the outage point: status %d, %d batches; want only batch 3", code, len(partial.Batches))
	}
	if code := get(t, s, "GET", "/feed/batches?after=3&limit=5", nil); code != http.StatusServiceUnavailable {
		t.Errorf("starting at batch 4: status %d, want 503", code)
	}

	get(t, s, "POST", "/admin/outage?on=true", nil)
	if code := get(t, s, "GET", "/feed/latest", nil); code != http.StatusServiceUnavailable {
		t.Errorf("full outage: status %d, want 503", code)
	}
	get(t, s, "POST", "/admin/outage?on=false", nil)
	if code := get(t, s, "GET", "/feed/latest", nil); code != http.StatusOK {
		t.Errorf("after outage: status %d", code)
	}
	if code := get(t, s, "GET", "/feed/batches?after=x", nil); code != http.StatusBadRequest {
		t.Errorf("bad query: status %d, want 400", code)
	}
}
