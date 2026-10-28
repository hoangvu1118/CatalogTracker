package storage

import (
	"context"
	"testing"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

func TestReadQueries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, ok, err := store.UnfinishedUpdate(ctx); ok || err != nil {
		t.Fatalf("empty database: ok=%v err=%v", ok, err)
	}

	feed := threeBatches()
	feed.failFrom = 3
	s := service(feed, store)
	u := run(t, s, store, func() (importer.Update, error) { return s.Start(ctx, false) })

	unfinished, ok, err := store.UnfinishedUpdate(ctx)
	if err != nil || !ok || unfinished.ID != u.ID {
		t.Fatalf("unfinished = %+v ok=%v err=%v", unfinished, ok, err)
	}

	updates, err := store.ListUpdates(ctx, 10)
	if err != nil || len(updates) != 1 || updates[0].Status != importer.StatusFailed {
		t.Fatalf("updates = %+v, %v", updates, err)
	}

	counts, err := store.ChangeCounts(ctx, u.ID)
	if err != nil || counts[catalog.OutcomeCreated] != 5 || counts[catalog.OutcomeUpdated] != 1 {
		t.Fatalf("counts = %v, %v", counts, err)
	}

	changes, err := store.ListChanges(ctx, u.ID, "", 100)
	if err != nil || len(changes) != 6 || changes[0].SKU != "MUG" || changes[0].BatchSeq != 1 {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	updated, _ := store.ListChanges(ctx, u.ID, catalog.OutcomeUpdated, 100)
	if len(updated) != 1 || updated[0].Detail != "price 10.00 EUR → 12.00 EUR" {
		t.Fatalf("updated = %+v", updated)
	}
	if limited, _ := store.ListChanges(ctx, u.ID, "", 2); len(limited) != 2 {
		t.Errorf("limit not applied: %d", len(limited))
	}

	open, err := store.ListProblems(ctx, 0, true)
	if err != nil || len(open) != 1 || open[0].Kind != importer.ProblemSupplierUnavailable {
		t.Fatalf("open problems = %+v, %v", open, err)
	}
	if other, _ := store.ListProblems(ctx, u.ID+1, false); len(other) != 0 {
		t.Errorf("problems of another update: %+v", other)
	}

	all, err := store.ListProducts(ctx, "")
	if err != nil || len(all) != 5 || all[0].SKU != "CHAIR" {
		t.Fatalf("products = %+v, %v", all, err)
	}
	if withdrawn, _ := store.ListProducts(ctx, catalog.StatusWithdrawn); len(withdrawn) != 0 {
		t.Errorf("withdrawn = %+v, want none yet", withdrawn)
	}
}
