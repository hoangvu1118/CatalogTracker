package e2e

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/importer"
	"github.com/hoangvu1118/catalogtracker/internal/supplier"
	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
)

// The demo story from start to finish: outage in the middle, resume,
// then every rule checked against the expected catalog (R1–R6).
func TestStoryWithOutageAndResume(t *testing.T) {
	e := newEnv(t, storyFeed(t), setup{importer: importer.Config{PageSize: 2, MaxRefills: 3}})
	e.supplier.FailFrom(5)

	u := e.wait(e.start(false).ID)

	// R6: stopped part-way, never reported complete.
	if u.Status != "failed" || u.FinishedAt != nil || !u.CanResume || u.Bookmark != 4 || u.NextBatch != 5 {
		t.Fatalf("after outage: %+v", u)
	}
	// R4: what was saved before the outage is all there.
	before := e.products()
	if len(before) != 5 || before["LAMP-RED"].Status != "withdrawn" || before["MUG-BLUE"].Version != 3 {
		t.Fatalf("catalog after outage: %+v", before)
	}
	// Staff must resume, not start another update.
	var conflict map[string]any
	if code := e.do("POST", "/api/updates", nil, &conflict); code != http.StatusConflict || conflict["unfinished_update_id"] != float64(u.ID) {
		t.Fatalf("start during failed update: %d %v", code, conflict)
	}

	// R5: resume continues at batch 5 and finishes.
	e.supplier.SetOutage(false)
	u = e.wait(e.resume(u.ID).ID)

	if u.Status != "completed" || u.Bookmark != 8 || u.BatchesDone != 8 || u.FinishedAt == nil {
		t.Fatalf("after resume: %+v", u)
	}
	want := map[string]int{"created": 6, "updated": 5, "withdrawn": 2, "restocked": 1, "duplicate": 2, "stale": 3}
	for k, v := range want {
		if u.Changes[k] != v {
			t.Errorf("%s = %d, want %d", k, u.Changes[k], v)
		}
	}
	// Each batch's changes were logged exactly once despite the outage.
	perBatch := map[int64]int{}
	for _, c := range e.changes(u.ID) {
		perBatch[c.Batch]++
	}
	for _, b := range e.feed.Batches {
		valid := 0
		for _, c := range b.Changes {
			if c.ToChange().Validate() == nil {
				valid++
			}
		}
		if perBatch[b.Seq] < valid || perBatch[b.Seq] > len(b.Changes) {
			t.Errorf("batch %d logged %d changes, has %d (%d valid)", b.Seq, perBatch[b.Seq], len(b.Changes), valid)
		}
	}
	e.assertMatchesExpected(fake.ExpectedFor(e.feed))
}

// R1: importing everything again, twice, changes nothing.
func TestReimportChangesNothing(t *testing.T) {
	e := newEnv(t, fake.Generate(fake.GenerateOptions{Seed: 11}), setup{})
	e.runToCompletion(false)
	before := e.products()

	for i := 0; i < 2; i++ {
		u := e.runToCompletion(true)
		if u.applied() != 0 {
			t.Errorf("re-import %d applied %d changes: %v", i+1, u.applied(), u.Changes)
		}
		if u.Changes["duplicate"] == 0 || u.Changes["stale"] == 0 {
			t.Errorf("re-import %d: expected duplicates and stale changes: %v", i+1, u.Changes)
		}
	}
	if fmt.Sprint(e.products()) != fmt.Sprint(before) {
		t.Error("catalog changed after re-import")
	}
	e.assertMatchesExpected(fake.ExpectedFor(e.feed))
}

// Realistic messy feeds, a flaky supplier that answers 503 a quarter of
// the time, and different page sizes: the final catalog must always be
// exactly what the feed says.
func TestGeneratedFeedsWithFlakySupplier(t *testing.T) {
	totalResumes := 0
	defer func() {
		if totalResumes < 5 {
			t.Errorf("only %d outages across all feeds; the test did not exercise resume", totalResumes)
		}
	}()
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			feed := fake.Generate(fake.GenerateOptions{Seed: seed, Products: 30 + int(seed)*10, Batches: 20 + int(seed)*5})
			pageSize := []int{1, 3, 5, 7, 10, 50}[seed-1]
			e := newEnv(t, feed, setup{
				supplier: fake.Options{FailRate: 0.25, Seed: seed},
				importer: importer.Config{PageSize: pageSize, MaxRefills: 3},
				attempts: 1, // no client retries: every 503 fails the update
			})

			u := e.wait(e.start(false).ID)
			resumes := 0
			for u.Status != "completed" {
				if !u.CanResume || resumes > 100 {
					t.Fatalf("not completing after %d resumes: %+v", resumes, u)
				}
				resumes++
				u = e.wait(e.resume(u.ID).ID)
			}
			totalResumes += resumes

			if u.Bookmark != int64(len(feed.Batches)) || u.BatchesDone != len(feed.Batches) {
				t.Errorf("bookmark %d, batches %d; feed has %d", u.Bookmark, u.BatchesDone, len(feed.Batches))
			}
			e.assertMatchesExpected(fake.ExpectedFor(feed))
			t.Logf("%d batches, %d messages (late %d, repeated %d, invalid %d), page size %d, %d resumes after outages",
				len(feed.Batches), feed.Stats.Delivered, feed.Stats.Late, feed.Stats.Repeated, feed.Stats.Invalid, pageSize, resumes)
		})
	}
}

