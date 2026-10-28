package importer

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
)

// applyBatch saves one batch all or nothing (D4): products, change log,
// problems and the bookmark are written in a single transaction.
func (s *Service) applyBatch(ctx context.Context, updateID int64, b Batch) error {
	return s.store.InBatchTx(ctx, func(tx BatchTx) error {
		current, err := tx.LockProducts(ctx, skusOf(b.Changes))
		if err != nil {
			return err
		}

		for _, ch := range b.Changes {
			if err := s.applyChange(ctx, tx, updateID, b.Seq, ch, current); err != nil {
				return err
			}
		}
		return tx.AdvanceBookmark(ctx, updateID, b.Seq)
	})
}

// applyChange runs one change through the version rule. current is updated
// in place, so later changes in the same batch see earlier ones.
func (s *Service) applyChange(ctx context.Context, tx BatchTx, updateID, seq int64, ch catalog.Change, current map[string]catalog.Product) error {
	var stored *catalog.Product
	if p, ok := current[ch.SKU]; ok {
		stored = &p
	}

	// A change that is not newer than what we have would be ignored anyway,
	// so it does not need attention even if its details are invalid.
	notNewer := stored != nil && ch.Version <= stored.Version
	if !notNewer {
		if err := ch.Validate(); err != nil {
			return tx.RecordProblem(ctx, Problem{
				UpdateID:  updateID,
				BatchSeq:  seq,
				SKU:       ch.SKU,
				Version:   ch.Version,
				Kind:      ProblemInvalidChange,
				Message:   err.Error(),
				CreatedAt: s.now(),
			})
		}
	}

	outcome, next := catalog.Decide(stored, ch)
	if outcome.Applies() {
		if err := tx.SaveProduct(ctx, next); err != nil {
			return err
		}
		if err := tx.ResolveInvalidChanges(ctx, next.SKU, next.Version); err != nil {
			return err
		}
		current[next.SKU] = next
	}

	return tx.RecordChange(ctx, ChangeRecord{
		UpdateID:  updateID,
		BatchSeq:  seq,
		SKU:       ch.SKU,
		Version:   ch.Version,
		Outcome:   outcome,
		Detail:    describe(stored, next, ch, outcome),
		CreatedAt: s.now(),
	})
}

func skusOf(changes []catalog.Change) []string {
	seen := make(map[string]bool, len(changes))
	var skus []string
	for _, ch := range changes {
		if ch.SKU != "" && !seen[ch.SKU] {
			seen[ch.SKU] = true
			skus = append(skus, ch.SKU)
		}
	}
	return skus
}

// describe writes a short human-readable summary for the change log.
func describe(before *catalog.Product, after catalog.Product, ch catalog.Change, outcome catalog.Outcome) string {
	switch outcome {
	case catalog.OutcomeCreated, catalog.OutcomeRestocked:
		return fmt.Sprintf("%s, %s", after.Name, money(after.PriceCents, after.Currency))
	case catalog.OutcomeUpdated:
		var diffs []string
		if before.Name != after.Name {
			diffs = append(diffs, fmt.Sprintf("name %q → %q", before.Name, after.Name))
		}
		if before.PriceCents != after.PriceCents || before.Currency != after.Currency {
			diffs = append(diffs, fmt.Sprintf("price %s → %s", money(before.PriceCents, before.Currency), money(after.PriceCents, after.Currency)))
		}
		if before.Description != after.Description {
			diffs = append(diffs, "description changed")
		}
		if len(diffs) == 0 {
			return "no visible change"
		}
		return strings.Join(diffs, "; ")
	case catalog.OutcomeWithdrawn:
		if after.Name != "" {
			return after.Name + " withdrawn from sale"
		}
		return "withdrawn before the shop knew it"
	case catalog.OutcomeDuplicate:
		return fmt.Sprintf("v%d already stored", ch.Version)
	case catalog.OutcomeStale:
		return fmt.Sprintf("v%d is older than stored v%d", ch.Version, before.Version)
	}
	return ""
}

func money(cents int64, currency string) string {
	return fmt.Sprintf("%d.%02d %s", cents/100, cents%100, currency)
}
