package fake

import (
	"github.com/hoangvu1118/catalogtracker/internal/catalog"
)

// Expected is what the shop's catalog must look like after every batch of a
// feed has been imported, worked out without the importer.
//
// For each SKU the valid change with the highest version wins, whatever the
// delivery order. Invalid changes newer than that winner stay open problems.
type Expected struct {
	Products map[string]ExpectedProduct
	// OpenProblems holds the invalid changes that must still need
	// attention, keyed by SKU and version.
	OpenProblems map[ProblemKey]bool
}

type ExpectedProduct struct {
	Status  catalog.Status
	Version int64
	// Details are only checked for active products: a withdrawn product
	// keeps whatever details it had when it was withdrawn.
	Name       string
	PriceCents int64
	Currency   string
}

type ProblemKey struct {
	SKU     string
	Version int64
}

// ExpectedFor computes the expected catalog for a whole feed.
func ExpectedFor(f Feed) Expected {
	best := map[string]catalog.Change{}
	var invalid []catalog.Change

	for _, w := range f.Changes() {
		ch := w.ToChange()
		if ch.Validate() != nil {
			invalid = append(invalid, ch)
			continue
		}
		if cur, ok := best[ch.SKU]; !ok || ch.Version > cur.Version {
			best[ch.SKU] = ch
		}
	}

	exp := Expected{Products: map[string]ExpectedProduct{}, OpenProblems: map[ProblemKey]bool{}}
	for sku, ch := range best {
		p := ExpectedProduct{Status: catalog.StatusActive, Version: ch.Version,
			Name: ch.Name, PriceCents: ch.PriceCents, Currency: ch.Currency}
		if ch.Op == catalog.OpWithdraw {
			p = ExpectedProduct{Status: catalog.StatusWithdrawn, Version: ch.Version}
		}
		exp.Products[sku] = p
	}
	for _, ch := range invalid {
		if ch.Version > best[ch.SKU].Version {
			exp.OpenProblems[ProblemKey{ch.SKU, ch.Version}] = true
		}
	}
	return exp
}
