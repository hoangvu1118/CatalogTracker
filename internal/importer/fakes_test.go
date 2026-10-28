package importer

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
)

// memFeed is an in-memory supplier. Only the first `published` batches are
// visible (Window 1); tests can publish more or simulate an outage.
type memFeed struct {
	mu        sync.Mutex
	batches   []Batch
	published int
	// failFrom makes Batches fail when it would return this seq or later
	// (0 = never), and makes Latest fail when downAll is set.
	failFrom int64
	downAll  bool
	// afterBatches runs after every Batches call (e.g. to publish more).
	afterBatches func(f *memFeed)
}

var errSupplierDown = errors.New("503 service unavailable")

func newFeed(batches ...[]catalog.Change) *memFeed {
	f := &memFeed{}
	for _, changes := range batches {
		f.add(changes)
	}
	f.published = len(f.batches)
	return f
}

func (f *memFeed) add(changes []catalog.Change) {
	f.batches = append(f.batches, Batch{Seq: int64(len(f.batches) + 1), Changes: changes})
}

func (f *memFeed) publish(n int) {
	f.published = min(f.published+n, len(f.batches))
}

func (f *memFeed) Latest(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.downAll {
		return 0, errSupplierDown
	}
	if f.published == 0 {
		return 0, nil
	}
	return f.batches[f.published-1].Seq, nil
}

func (f *memFeed) Batches(ctx context.Context, after int64, limit int) ([]Batch, error) {
	f.mu.Lock()
	var out []Batch
	var err error
	for _, b := range f.batches[:f.published] {
		if b.Seq <= after || len(out) == limit {
			continue
		}
		if f.downAll || (f.failFrom > 0 && b.Seq >= f.failFrom) {
			err = errSupplierDown
			break
		}
		out = append(out, b)
	}
	hook := f.afterBatches
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if hook != nil {
		f.mu.Lock()
		hook(f)
		f.mu.Unlock()
	}
	return out, ctx.Err()
}

// memStore is an in-memory Store. A batch transaction works on a copy and
// replaces the real state only if it succeeds, like a database rollback.
type memStore struct {
	mu    sync.Mutex
	state memState
	// failSave returns an error on the n-th SaveProduct (counted across the
	// whole test, 1-based), simulating a crash in the middle of a batch.
	failSave int
	saves    int
}

type memState struct {
	products map[string]catalog.Product
	updates  map[int64]Update
	changes  []ChangeRecord
	problems []Problem
	nextID   int64
}

func newStore() *memStore {
	return &memStore{state: memState{
		products: map[string]catalog.Product{},
		updates:  map[int64]Update{},
	}}
}

func (s memState) clone() memState {
	c := memState{
		products: make(map[string]catalog.Product, len(s.products)),
		updates:  make(map[int64]Update, len(s.updates)),
		changes:  append([]ChangeRecord(nil), s.changes...),
		problems: append([]Problem(nil), s.problems...),
		nextID:   s.nextID,
	}
	for k, v := range s.products {
		c.products[k] = v
	}
	for k, v := range s.updates {
		c.updates[k] = v
	}
	return c
}

func (m *memStore) CreateUpdate(_ context.Context, u Update) (Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, other := range m.state.updates {
		if other.Status != StatusCompleted {
			return Update{}, ErrUnfinishedUpdate
		}
	}
	m.state.nextID++
	u.ID = m.state.nextID
	m.state.updates[u.ID] = u
	return u, nil
}

func (m *memStore) GetUpdate(_ context.Context, id int64) (Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.state.updates[id]
	if !ok {
		return Update{}, ErrNotFound
	}
	return u, nil
}

func (m *memStore) LastBookmark(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best int64
	for _, u := range m.state.updates {
		best = max(best, u.Bookmark)
	}
	return best, nil
}

func (m *memStore) SaveState(_ context.Context, u Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.state.updates[u.ID]
	if !ok {
		return ErrNotFound
	}
	stored.Status, stored.Target, stored.Refills = u.Status, u.Target, u.Refills
	stored.Error, stored.FinishedAt = u.Error, u.FinishedAt
	m.state.updates[u.ID] = stored
	return nil
}

func (m *memStore) MarkRunningAsInterrupted(context.Context) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for id, u := range m.state.updates {
		if u.Status == StatusRunning {
			u.Status = StatusInterrupted
			m.state.updates[id] = u
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (m *memStore) AddProblem(_ context.Context, p Problem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.problems = append(m.state.problems, p)
	return nil
}

func (m *memStore) ResolveProblems(_ context.Context, updateID int64, kind ProblemKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.state.problems {
		if p.UpdateID == updateID && p.Kind == kind && p.ResolvedAt == nil {
			now := p.CreatedAt
			m.state.problems[i].ResolvedAt = &now
		}
	}
	return nil
}

func (m *memStore) InBatchTx(_ context.Context, fn func(tx BatchTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &memTx{store: m, state: m.state.clone()}
	if err := fn(tx); err != nil {
		return err // the copy is dropped: rollback
	}
	m.state = tx.state // commit
	return nil
}

type memTx struct {
	store *memStore
	state memState
}

func (t *memTx) LockProducts(_ context.Context, skus []string) (map[string]catalog.Product, error) {
	out := map[string]catalog.Product{}
	for _, sku := range skus {
		if p, ok := t.state.products[sku]; ok {
			out[sku] = p
		}
	}
	return out, nil
}

func (t *memTx) SaveProduct(_ context.Context, p catalog.Product) error {
	t.store.saves++
	if t.store.failSave > 0 && t.store.saves == t.store.failSave {
		return errors.New("simulated crash while saving")
	}
	t.state.products[p.SKU] = p
	return nil
}

func (t *memTx) RecordChange(_ context.Context, c ChangeRecord) error {
	t.state.changes = append(t.state.changes, c)
	return nil
}

func (t *memTx) RecordProblem(_ context.Context, p Problem) error {
	t.state.problems = append(t.state.problems, p)
	return nil
}

func (t *memTx) ResolveInvalidChanges(_ context.Context, sku string, upTo int64) error {
	for i, p := range t.state.problems {
		if p.Kind == ProblemInvalidChange && p.SKU == sku && p.Version <= upTo && p.ResolvedAt == nil {
			now := p.CreatedAt
			t.state.problems[i].ResolvedAt = &now
		}
	}
	return nil
}

func (t *memTx) AdvanceBookmark(_ context.Context, updateID, seq int64) error {
	u := t.state.updates[updateID]
	if u.Bookmark >= seq {
		return ErrBatchAlreadyApplied
	}
	u.Bookmark = seq
	u.BatchesDone++
	t.state.updates[updateID] = u
	return nil
}

// Read helpers for assertions.

func (m *memStore) product(sku string) (catalog.Product, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.state.products[sku]
	return p, ok
}

func (m *memStore) productCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.state.products)
}

func (m *memStore) outcomes(updateID int64) map[catalog.Outcome]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[catalog.Outcome]int{}
	for _, c := range m.state.changes {
		if c.UpdateID == updateID {
			out[c.Outcome]++
		}
	}
	return out
}

func (m *memStore) openProblems() []Problem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Problem
	for _, p := range m.state.problems {
		if p.ResolvedAt == nil {
			out = append(out, p)
		}
	}
	return out
}
