package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// These tests run the real importer against the real database, covering
// the rules in docs/01-problem.md that depend on storage (R1, R4, R5, R6).

type sliceFeed struct {
	mu       sync.Mutex
	batches  []importer.Batch
	failFrom int64 // Batches fails from this seq on (0 = never)
}

func (f *sliceFeed) Latest(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.batches)), nil
}

func (f *sliceFeed) Batches(_ context.Context, after int64, limit int) ([]importer.Batch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []importer.Batch
	for _, b := range f.batches {
		if b.Seq > after && len(out) < limit {
			if f.failFrom > 0 && b.Seq >= f.failFrom {
				return nil, errors.New("503 service unavailable")
			}
			out = append(out, b)
		}
	}
	return out, nil
}

func feedOf(batches ...[]catalog.Change) *sliceFeed {
	f := &sliceFeed{}
	for i, changes := range batches {
		f.batches = append(f.batches, importer.Batch{Seq: int64(i + 1), Changes: changes})
	}
	return f
}

// crashingStore fails the n-th SaveProduct, like the application dying in
// the middle of a batch.
type crashingStore struct {
	*Store
	mu      sync.Mutex
	crashAt int
	saves   int
}

func (c *crashingStore) InBatchTx(ctx context.Context, fn func(importer.BatchTx) error) error {
	return c.Store.InBatchTx(ctx, func(tx importer.BatchTx) error {
		return fn(&crashingTx{BatchTx: tx, store: c})
	})
}

type crashingTx struct {
	importer.BatchTx
	store *crashingStore
}

func (t *crashingTx) SaveProduct(ctx context.Context, p catalog.Product) error {
	t.store.mu.Lock()
	t.store.saves++
	crash := t.store.saves == t.store.crashAt
	t.store.mu.Unlock()
	if crash {
		return errors.New("simulated crash")
	}
	return t.BatchTx.SaveProduct(ctx, p)
}

func up(sku string, version, price int64) catalog.Change {
	return catalog.Change{SKU: sku, Version: version, Op: catalog.OpUpsert, Name: sku, PriceCents: price, Currency: "EUR"}
}

func threeBatches() *sliceFeed {
	return feedOf(
		[]catalog.Change{up("MUG", 1, 1000), up("LAMP", 1, 3000), up("VASE", 1, 2500)},
		[]catalog.Change{up("MUG", 2, 1200), up("CHAIR", 1, 9000), up("DESK", 1, 15000)},
		[]catalog.Change{{SKU: "LAMP", Version: 2, Op: catalog.OpWithdraw}, up("MUG", 1, 999)},
	)
}

func service(feed importer.Feed, store importer.Store) *importer.Service {
	return importer.NewService(feed, store, importer.Config{PageSize: 1, MaxRefills: 3},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func run(t *testing.T, s *importer.Service, store *Store, start func() (importer.Update, error)) importer.Update {
	t.Helper()
	u, err := start()
	if err != nil {
		t.Fatal(err)
	}
	s.Wait()
	u, err = store.GetUpdate(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func catalogSnapshot(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT sku, status || ' v' || version || ' ' || price_cents FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sku, state string
		rows.Scan(&sku, &state)
		out[sku] = state
	}
	return out
}

var wantFinal = map[string]string{
	"MUG":   "active v2 1200",
	"LAMP":  "withdrawn v2 3000",
	"VASE":  "active v1 2500",
	"CHAIR": "active v1 9000",
	"DESK":  "active v1 15000",
}

func assertCatalog(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("catalog has %d products, want %d: %v", len(got), len(want), got)
	}
	for sku, w := range want {
		if got[sku] != w {
			t.Errorf("%s = %q, want %q", sku, got[sku], w)
		}
	}
}

// R4, R5: a crash in the middle of batch 2 keeps batch 1, drops all of
// batch 2, and Resume finishes the job without losing or doubling work.
func TestCrashMidBatchThenResume(t *testing.T) {
	store := newTestStore(t)
	crashing := &crashingStore{Store: store, crashAt: 5} // batch 1 = saves 1–3, batch 2 crashes on its 2nd
	s := service(threeBatches(), crashing)
	ctx := context.Background()

	u := run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, false) })

	if u.Status != importer.StatusFailed || u.Bookmark != 1 {
		t.Fatalf("got %+v, want failed at bookmark 1", u)
	}
	assertCatalog(t, catalogSnapshot(t, store), map[string]string{
		"MUG": "active v1 1000", "LAMP": "active v1 3000", "VASE": "active v1 2500",
	})
	assertCount(t, store, `SELECT count(*) FROM update_changes WHERE batch_seq = 2`, 0)

	u = run(t, s, store, func() (importer.Update, error) { return s.Resume(ctx, u.ID) })

	if u.Status != importer.StatusCompleted || u.Bookmark != 3 || u.BatchesDone != 3 {
		t.Fatalf("got %+v, want completed at 3 after 3 batches", u)
	}
	assertCatalog(t, catalogSnapshot(t, store), wantFinal)
	assertCount(t, store, `SELECT count(*) FROM update_changes`, 8) // each change logged exactly once
}

