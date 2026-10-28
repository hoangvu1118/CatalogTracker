package fake

import (
	"fmt"
	"math/rand/v2"

	"github.com/hoangvu1118/catalogtracker/internal/supplier"
)

// GenerateOptions shapes a generated feed. Zero values use defaults.
type GenerateOptions struct {
	Seed         uint64
	Products     int     // distinct products (default 60)
	Batches      int     // roughly how many batches (default 40)
	MaxBatchSize int     // largest batch (default 12)
	LateRate     float64 // chance a change is delivered 1–5 batches late (default 0.15)
	RepeatRate   float64 // chance a change is delivered again later (default 0.10)
	InvalidRate  float64 // chance the supplier sends a broken version (default 0.04)
}

// Stats counts the imperfections put into a generated feed.
type Stats struct {
	Products     int `json:"products"`
	Changes      int `json:"changes"`   // distinct changes the supplier made
	Delivered    int `json:"delivered"` // messages actually delivered
	Late         int `json:"late"`
	Repeated     int `json:"repeated"`
	ReplayedBat  int `json:"replayed_batches"`
	Shuffled     int `json:"shuffled_batches"`
	Invalid      int `json:"invalid"`
	VersionGaps  int `json:"version_gaps"`
	Withdrawals  int `json:"withdrawals"`
	Restocks     int `json:"restocks"`
	EmptyBatches int `json:"empty_batches"`
	Currencies   int `json:"currencies"`
}

func (o *GenerateOptions) defaults() {
	if o.Products <= 0 {
		o.Products = 60
	}
	if o.Batches <= 0 {
		o.Batches = 40
	}
	if o.MaxBatchSize <= 0 {
		o.MaxBatchSize = 12
	}
	if o.LateRate == 0 {
		o.LateRate = 0.15
	}
	if o.RepeatRate == 0 {
		o.RepeatRate = 0.10
	}
	if o.InvalidRate == 0 {
		o.InvalidRate = 0.04
	}
}

var (
	kinds = []struct {
		code, name string
		basePrice  int64
	}{
		{"MUG", "Ceramic Mug", 1200}, {"LAMP", "Desk Lamp", 3900}, {"CHAIR", "Oak Chair", 8900},
		{"VASE", "Glass Vase", 2500}, {"CUSH", "Linen Cushion", 1900}, {"THROW", "Wool Throw", 5900},
		{"TRAY", "Bamboo Tray", 1500}, {"TOWEL", "Cotton Towel", 900}, {"KETTLE", "Steel Kettle", 4500},
		{"POT", "Plant Pot", 1100}, {"CLOCK", "Wall Clock", 3200}, {"RUG", "Jute Rug", 12900},
	}
	colors     = []string{"Blue", "Red", "Green", "Black", "White", "Sand", "Grey", "Teal"}
	sizes      = []string{"S", "M", "L"}
	currencies = []string{"EUR", "EUR", "EUR", "EUR", "EUR", "EUR", "EUR", "EUR", "USD", "GBP"}
)

// product is the supplier's own, always-correct view of one product.
type product struct {
	sku, name, desc string
	price           int64
	currency        string
	version         int64
	created         bool
	withdrawn       bool
}

// Generate builds a realistic, messy feed. The same options always give
// the same feed.
//
// It first plays the supplier's true history (creates, price changes,
// renames, withdrawals, restocks, version gaps, broken records), cuts it
// into batches of varying size, then spoils the delivery: late messages,
// repeated messages, replayed batches and shuffled batches.
func Generate(o GenerateOptions) Feed {
	o.defaults()
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0x5eed))
	stats := &Stats{Products: o.Products}

	products := makeProducts(rng, o.Products)
	usedCurrencies := map[string]bool{}
	for _, p := range products {
		usedCurrencies[p.currency] = true
	}
	stats.Currencies = len(usedCurrencies)

	// 1. True history, in the order the supplier made the changes.
	target := o.Batches * (o.MaxBatchSize + 1) / 2
	var history []supplier.WireChange
	for len(history) < target {
		p := products[rng.IntN(len(products))]
		history = append(history, nextChanges(rng, p, o.InvalidRate, stats)...)
	}
	// A few records so broken that even the SKU is missing.
	for i := 0; i < max(1, o.Batches/20); i++ {
		history = append(history, supplier.WireChange{Version: 1, Op: "upsert", Name: "Unknown item", PriceCents: 500, Currency: "EUR"})
		stats.Invalid++
	}
	stats.Changes = len(history)

	// 2. Cut into batches of varying size; a few are empty.
	var batches [][]supplier.WireChange
	for i := 0; i < len(history); {
		if rng.Float64() < 0.03 {
			batches = append(batches, nil)
			stats.EmptyBatches++
			continue
		}
		n := 1 + rng.IntN(o.MaxBatchSize)
		end := min(i+n, len(history))
		batches = append(batches, append([]supplier.WireChange(nil), history[i:end]...))
		i = end
	}

	// 3. Spoil the delivery.
	last := len(batches) - 1
	for b := 0; b < len(batches); b++ {
		var keep []supplier.WireChange
		for _, ch := range batches[b] {
			switch {
			case b < last && rng.Float64() < o.LateRate:
				to := min(b+1+rng.IntN(5), last)
				batches[to] = append(batches[to], ch)
				stats.Late++
			default:
				keep = append(keep, ch)
			}
			if b < last && rng.Float64() < o.RepeatRate {
				to := min(b+rng.IntN(7), last)
				batches[to] = append(batches[to], ch)
				stats.Repeated++
			}
		}
		batches[b] = keep
	}
	for b := 1; b < len(batches); b++ {
		if rng.Float64() < 0.05 { // the supplier re-sends a whole earlier batch
			batches[b] = append(batches[b], batches[b-1]...)
			stats.ReplayedBat++
		}
		if len(batches[b]) > 1 && rng.Float64() < 0.25 {
			rng.Shuffle(len(batches[b]), func(i, j int) { batches[b][i], batches[b][j] = batches[b][j], batches[b][i] })
			stats.Shuffled++
		}
	}

	feed := Feed{Stats: stats}
	for i, changes := range batches {
		if changes == nil {
			changes = []supplier.WireChange{}
		}
		stats.Delivered += len(changes)
		feed.Batches = append(feed.Batches, supplier.WireBatch{Seq: int64(i + 1), Changes: changes})
	}
	return feed
}

