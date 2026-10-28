package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// ErrStaleWrite means the database already holds a newer version of the
// product. The importer's version rule should prevent this; the check in
// SQL is a second safety net.
var ErrStaleWrite = errors.New("refused to overwrite a newer product version")

// batchTx implements importer.BatchTx inside one PostgreSQL transaction.
type batchTx struct {
	tx pgx.Tx
}

func (b *batchTx) LockProducts(ctx context.Context, skus []string) (map[string]catalog.Product, error) {
	rows, err := b.tx.Query(ctx, `
		SELECT sku, name, description, price_cents, currency, status, version
		FROM products
		WHERE sku = ANY($1)
		ORDER BY sku
		FOR UPDATE`, skus)
	if err != nil {
		return nil, fmt.Errorf("lock products: %w", err)
	}
	defer rows.Close()

	out := make(map[string]catalog.Product, len(skus))
	for rows.Next() {
		var p catalog.Product
		if err := rows.Scan(&p.SKU, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.Status, &p.Version); err != nil {
			return nil, err
		}
		out[p.SKU] = p
	}
	return out, rows.Err()
}

func (b *batchTx) SaveProduct(ctx context.Context, p catalog.Product) error {
	tag, err := b.tx.Exec(ctx, `
		INSERT INTO products (sku, name, description, price_cents, currency, status, version, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (sku) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			price_cents = EXCLUDED.price_cents,
			currency = EXCLUDED.currency,
			status = EXCLUDED.status,
			version = EXCLUDED.version,
			updated_at = now()
		WHERE products.version < EXCLUDED.version`,
		p.SKU, p.Name, p.Description, p.PriceCents, p.Currency, p.Status, p.Version)
	if err != nil {
		return fmt.Errorf("save product %s: %w", p.SKU, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s v%d", ErrStaleWrite, p.SKU, p.Version)
	}
	return nil
}

func (b *batchTx) RecordChange(ctx context.Context, c importer.ChangeRecord) error {
	_, err := b.tx.Exec(ctx, `
		INSERT INTO update_changes (update_id, batch_seq, sku, version, outcome, detail, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.UpdateID, c.BatchSeq, c.SKU, c.Version, c.Outcome, c.Detail, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("record change: %w", err)
	}
	return nil
}

// RecordProblem skips a problem that is already open for the same SKU and
// version, so a broken record delivered twice needs attention only once.
func (b *batchTx) RecordProblem(ctx context.Context, p importer.Problem) error {
	_, err := b.tx.Exec(ctx, `
		INSERT INTO problems (update_id, batch_seq, sku, version, kind, message, created_at)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM problems
			WHERE kind = $5 AND sku = $3 AND version = $4 AND resolved_at IS NULL)`,
		p.UpdateID, p.BatchSeq, p.SKU, p.Version, p.Kind, p.Message, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("record problem: %w", err)
	}
	return nil
}

func (b *batchTx) ResolveInvalidChanges(ctx context.Context, sku string, upToVersion int64) error {
	_, err := b.tx.Exec(ctx, `
		UPDATE problems SET resolved_at = now()
		WHERE kind = $1 AND sku = $2 AND version <= $3 AND resolved_at IS NULL`,
		importer.ProblemInvalidChange, sku, upToVersion)
	if err != nil {
		return fmt.Errorf("resolve problems for %s: %w", sku, err)
	}
	return nil
}

func (b *batchTx) AdvanceBookmark(ctx context.Context, updateID, seq int64) error {
	tag, err := b.tx.Exec(ctx, `
		UPDATE updates
		SET bookmark = $2, batches_done = batches_done + 1
		WHERE id = $1 AND bookmark < $2`,
		updateID, seq)
	if err != nil {
		return fmt.Errorf("advance bookmark: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return importer.ErrBatchAlreadyApplied
	}
	return nil
}
