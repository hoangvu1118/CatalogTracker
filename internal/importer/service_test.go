package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
)

func up(sku string, version, price int64) catalog.Change {
	return catalog.Change{SKU: sku, Version: version, Op: catalog.OpUpsert, Name: sku + " name", PriceCents: price, Currency: "EUR"}
}

func wd(sku string, version int64) catalog.Change {
	return catalog.Change{SKU: sku, Version: version, Op: catalog.OpWithdraw}
}

func newService(feed Feed, store Store, cfg Config) *Service {
	return NewService(feed, store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// startAndWait starts an update and waits until it stops.
func startAndWait(t *testing.T, s *Service, fromBeginning bool) Update {
	t.Helper()
	u, err := s.Start(context.Background(), fromBeginning)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.Wait()
	return reload(t, s, u.ID)
}

func resumeAndWait(t *testing.T, s *Service, id int64) Update {
	t.Helper()
	if _, err := s.Resume(context.Background(), id); err != nil {
		t.Fatalf("resume: %v", err)
	}
	s.Wait()
	return reload(t, s, id)
}

func reload(t *testing.T, s *Service, id int64) Update {
	t.Helper()
	u, err := s.store.GetUpdate(context.Background(), id)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}
	return u
}

func wantProduct(t *testing.T, store *memStore, sku string, status catalog.Status, version, price int64) {
	t.Helper()
	p, ok := store.product(sku)
	if !ok {
		t.Fatalf("%s: missing", sku)
	}
	if p.Status != status || p.Version != version || p.PriceCents != price {
		t.Errorf("%s: got %s v%d %d, want %s v%d %d", sku, p.Status, p.Version, p.PriceCents, status, version, price)
	}
}

func basicFeed() *memFeed {
	return newFeed(
		[]catalog.Change{up("MUG", 1, 1000), up("LAMP", 1, 3000)},
		[]catalog.Change{up("MUG", 2, 1200), up("CHAIR", 1, 9000)},
		[]catalog.Change{wd("LAMP", 2)},
	)
}

func TestFullImportCompletes(t *testing.T) {
	store := newStore()
	s := newService(basicFeed(), store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusCompleted || u.Bookmark != 3 || u.BatchesDone != 3 || u.FinishedAt == nil {
		t.Fatalf("got %+v, want completed at bookmark 3 after 3 batches", u)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 2, 1200)
	wantProduct(t, store, "CHAIR", catalog.StatusActive, 1, 9000)
	wantProduct(t, store, "LAMP", catalog.StatusWithdrawn, 2, 3000)

	got := store.outcomes(u.ID)
	want := map[catalog.Outcome]int{catalog.OutcomeCreated: 3, catalog.OutcomeUpdated: 1, catalog.OutcomeWithdrawn: 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
}

func TestNextUpdateContinuesFromBookmark(t *testing.T) {
	feed := basicFeed()
	store := newStore()
	s := newService(feed, store, DefaultConfig())
	startAndWait(t, s, false)

	second := startAndWait(t, s, false)
	if second.Status != StatusCompleted || second.BatchesDone != 0 || second.StartBookmark != 3 {
		t.Fatalf("nothing new: got %+v", second)
	}

	feed.add([]catalog.Change{up("MUG", 3, 1500)})
	feed.publish(1)
	third := startAndWait(t, s, false)
	if third.BatchesDone != 1 || third.Bookmark != 4 {
		t.Fatalf("one new batch: got %+v", third)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 3, 1500)
}

// R1: importing the same data twice must not create duplicates.
func TestReimportFromBeginningChangesNothing(t *testing.T) {
	store := newStore()
	s := newService(basicFeed(), store, DefaultConfig())
	startAndWait(t, s, false)

	again := startAndWait(t, s, true)

	if again.Status != StatusCompleted || again.BatchesDone != 3 {
		t.Fatalf("got %+v", again)
	}
	if n := store.productCount(); n != 3 {
		t.Errorf("products = %d, want 3", n)
	}
	// Latest versions are duplicates; the older ones (MUG v1, LAMP v1) are stale.
	got := store.outcomes(again.ID)
	if got[catalog.OutcomeDuplicate] != 3 || got[catalog.OutcomeStale] != 2 || len(got) != 2 {
		t.Errorf("outcomes = %v, want 3 duplicate and 2 stale only", got)
	}
	wantProduct(t, store, "LAMP", catalog.StatusWithdrawn, 2, 3000)
}

// R2, R3: out-of-order and repeated changes, inside and across batches.
func TestOutOfOrderAndRepeatedChanges(t *testing.T) {
	feed := newFeed(
		[]catalog.Change{up("MUG", 3, 1500), up("MUG", 1, 1000)},   // newest first in the same batch
		[]catalog.Change{up("MUG", 2, 1200), up("MUG", 3, 1500)},   // late old one, and a repeat
		[]catalog.Change{wd("LAMP", 7)},                            // withdrawal of an unseen product
		[]catalog.Change{up("LAMP", 5, 3000), up("LAMP", 6, 3100)}, // older: must not revive it
		[]catalog.Change{up("LAMP", 9, 3200)},                      // newer: restocks it
	)
	store := newStore()
	s := newService(feed, store, Config{PageSize: 2, MaxRefills: 3})

	u := startAndWait(t, s, false)

	wantProduct(t, store, "MUG", catalog.StatusActive, 3, 1500)
	wantProduct(t, store, "LAMP", catalog.StatusActive, 9, 3200)
	got := store.outcomes(u.ID)
	want := map[catalog.Outcome]int{
		catalog.OutcomeCreated: 1, catalog.OutcomeStale: 4, catalog.OutcomeDuplicate: 1,
		catalog.OutcomeWithdrawn: 1, catalog.OutcomeRestocked: 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

// R4, R5, R6: supplier outage in the middle, then resume.
func TestSupplierOutageThenResume(t *testing.T) {
	feed := basicFeed()
	feed.failFrom = 3
	store := newStore()
	s := newService(feed, store, Config{PageSize: 1, MaxRefills: 3})

	u := startAndWait(t, s, false)

	if u.Status != StatusFailed || u.Bookmark != 2 || u.FinishedAt != nil {
		t.Fatalf("got %+v, want failed at bookmark 2", u)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 2, 1200)  // work from batches 1–2 kept
	wantProduct(t, store, "LAMP", catalog.StatusActive, 1, 3000) // batch 3 not applied yet
	if p := store.openProblems(); len(p) != 1 || p[0].Kind != ProblemSupplierUnavailable {
		t.Fatalf("open problems = %+v, want one supplier_unavailable", p)
	}
	if _, err := s.Start(context.Background(), false); !errors.Is(err, ErrUnfinishedUpdate) {
		t.Fatalf("start during failed update: err = %v, want ErrUnfinishedUpdate", err)
	}

	feed.failFrom = 0 // supplier is back
	u = resumeAndWait(t, s, u.ID)

	if u.Status != StatusCompleted || u.Bookmark != 3 || u.BatchesDone != 3 {
		t.Fatalf("got %+v, want completed at bookmark 3", u)
	}
	wantProduct(t, store, "LAMP", catalog.StatusWithdrawn, 2, 3000)
	if p := store.openProblems(); len(p) != 0 {
		t.Errorf("open problems after completion = %+v, want none", p)
	}
}

// D4: a crash in the middle of a batch keeps nothing from that batch.
func TestCrashMidBatchRollsBackWholeBatch(t *testing.T) {
	store := newStore()
	store.failSave = 4 // batch 1 saves 2 products, batch 2 fails on its 2nd
	s := newService(basicFeed(), store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusFailed || u.Bookmark != 1 {
		t.Fatalf("got %+v, want failed at bookmark 1", u)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 1, 1000) // batch 2's MUG v2 rolled back
	if _, ok := store.product("CHAIR"); ok {
		t.Error("CHAIR from the failed batch must not be saved")
	}
	if got := store.outcomes(u.ID); got[catalog.OutcomeUpdated] != 0 {
		t.Errorf("change log of the failed batch must be rolled back: %v", got)
	}

	u = resumeAndWait(t, s, u.ID)
	if u.Status != StatusCompleted || u.Bookmark != 3 {
		t.Fatalf("got %+v, want completed", u)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 2, 1200)
	wantProduct(t, store, "CHAIR", catalog.StatusActive, 1, 9000)
}

// D6: an update left running by a crash becomes interrupted at start-up.
func TestRecoverMarksRunningAsInterrupted(t *testing.T) {
	store := newStore()
	store.state.updates[7] = Update{ID: 7, Status: StatusRunning, Bookmark: 1}
	store.state.nextID = 7
	s := newService(basicFeed(), store, DefaultConfig())

	ids, err := s.Recover(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("recover = %v, %v", ids, err)
	}
	u := resumeAndWait(t, s, 7)
	if u.Status != StatusCompleted || u.Bookmark != 3 || u.BatchesDone != 2 {
		t.Fatalf("got %+v, want completed after the 2 remaining batches", u)
	}
}

// D6: shutting the application down interrupts the running update.
func TestShutdownInterrupts(t *testing.T) {
	feed := basicFeed()
	store := newStore()
	s := newService(feed, store, Config{PageSize: 1, MaxRefills: 3})
	feed.afterBatches = func(*memFeed) { s.stopAll() } // stop after the first fetch

	u, err := s.Start(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	s.Shutdown()
	u = reload(t, s, u.ID)

	if u.Status != StatusInterrupted {
		t.Fatalf("status = %s, want interrupted", u.Status)
	}
	if u.Bookmark >= 3 {
		t.Fatalf("bookmark = %d: an interrupted update must not have finished", u.Bookmark)
	}
}

// D5: changes that arrive during the update are picked up by refilling.
func TestWindowRefillCatchesUpNewBatches(t *testing.T) {
	feed := basicFeed()
	feed.add([]catalog.Change{up("MUG", 3, 1300)})
	feed.add([]catalog.Change{up("MUG", 4, 1400)})
	feed.published = 3
	published := false
	feed.afterBatches = func(f *memFeed) {
		if !published {
			f.publish(2) // two more batches arrive while Window 2 is processed
			published = true
		}
	}
	store := newStore()
	s := newService(feed, store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusCompleted || u.Bookmark != 5 || u.Refills != 1 {
		t.Fatalf("got %+v, want completed at 5 after 1 refill", u)
	}
	wantProduct(t, store, "MUG", catalog.StatusActive, 4, 1400)
}

// D5 stop rule C: a supplier that never stops changing still lets the
// update finish after MaxRefills; the rest waits for the next update.
func TestRefillLimitGuaranteesTheUpdateEnds(t *testing.T) {
	feed := newFeed([]catalog.Change{up("MUG", 1, 1000)})
	version := int64(1)
	feed.afterBatches = func(f *memFeed) {
		version++
		f.add([]catalog.Change{up("MUG", version, 1000+version)})
		f.publish(1)
	}
	store := newStore()
	s := newService(feed, store, Config{PageSize: 5, MaxRefills: 2})

	u := startAndWait(t, s, false)

	if u.Status != StatusCompleted || u.Refills != 2 || u.BatchesDone != 3 {
		t.Fatalf("got %+v, want completed after 2 refills and 3 batches", u)
	}
	latest, _ := feed.Latest(context.Background())
	if latest <= u.Bookmark {
		t.Fatalf("expected newer batches left for the next update (latest %d, bookmark %d)", latest, u.Bookmark)
	}
}

// D8: invalid changes are skipped and reported; a newer valid version
// resolves the problem.
func TestInvalidChangesBecomeProblems(t *testing.T) {
	bad := up("VASE", 2, -100)
	bad.Name = ""
	feed := newFeed(
		[]catalog.Change{up("VASE", 1, 2500), bad, up("MUG", 1, 1000)},
	)
	store := newStore()
	s := newService(feed, store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusCompleted {
		t.Fatalf("an invalid record must not stop the update: %+v", u)
	}
	wantProduct(t, store, "VASE", catalog.StatusActive, 1, 2500)
	wantProduct(t, store, "MUG", catalog.StatusActive, 1, 1000)
	open := store.openProblems()
	if len(open) != 1 || open[0].Kind != ProblemInvalidChange || open[0].SKU != "VASE" || open[0].Version != 2 {
		t.Fatalf("open problems = %+v, want VASE v2 invalid", open)
	}

	// An old invalid change for a product we already have newer data for
	// needs no attention; a newer valid one resolves the open problem.
	stale := up("MUG", 1, 0)
	feed.add([]catalog.Change{stale, up("VASE", 3, 2700)})
	feed.publish(1)
	startAndWait(t, s, false)

	if open := store.openProblems(); len(open) != 0 {
		t.Fatalf("open problems = %+v, want none", open)
	}
	wantProduct(t, store, "VASE", catalog.StatusActive, 3, 2700)
}

// The demo pacing slows updates down but can still be interrupted.
func TestBatchDelayPacesAndCanBeInterrupted(t *testing.T) {
	store := newStore()
	s := newService(basicFeed(), store, Config{PageSize: 5, MaxRefills: 3, BatchDelay: 30 * time.Millisecond})

	begin := time.Now()
	u := startAndWait(t, s, false)
	if u.Status != StatusCompleted || time.Since(begin) < 90*time.Millisecond {
		t.Fatalf("got %+v after %v, want completed after at least 3 pauses", u, time.Since(begin))
	}

	slow := newService(basicFeed(), newStore(), Config{PageSize: 5, MaxRefills: 3, BatchDelay: time.Hour})
	u, err := slow.Start(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // pausing before batch 1
	slow.Shutdown()
	if got := reload(t, slow, u.ID); got.Status != StatusInterrupted || got.Bookmark != 0 {
		t.Fatalf("got %+v, want interrupted before any batch was saved", got)
	}
}

// observingStore records, each time a batch is saved, how far the service
// says it has downloaded.
type observingStore struct {
	*memStore
	service  *Service
	updateID int64
	seen     []int64
}

func (o *observingStore) InBatchTx(ctx context.Context, fn func(tx BatchTx) error) error {
	o.seen = append(o.seen, o.service.Fetched(o.updateID))
	return o.memStore.InBatchTx(ctx, fn)
}

// Window 2 is downloaded one page at a time; Fetched shows how far.
func TestFetchedFollowsThePages(t *testing.T) {
	feed := newFeed(
		[]catalog.Change{up("A", 1, 100)}, []catalog.Change{up("B", 1, 100)}, []catalog.Change{up("C", 1, 100)},
		[]catalog.Change{up("D", 1, 100)}, []catalog.Change{up("E", 1, 100)},
	)
	store := &observingStore{memStore: newStore(), updateID: 1}
	s := newService(feed, store, Config{PageSize: 2, MaxRefills: 3})
	store.service = s

	u := startAndWait(t, s, false)

	// Pages: [1 2] [3 4] [5]. While saving batch 1 or 2, batches up to 2 are in memory, and so on.
	if fmt.Sprint(store.seen) != "[2 2 4 4 5]" {
		t.Errorf("fetched while saving each batch = %v, want [2 2 4 4 5]", store.seen)
	}
	if got := s.Fetched(u.ID); got != 0 {
		t.Errorf("after the update, fetched = %d, want 0 (nothing held in memory)", got)
	}
}

func TestResumeRules(t *testing.T) {
	store := newStore()
	s := newService(basicFeed(), store, DefaultConfig())
	u := startAndWait(t, s, false)

	if _, err := s.Resume(context.Background(), u.ID); !errors.Is(err, ErrNotResumable) {
		t.Errorf("resume completed: err = %v, want ErrNotResumable", err)
	}
	if _, err := s.Resume(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("resume unknown: err = %v, want ErrNotFound", err)
	}
}

func TestEmptyFeedCompletesImmediately(t *testing.T) {
	store := newStore()
	s := newService(newFeed(), store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusCompleted || u.BatchesDone != 0 {
		t.Fatalf("got %+v", u)
	}
}

func TestSupplierDownAtStartFails(t *testing.T) {
	feed := basicFeed()
	feed.downAll = true
	store := newStore()
	s := newService(feed, store, DefaultConfig())

	u := startAndWait(t, s, false)

	if u.Status != StatusFailed || u.Bookmark != 0 || u.Error == "" {
		t.Fatalf("got %+v, want failed with an error message", u)
	}
	if store.productCount() != 0 {
		t.Error("no products should be written")
	}
}