func makeProducts(rng *rand.Rand, n int) []*product {
	seen := map[string]bool{}
	var out []*product
	for len(out) < n {
		k := kinds[rng.IntN(len(kinds))]
		color := colors[rng.IntN(len(colors))]
		size := sizes[rng.IntN(len(sizes))]
		sku := fmt.Sprintf("%s-%s-%s", k.code, color[:3], size)
		if seen[sku] {
			if len(seen) >= len(kinds)*len(colors)*len(sizes) {
				break
			}
			continue
		}
		seen[sku] = true
		out = append(out, &product{
			sku:      sku,
			name:     fmt.Sprintf("%s %s (%s)", color, k.name, size),
			desc:     fmt.Sprintf("%s %s, size %s.", color, k.name, size),
			price:    k.basePrice + int64(rng.IntN(800)) - 400 + int64(sizeIndex(size))*300,
			currency: currencies[rng.IntN(len(currencies))],
		})
	}
	return out
}

func sizeIndex(s string) int {
	for i, v := range sizes {
		if v == s {
			return i
		}
	}
	return 0
}

// nextChanges advances one product's true history by one event.
func nextChanges(rng *rand.Rand, p *product, invalidRate float64, stats *Stats) []supplier.WireChange {
	bump := func() {
		step := int64(1)
		if p.version > 0 && rng.Float64() < 0.10 {
			step += 1 + int64(rng.IntN(2)) // the supplier skipped version numbers
			stats.VersionGaps++
		}
		p.version += step
	}

	var out []supplier.WireChange
	if p.created && rng.Float64() < invalidRate {
		bump()
		out = append(out, broken(rng, p))
		stats.Invalid++
	}

	switch {
	case !p.created:
		p.created = true
	case p.withdrawn:
		if rng.Float64() < 0.5 {
			return out // stays withdrawn for now
		}
		p.withdrawn = false
		p.price = adjust(rng, p.price)
		stats.Restocks++
	default:
		switch r := rng.Float64(); {
		case r < 0.55:
			p.price = adjust(rng, p.price)
		case r < 0.75:
			p.name = rename(rng, p.name)
			p.desc = p.name + " — updated description."
		default:
			p.withdrawn = true
		}
	}

	bump()
	if p.withdrawn {
		stats.Withdrawals++
		return append(out, supplier.WireChange{SKU: p.sku, Version: p.version, Op: "withdraw"})
	}
	return append(out, supplier.WireChange{
		SKU: p.sku, Version: p.version, Op: "upsert",
		Name: p.name, Description: p.desc, PriceCents: p.price, Currency: p.currency,
	})
}

// broken returns a version of the product with a data error in it.
func broken(rng *rand.Rand, p *product) supplier.WireChange {
	c := supplier.WireChange{
		SKU: p.sku, Version: p.version, Op: "upsert",
		Name: p.name, Description: p.desc, PriceCents: p.price, Currency: p.currency,
	}
	switch rng.IntN(4) {
	case 0:
		c.Name = ""
	case 1:
		c.PriceCents = -c.PriceCents
	case 2:
		c.Currency = "eur"
	default:
		c.Op = "delete"
	}
	return c
}

func adjust(rng *rand.Rand, price int64) int64 {
	pct := int64(rng.IntN(31)) - 12 // -12% .. +18%
	next := price + price*pct/100
	return max(next/10*10+9, 99) // prices end in 9
}

var adjectives = []string{"Classic", "Nordic", "Rustic", "Modern", "Organic"}

func rename(rng *rand.Rand, name string) string {
	return adjectives[rng.IntN(len(adjectives))] + " " + name
}