// R4, R5, R6: the application is stopped again and again in the middle of
// updates. After each restart the update is interrupted, never completed;
// resuming eventually finishes with the right catalog.
func TestRepeatedRestartsDuringUpdate(t *testing.T) {
	feed := fake.Generate(fake.GenerateOptions{Seed: 21, Batches: 40})
	e := newEnv(t, feed, setup{
		supplier: fake.Options{Delay: 5 * time.Millisecond},
		importer: importer.Config{PageSize: 2, MaxRefills: 3},
	})
	rng := rand.New(rand.NewPCG(21, 21))

	id := e.start(false).ID
	restarts := 0
	for {
		time.Sleep(time.Duration(10+rng.IntN(40)) * time.Millisecond)
		e.restartApp()
		u := e.get(id)
		if u.Status == "completed" {
			break
		}
		restarts++
		if u.Status != "interrupted" || u.FinishedAt != nil || restarts > 500 {
			t.Fatalf("after restart %d: %+v", restarts, u)
		}
		e.resume(id)
	}

	u := e.get(id)
	if u.BatchesDone != len(feed.Batches) {
		t.Errorf("batches done %d, feed has %d: a batch was lost or saved twice", u.BatchesDone, len(feed.Batches))
	}
	if restarts < 3 {
		t.Errorf("only %d restarts happened mid-update; the test did not exercise recovery", restarts)
	}
	e.assertMatchesExpected(fake.ExpectedFor(feed))
	t.Logf("completed after %d restarts", restarts)
}

// D5: a supplier that keeps publishing while we read. Each update refills
// Window 2 at most 3 times and then completes; later updates catch up.
func TestSupplierThatKeepsChanging(t *testing.T) {
	feed := fake.Generate(fake.GenerateOptions{Seed: 5, Batches: 60})
	e := newEnv(t, feed, setup{
		supplier: fake.Options{Publish: 5, Grow: 2},
		importer: importer.Config{PageSize: 2, MaxRefills: 3},
	})

	first := e.wait(e.start(false).ID)
	if first.Status != "completed" || first.Refills != 3 {
		t.Fatalf("first update: %+v", first)
	}
	if first.Bookmark >= int64(len(feed.Batches)) {
		t.Fatalf("expected batches left for the next update; bookmark %d of %d", first.Bookmark, len(feed.Batches))
	}

	var last update
	for i := 0; i < 50; i++ {
		last = e.wait(e.start(false).ID)
		if last.Status != "completed" {
			t.Fatalf("update %d: %+v", i+2, last)
		}
		if last.Bookmark == int64(len(feed.Batches)) {
			break
		}
	}
	if last.Bookmark != int64(len(feed.Batches)) {
		t.Fatalf("never caught up: bookmark %d of %d", last.Bookmark, len(feed.Batches))
	}
	e.assertMatchesExpected(fake.ExpectedFor(feed))
}

// Many staff members pressing "Start update" at the same moment: exactly
// one update starts.
func TestConcurrentStartRequests(t *testing.T) {
	e := newEnv(t, storyFeed(t), setup{supplier: fake.Options{Delay: 30 * time.Millisecond}})

	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.do("POST", "/api/updates", nil, nil)
		}()
	}
	wg.Wait()

	accepted := 0
	for _, c := range codes {
		switch c {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d updates started, want exactly 1 (codes %v)", accepted, codes)
	}
	var list []update
	e.do("GET", "/api/updates", nil, &list)
	e.wait(list[0].ID)
	e.assertMatchesExpected(fake.ExpectedFor(e.feed))
}

// The supplier is down from the very start, and still down at the first
// resume: the catalog is untouched, the update stays resumable, and only
// one "supplier unavailable" problem is shown.
func TestSupplierDownFromTheStart(t *testing.T) {
	e := newEnv(t, storyFeed(t), setup{})
	e.runToCompletion(false)
	before := e.products()

	e.supplier.SetOutage(true)
	u := e.wait(e.start(true).ID)
	u = e.wait(e.resume(u.ID).ID)

	if u.Status != "failed" || u.BatchesDone != 0 {
		t.Fatalf("got %+v", u)
	}
	if fmt.Sprint(e.products()) != fmt.Sprint(before) {
		t.Error("catalog changed during an outage")
	}
	supplierProblems := 0
	for _, p := range e.openProblems() {
		if p.Kind == "supplier_unavailable" {
			supplierProblems++
		}
	}
	if supplierProblems != 1 {
		t.Errorf("open supplier problems = %d, want 1", supplierProblems)
	}

	e.supplier.SetOutage(false)
	u = e.wait(e.resume(u.ID).ID)
	if u.Status != "completed" || len(e.openProblems()) != 2 {
		t.Fatalf("after recovery: %+v, problems %+v", u, e.openProblems())
	}
}

// Batches with gaps in their numbers, empty batches, and a product that
// only ever appears as a withdrawal.
func TestSequenceGapsAndEmptyBatches(t *testing.T) {
	up := func(sku string, v, price int64) supplier.WireChange {
		return supplier.WireChange{SKU: sku, Version: v, Op: "upsert", Name: sku, PriceCents: price, Currency: "EUR"}
	}
	feed := fake.Feed{Batches: []supplier.WireBatch{
		{Seq: 3, Changes: []supplier.WireChange{up("A", 1, 100), up("B", 1, 200)}},
		{Seq: 4, Changes: []supplier.WireChange{}},
		{Seq: 10, Changes: []supplier.WireChange{up("A", 4, 150), {SKU: "GHOST", Version: 9, Op: "withdraw"}}},
		{Seq: 11, Changes: []supplier.WireChange{}},
		{Seq: 20, Changes: []supplier.WireChange{up("B", 2, 250), up("A", 3, 999)}},
	}}
	e := newEnv(t, feed, setup{importer: importer.Config{PageSize: 2, MaxRefills: 3}})

	u := e.runToCompletion(false)

	if u.Bookmark != 20 || u.BatchesDone != 5 {
		t.Fatalf("got %+v", u)
	}
	e.assertMatchesExpected(fake.ExpectedFor(feed))
}