// R1: importing the same data again, even twice, changes nothing.
func TestRepeatedImportsCreateNoDuplicates(t *testing.T) {
	store := newTestStore(t)
	s := service(threeBatches(), store)
	ctx := context.Background()

	run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, false) })
	for i := 0; i < 2; i++ {
		u := run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, true) })
		if u.Status != importer.StatusCompleted {
			t.Fatalf("re-import %d: %+v", i+1, u)
		}
		assertCount(t, store, `SELECT count(*) FROM update_changes WHERE update_id = `+itoa(u.ID)+
			` AND outcome NOT IN ('duplicate', 'stale')`, 0)
	}
	assertCatalog(t, catalogSnapshot(t, store), wantFinal)
}

// R4, R6: a supplier outage leaves the catalog intact and the update failed,
// never completed; the next update must wait for a resume.
func TestOutageKeepsCatalogAndNeverReportsComplete(t *testing.T) {
	store := newTestStore(t)
	feed := threeBatches()
	s := service(feed, store)
	ctx := context.Background()
	run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, false) })
	before := catalogSnapshot(t, store)

	// The supplier publishes new changes but goes down while we read them.
	feed.mu.Lock()
	feed.batches = append(feed.batches,
		importer.Batch{Seq: 4, Changes: []catalog.Change{up("MUG", 3, 1300)}},
		importer.Batch{Seq: 5, Changes: []catalog.Change{up("VASE", 2, 2600)}})
	feed.failFrom = 5
	feed.mu.Unlock()

	u := run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, false) })

	if u.Status != importer.StatusFailed || u.FinishedAt != nil || u.Bookmark != 4 {
		t.Fatalf("got %+v, want failed at bookmark 4", u)
	}
	after := catalogSnapshot(t, store)
	if len(after) != len(before) {
		t.Fatalf("catalog shrank during an outage: %v → %v", before, after)
	}
	assertCount(t, store, `SELECT count(*) FROM problems WHERE kind = 'supplier_unavailable' AND resolved_at IS NULL`, 1)
	if _, err := s.Start(ctx, false); !errors.Is(err, importer.ErrUnfinishedUpdate) {
		t.Fatalf("start during failed update: err = %v", err)
	}

	feed.mu.Lock()
	feed.failFrom = 0
	feed.mu.Unlock()
	u = run(t, s, store, func() (importer.Update, error) { return s.Resume(ctx, u.ID) })

	if u.Status != importer.StatusCompleted || u.Bookmark != 5 {
		t.Fatalf("got %+v", u)
	}
	assertCount(t, store, `SELECT count(*) FROM problems WHERE resolved_at IS NULL`, 0)
}

// R5, R6: the application died (no chance to save a status). After restart
// the update shows as interrupted, and resume continues from the bookmark.
func TestHardCrashRecoveredAfterRestart(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// State left behind by a crash: running, bookmark at batch 1.
	u, _ := store.CreateUpdate(ctx, importer.Update{Status: importer.StatusRunning, StartedAt: time.Now()})
	store.InBatchTx(ctx, func(tx importer.BatchTx) error {
		for _, ch := range threeBatches().batches[0].Changes {
			_, p := catalog.Decide(nil, ch)
			tx.SaveProduct(ctx, p)
		}
		return tx.AdvanceBookmark(ctx, u.ID, 1)
	})

	restarted := service(threeBatches(), store)
	if ids, err := restarted.Recover(ctx); err != nil || len(ids) != 1 {
		t.Fatalf("recover: %v %v", ids, err)
	}
	if got, _ := store.GetUpdate(ctx, u.ID); got.Status != importer.StatusInterrupted {
		t.Fatalf("status = %s, want interrupted", got.Status)
	}

	u = run(t, restarted, store, func() (importer.Update, error) { return restarted.Resume(ctx, u.ID) })

	if u.Status != importer.StatusCompleted || u.BatchesDone != 3 {
		t.Fatalf("got %+v", u)
	}
	assertCatalog(t, catalogSnapshot(t, store), wantFinal)
}

// Two application instances pressing Update at the same time: the
// database lets only one update be unfinished.
func TestOnlyOneUpdateAcrossInstances(t *testing.T) {
	store := newTestStore(t)
	a := service(threeBatches(), store)
	b := service(threeBatches(), store)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, s := range []*importer.Service{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Start(ctx, false)
		}()
	}
	wg.Wait()
	a.Wait()
	b.Wait()

	conflicts := 0
	for _, err := range errs {
		if errors.Is(err, importer.ErrUnfinishedUpdate) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	// Either the second start was refused, or the first already completed.
	assertCount(t, store, `SELECT count(*) FROM updates WHERE status <> 'completed'`, 0)
	if conflicts == 0 {
		assertCount(t, store, `SELECT count(*) FROM updates`, 2)
	}
	assertCatalog(t, catalogSnapshot(t, store), wantFinal)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
