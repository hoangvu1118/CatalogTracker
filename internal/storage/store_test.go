package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := testPool(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool)
}

func newRunning(t *testing.T, s *Store, bookmark int64) importer.Update {
	t.Helper()
	u, err := s.CreateUpdate(context.Background(), importer.Update{
		Status: importer.StatusRunning, StartBookmark: bookmark, Bookmark: bookmark, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create update: %v", err)
	}
	return u
}

func product(sku string, version int64, status catalog.Status) catalog.Product {
	return catalog.Product{SKU: sku, Name: sku, PriceCents: 1000, Currency: "EUR", Status: status, Version: version}
}

func TestUpdateLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u := newRunning(t, s, 0)
	if _, err := s.CreateUpdate(ctx, importer.Update{Status: importer.StatusRunning, StartedAt: time.Now()}); !errors.Is(err, importer.ErrUnfinishedUpdate) {
		t.Fatalf("second unfinished update: err = %v, want ErrUnfinishedUpdate", err)
	}

	finished := time.Now()
	u.Status, u.Target, u.Refills, u.FinishedAt = importer.StatusCompleted, 9, 2, &finished
	if err := s.SaveState(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUpdate(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != importer.StatusCompleted || got.Target != 9 || got.Refills != 2 || got.FinishedAt == nil {
		t.Errorf("got %+v", got)
	}

	if _, err := s.GetUpdate(ctx, 12345); !errors.Is(err, importer.ErrNotFound) {
		t.Errorf("missing update: err = %v, want ErrNotFound", err)
	}
	newRunning(t, s, 0) // allowed again once the first is completed
}

func TestBatchTxCommitsEverythingTogether(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newRunning(t, s, 0)

	err := s.InBatchTx(ctx, func(tx importer.BatchTx) error {
		if err := tx.SaveProduct(ctx, product("MUG", 1, catalog.StatusActive)); err != nil {
			return err
		}
		if err := tx.RecordChange(ctx, importer.ChangeRecord{UpdateID: u.ID, BatchSeq: 1, SKU: "MUG", Version: 1, Outcome: catalog.OutcomeCreated, CreatedAt: time.Now()}); err != nil {
			return err
		}
		return tx.AdvanceBookmark(ctx, u.ID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetUpdate(ctx, u.ID)
	if got.Bookmark != 1 || got.BatchesDone != 1 {
		t.Errorf("bookmark %d, batches %d; want 1, 1", got.Bookmark, got.BatchesDone)
	}
	if b, _ := s.LastBookmark(ctx); b != 1 {
		t.Errorf("last bookmark = %d, want 1", b)
	}
	assertCount(t, s, `SELECT count(*) FROM products`, 1)
	assertCount(t, s, `SELECT count(*) FROM update_changes`, 1)
}

func TestBatchTxRollsBackEverythingOnError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newRunning(t, s, 0)
	crash := errors.New("crash")

	err := s.InBatchTx(ctx, func(tx importer.BatchTx) error {
		tx.SaveProduct(ctx, product("MUG", 1, catalog.StatusActive))
		tx.RecordProblem(ctx, importer.Problem{UpdateID: u.ID, BatchSeq: 1, Kind: importer.ProblemInvalidChange, Message: "x", CreatedAt: time.Now()})
		tx.AdvanceBookmark(ctx, u.ID, 1)
		return crash
	})
	if !errors.Is(err, crash) {
		t.Fatalf("err = %v, want crash", err)
	}

	got, _ := s.GetUpdate(ctx, u.ID)
	if got.Bookmark != 0 || got.BatchesDone != 0 {
		t.Errorf("bookmark moved despite rollback: %+v", got)
	}
	assertCount(t, s, `SELECT count(*) FROM products`, 0)
	assertCount(t, s, `SELECT count(*) FROM problems`, 0)
}

func TestLockProductsAndStaleWriteGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.InBatchTx(ctx, func(tx importer.BatchTx) error {
		if err := tx.SaveProduct(ctx, product("LAMP", 7, catalog.StatusWithdrawn)); err != nil {
			return err
		}
		got, err := tx.LockProducts(ctx, []string{"LAMP", "UNKNOWN"})
		if err != nil {
			return err
		}
		if len(got) != 1 || got["LAMP"].Status != catalog.StatusWithdrawn || got["LAMP"].Version != 7 {
			t.Errorf("locked products = %+v", got)
		}
		// The database itself refuses an older or equal version.
		if err := tx.SaveProduct(ctx, product("LAMP", 5, catalog.StatusActive)); !errors.Is(err, ErrStaleWrite) {
			t.Errorf("older write: err = %v, want ErrStaleWrite", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceBookmarkOnlyMovesForward(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newRunning(t, s, 5)

	err := s.InBatchTx(ctx, func(tx importer.BatchTx) error { return tx.AdvanceBookmark(ctx, u.ID, 5) })
	if !errors.Is(err, importer.ErrBatchAlreadyApplied) {
		t.Fatalf("err = %v, want ErrBatchAlreadyApplied", err)
	}
}

func TestMarkRunningAsInterrupted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newRunning(t, s, 0)

	ids, err := s.MarkRunningAsInterrupted(ctx)
	if err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
	got, _ := s.GetUpdate(ctx, u.ID)
	if got.Status != importer.StatusInterrupted || got.Error == "" {
		t.Errorf("got %+v", got)
	}
}

func TestProblemsResolve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newRunning(t, s, 0)
	now := time.Now()

	s.AddProblem(ctx, importer.Problem{UpdateID: u.ID, BatchSeq: 3, Kind: importer.ProblemSupplierUnavailable, Message: "down", CreatedAt: now})
	s.InBatchTx(ctx, func(tx importer.BatchTx) error {
		tx.RecordProblem(ctx, importer.Problem{UpdateID: u.ID, BatchSeq: 1, SKU: "VASE", Version: 2, Kind: importer.ProblemInvalidChange, Message: "bad", CreatedAt: now})
		tx.RecordProblem(ctx, importer.Problem{UpdateID: u.ID, BatchSeq: 1, SKU: "VASE", Version: 5, Kind: importer.ProblemInvalidChange, Message: "bad", CreatedAt: now})
		return tx.ResolveInvalidChanges(ctx, "VASE", 3) // resolves v2 only
	})
	assertCount(t, s, `SELECT count(*) FROM problems WHERE resolved_at IS NULL`, 2)

	if err := s.ResolveProblems(ctx, u.ID, importer.ProblemSupplierUnavailable); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT count(*) FROM problems WHERE resolved_at IS NULL`, 1)
}

func assertCount(t *testing.T, s *Store, query string, want int) {
	t.Helper()
	var got int
	if err := s.pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s = %d, want %d", query, got, want)
	}
}
