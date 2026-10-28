package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// Read-only queries for the staff API.

// ListUpdates returns the newest updates first.
func (s *Store) ListUpdates(ctx context.Context, limit int) ([]importer.Update, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+updateColumns+` FROM updates ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list updates: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (importer.Update, error) { return scanUpdate(r) })
}

// UnfinishedUpdate returns the update that is not completed, if any.
func (s *Store) UnfinishedUpdate(ctx context.Context) (importer.Update, bool, error) {
	u, err := scanUpdate(s.pool.QueryRow(ctx,
		`SELECT `+updateColumns+` FROM updates WHERE status <> 'completed' ORDER BY id DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return importer.Update{}, false, nil
	}
	if err != nil {
		return importer.Update{}, false, fmt.Errorf("unfinished update: %w", err)
	}
	return u, true, nil
}

// ChangeCounts counts an update's change log by outcome.
func (s *Store) ChangeCounts(ctx context.Context, updateID int64) (map[catalog.Outcome]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT outcome, count(*) FROM update_changes WHERE update_id = $1 GROUP BY outcome`, updateID)
	if err != nil {
		return nil, fmt.Errorf("count changes: %w", err)
	}
	defer rows.Close()
	out := map[catalog.Outcome]int{}
	for rows.Next() {
		var o catalog.Outcome
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[o] = n
	}
	return out, rows.Err()
}

// ListChanges returns an update's change log in processing order,
// optionally only one outcome.
func (s *Store) ListChanges(ctx context.Context, updateID int64, outcome catalog.Outcome, limit int) ([]importer.ChangeRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT update_id, batch_seq, sku, version, outcome, detail, created_at
		FROM update_changes
		WHERE update_id = $1 AND ($2 = '' OR outcome = $2)
		ORDER BY id
		LIMIT $3`, updateID, string(outcome), limit)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (importer.ChangeRecord, error) {
		var c importer.ChangeRecord
		err := r.Scan(&c.UpdateID, &c.BatchSeq, &c.SKU, &c.Version, &c.Outcome, &c.Detail, &c.CreatedAt)
		return c, err
	})
}

// ListProblems returns problems, newest first. updateID 0 means all
// updates; openOnly hides resolved problems.
func (s *Store) ListProblems(ctx context.Context, updateID int64, openOnly bool) ([]importer.Problem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, update_id, batch_seq, sku, version, kind, message, created_at, resolved_at
		FROM problems
		WHERE ($1 = 0 OR update_id = $1) AND (NOT $2 OR resolved_at IS NULL)
		ORDER BY id DESC`, updateID, openOnly)
	if err != nil {
		return nil, fmt.Errorf("list problems: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (importer.Problem, error) {
		var p importer.Problem
		err := r.Scan(&p.ID, &p.UpdateID, &p.BatchSeq, &p.SKU, &p.Version, &p.Kind, &p.Message, &p.CreatedAt, &p.ResolvedAt)
		return p, err
	})
}

// ListProducts returns the catalog ordered by SKU; status "" means all.
func (s *Store) ListProducts(ctx context.Context, status catalog.Status) ([]catalog.Product, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sku, name, description, price_cents, currency, status, version
		FROM products
		WHERE ($1 = '' OR status = $1)
		ORDER BY sku`, string(status))
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (catalog.Product, error) {
		var p catalog.Product
		err := r.Scan(&p.SKU, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.Status, &p.Version)
		return p, err
	})
}
