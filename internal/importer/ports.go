package importer

import (
	"context"
	"errors"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
)

// The interfaces below are what the importer needs from the outside world.
// They are defined here, where they are used, and implemented elsewhere
// (internal/supplier, internal/storage), so the importer never depends on
// HTTP or PostgreSQL directly.

// Batch is one piece of the supplier's feed. Seq numbers only grow.
type Batch struct {
	Seq     int64
	Changes []catalog.Change
}

// Feed is the supplier as seen by the importer (Window 1).
type Feed interface {
	// Latest returns the sequence number of the newest batch available.
	Latest(ctx context.Context) (int64, error)
	// Batches returns up to limit batches with Seq > after, oldest first.
	Batches(ctx context.Context, after int64, limit int) ([]Batch, error)
}

// ChangeRecord is one line of the "what changed" log (D7).
type ChangeRecord struct {
	UpdateID  int64
	BatchSeq  int64
	SKU       string
	Version   int64
	Outcome   catalog.Outcome
	Detail    string
	CreatedAt time.Time
}

// ProblemKind classifies something staff should look at (D8).
type ProblemKind string

const (
	ProblemInvalidChange       ProblemKind = "invalid_change"
	ProblemSupplierUnavailable ProblemKind = "supplier_unavailable"
)

// Problem is something that needs staff attention until it is resolved.
type Problem struct {
	ID         int64
	UpdateID   int64
	BatchSeq   int64
	SKU        string
	Version    int64
	Kind       ProblemKind
	Message    string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// UpdateStore keeps the updates themselves.
type UpdateStore interface {
	// CreateUpdate stores a new running update.
	// It returns ErrUnfinishedUpdate if another update is not completed.
	CreateUpdate(ctx context.Context, u Update) (Update, error)
	// GetUpdate returns ErrNotFound if the update does not exist.
	GetUpdate(ctx context.Context, id int64) (Update, error)
	// LastBookmark returns the furthest bookmark reached by any update.
	LastBookmark(ctx context.Context) (int64, error)
	// SaveState stores Status, Target, Refills, Error and FinishedAt.
	// Bookmark and BatchesDone are only changed by BatchTx.AdvanceBookmark.
	SaveState(ctx context.Context, u Update) error
	// MarkRunningAsInterrupted turns every running update into an
	// interrupted one and returns their ids. Called once at start-up.
	MarkRunningAsInterrupted(ctx context.Context) ([]int64, error)
	// AddProblem records a problem outside a batch (e.g. supplier outage).
	AddProblem(ctx context.Context, p Problem) error
	// ResolveProblems marks all open problems of one kind for an update resolved.
	ResolveProblems(ctx context.Context, updateID int64, kind ProblemKind) error
}

// BatchStore saves one batch all or nothing (D4).
type BatchStore interface {
	// InBatchTx runs fn in one transaction. If fn returns an error,
	// nothing fn wrote is kept.
	InBatchTx(ctx context.Context, fn func(tx BatchTx) error) error
}

// BatchTx is what can be done inside one batch transaction.
type BatchTx interface {
	// LockProducts returns the stored products for the SKUs (missing SKUs
	// are absent from the map) and locks them until the transaction ends.
	LockProducts(ctx context.Context, skus []string) (map[string]catalog.Product, error)
	SaveProduct(ctx context.Context, p catalog.Product) error
	RecordChange(ctx context.Context, c ChangeRecord) error
	// RecordProblem records a problem unless the same one (kind, SKU and
	// version) is already open.
	RecordProblem(ctx context.Context, p Problem) error
	// ResolveInvalidChanges resolves open invalid-change problems for a SKU
	// whose version is not newer than the version just applied.
	ResolveInvalidChanges(ctx context.Context, sku string, upToVersion int64) error
	// AdvanceBookmark moves the update's bookmark to seq and counts the batch.
	// It returns ErrBatchAlreadyApplied if the bookmark is already at or past seq.
	AdvanceBookmark(ctx context.Context, updateID, seq int64) error
}

// Store is everything the importer needs from storage.
type Store interface {
	UpdateStore
	BatchStore
}

var (
	ErrNotFound            = errors.New("update not found")
	ErrUnfinishedUpdate    = errors.New("an unfinished update exists; resume it first")
	ErrNotResumable        = errors.New("update cannot be resumed")
	ErrBatchAlreadyApplied = errors.New("batch already applied")
)
