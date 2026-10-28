package httpapi

import (
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// JSON shapes returned by the API. Kept separate from the domain types so
// the API can stay stable when internals change.

type updateJSON struct {
	ID            int64          `json:"id"`
	Status        string         `json:"status"`
	CanResume     bool           `json:"can_resume"`
	StartBookmark int64          `json:"start_bookmark"`
	Bookmark      int64          `json:"bookmark"`
	NextBatch     int64          `json:"next_batch"`
	Target        int64          `json:"target"`
	Refills       int            `json:"refills"`
	BatchesDone   int            `json:"batches_done"`
	Changes       map[string]int `json:"changes"`
	OpenProblems  int            `json:"open_problems"`
	Error         string         `json:"error,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
}

func toUpdateJSON(u importer.Update, counts map[catalog.Outcome]int, openProblems int) updateJSON {
	changes := map[string]int{}
	for _, o := range []catalog.Outcome{
		catalog.OutcomeCreated, catalog.OutcomeUpdated, catalog.OutcomeWithdrawn,
		catalog.OutcomeRestocked, catalog.OutcomeDuplicate, catalog.OutcomeStale,
	} {
		changes[string(o)] = counts[o]
	}
	return updateJSON{
		ID:            u.ID,
		Status:        string(u.Status),
		CanResume:     u.Status.CanResume(),
		StartBookmark: u.StartBookmark,
		Bookmark:      u.Bookmark,
		NextBatch:     u.Bookmark + 1,
		Target:        u.Target,
		Refills:       u.Refills,
		BatchesDone:   u.BatchesDone,
		Changes:       changes,
		OpenProblems:  openProblems,
		Error:         u.Error,
		StartedAt:     u.StartedAt,
		FinishedAt:    u.FinishedAt,
	}
}

type changeJSON struct {
	BatchSeq int64  `json:"batch"`
	SKU      string `json:"sku"`
	Version  int64  `json:"version"`
	Outcome  string `json:"outcome"`
	Detail   string `json:"detail"`
}

func toChangeJSON(c importer.ChangeRecord) changeJSON {
	return changeJSON{BatchSeq: c.BatchSeq, SKU: c.SKU, Version: c.Version, Outcome: string(c.Outcome), Detail: c.Detail}
}

type problemJSON struct {
	ID         int64      `json:"id"`
	UpdateID   int64      `json:"update_id"`
	BatchSeq   int64      `json:"batch"`
	SKU        string     `json:"sku"`
	Version    int64      `json:"version"`
	Kind       string     `json:"kind"`
	Message    string     `json:"message"`
	Open       bool       `json:"open"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

func toProblemJSON(p importer.Problem) problemJSON {
	return problemJSON{
		ID: p.ID, UpdateID: p.UpdateID, BatchSeq: p.BatchSeq, SKU: p.SKU, Version: p.Version,
		Kind: string(p.Kind), Message: p.Message, Open: p.ResolvedAt == nil,
		CreatedAt: p.CreatedAt, ResolvedAt: p.ResolvedAt,
	}
}

type productJSON struct {
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceCents  int64  `json:"price_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
}

func toProductJSON(p catalog.Product) productJSON {
	return productJSON{
		SKU: p.SKU, Name: p.Name, Description: p.Description, PriceCents: p.PriceCents,
		Currency: p.Currency, Status: string(p.Status), Version: p.Version,
	}
}
