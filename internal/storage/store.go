package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// Store implements importer.Store with PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

var _ importer.Store = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const updateColumns = `id, status, start_bookmark, bookmark, target, refills, batches_done, error, started_at, finished_at`

func scanUpdate(row pgx.Row) (importer.Update, error) {
	var u importer.Update
	err := row.Scan(&u.ID, &u.Status, &u.StartBookmark, &u.Bookmark, &u.Target, &u.Refills,
		&u.BatchesDone, &u.Error, &u.StartedAt, &u.FinishedAt)
	return u, err
}

func (s *Store) CreateUpdate(ctx context.Context, u importer.Update) (importer.Update, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO updates (status, start_bookmark, bookmark, started_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+updateColumns,
		u.Status, u.StartBookmark, u.Bookmark, u.StartedAt)
	created, err := scanUpdate(row)
	if isUniqueViolation(err, "updates_one_unfinished") {
		return importer.Update{}, importer.ErrUnfinishedUpdate
	}
	if err != nil {
		return importer.Update{}, fmt.Errorf("create update: %w", err)
	}
	return created, nil
}

func (s *Store) GetUpdate(ctx context.Context, id int64) (importer.Update, error) {
	u, err := scanUpdate(s.pool.QueryRow(ctx, `SELECT `+updateColumns+` FROM updates WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return importer.Update{}, importer.ErrNotFound
	}
	if err != nil {
		return importer.Update{}, fmt.Errorf("get update %d: %w", id, err)
	}
	return u, nil
}

func (s *Store) LastBookmark(ctx context.Context) (int64, error) {
	var b int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(bookmark), 0) FROM updates`).Scan(&b)
	if err != nil {
		return 0, fmt.Errorf("last bookmark: %w", err)
	}
	return b, nil
}

func (s *Store) SaveState(ctx context.Context, u importer.Update) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE updates
		SET status = $2, target = $3, refills = $4, error = $5, finished_at = $6
		WHERE id = $1`,
		u.ID, u.Status, u.Target, u.Refills, u.Error, u.FinishedAt)
	if isUniqueViolation(err, "updates_one_unfinished") {
		return importer.ErrUnfinishedUpdate
	}
	if err != nil {
		return fmt.Errorf("save update %d: %w", u.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return importer.ErrNotFound
	}
	return nil
}

func (s *Store) MarkRunningAsInterrupted(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE updates
		SET status = 'interrupted', error = 'the application stopped while this update was running'
		WHERE status = 'running'
		RETURNING id`)
	if err != nil {
		return nil, fmt.Errorf("mark running updates interrupted: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// AddProblem records a problem unless the same kind is already open for
// the update (a supplier that fails at every resume is one problem).
func (s *Store) AddProblem(ctx context.Context, p importer.Problem) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO problems (update_id, batch_seq, sku, version, kind, message, created_at)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM problems WHERE update_id = $1 AND kind = $5 AND resolved_at IS NULL)`,
		p.UpdateID, p.BatchSeq, p.SKU, p.Version, p.Kind, p.Message, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("record problem: %w", err)
	}
	return nil
}

func (s *Store) ResolveProblems(ctx context.Context, updateID int64, kind importer.ProblemKind) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE problems SET resolved_at = now()
		WHERE update_id = $1 AND kind = $2 AND resolved_at IS NULL`,
		updateID, kind)
	if err != nil {
		return fmt.Errorf("resolve problems: %w", err)
	}
	return nil
}

// InBatchTx runs fn in one database transaction: commit if fn succeeds,
// rollback otherwise (D4).
func (s *Store) InBatchTx(ctx context.Context, fn func(tx importer.BatchTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin batch: %w", err)
	}
	defer tx.Rollback(context.Background()) // no-op after Commit

	if err := fn(&batchTx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
