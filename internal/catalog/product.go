package catalog

// Status says whether a product can be sold.
type Status string

const (
	StatusActive    Status = "active"
	StatusWithdrawn Status = "withdrawn"
)

// Product is the shop's copy of one supplier product.
//
// Withdrawn products are kept (never deleted) so that their Version
// remembers which changes are old (docs/02-decisions.md, D3).
type Product struct {
	SKU         string
	Name        string
	Description string
	PriceCents  int64
	Currency    string
	Status      Status
	Version     int64
}

// Op is what a supplier change asks us to do.
type Op string

const (
	OpUpsert   Op = "upsert"   // create the product or change its details
	OpWithdraw Op = "withdraw" // stop selling the product
)

// Change is one supplier message about one product.
type Change struct {
	SKU         string
	Version     int64
	Op          Op
	Name        string
	Description string
	PriceCents  int64
	Currency    string
}
