package catalog

// Outcome is what happened when a change was compared with the stored product.
type Outcome string

const (
	OutcomeCreated   Outcome = "created"   // new product added
	OutcomeUpdated   Outcome = "updated"   // details changed
	OutcomeWithdrawn Outcome = "withdrawn" // product withdrawn from sale
	OutcomeRestocked Outcome = "restocked" // withdrawn product made active again
	OutcomeDuplicate Outcome = "duplicate" // same version already stored, ignored
	OutcomeStale     Outcome = "stale"     // older than the stored version, ignored
)

// Applies reports whether the outcome changes the stored product.
func (o Outcome) Applies() bool {
	switch o {
	case OutcomeCreated, OutcomeUpdated, OutcomeWithdrawn, OutcomeRestocked:
		return true
	}
	return false
}

// Decide applies the version rule (docs/02-decisions.md, D2).
//
// current is the stored product, or nil if the shop has never seen the SKU.
// The change must already be valid (see Change.Validate).
//
// It returns the outcome and, when Outcome.Applies is true, the product to
// store. The rule only compares versions: bigger wins, equal is a duplicate,
// smaller is stale. Gaps between versions do not matter.
func Decide(current *Product, ch Change) (Outcome, Product) {
	if current != nil {
		switch {
		case ch.Version == current.Version:
			return OutcomeDuplicate, *current
		case ch.Version < current.Version:
			return OutcomeStale, *current
		}
	}

	if ch.Op == OpWithdraw {
		return OutcomeWithdrawn, withdraw(current, ch)
	}

	next := Product{
		SKU:         ch.SKU,
		Name:        ch.Name,
		Description: ch.Description,
		PriceCents:  ch.PriceCents,
		Currency:    ch.Currency,
		Status:      StatusActive,
		Version:     ch.Version,
	}
	switch {
	case current == nil:
		return OutcomeCreated, next
	case current.Status == StatusWithdrawn:
		return OutcomeRestocked, next
	default:
		return OutcomeUpdated, next
	}
}

// withdraw keeps the last known details and marks the product withdrawn.
// A withdrawal for an unknown SKU still creates a withdrawn row, so that
// older changes for it arriving later are recognised as stale.
func withdraw(current *Product, ch Change) Product {
	p := Product{SKU: ch.SKU}
	if current != nil {
		p = *current
	}
	p.Status = StatusWithdrawn
	p.Version = ch.Version
	return p
}
