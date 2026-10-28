package supplier

import (
	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// The supplier's JSON format. Both the client and the fictional supplier
// use these types, so they always agree.
//
//	GET /feed/latest                    → LatestResponse
//	GET /feed/batches?after=N&limit=M   → BatchesResponse (oldest first)

type LatestResponse struct {
	Latest int64 `json:"latest"`
}

type BatchesResponse struct {
	Batches []WireBatch `json:"batches"`
}

type WireBatch struct {
	Seq     int64        `json:"seq"`
	Changes []WireChange `json:"changes"`
}

type WireChange struct {
	SKU         string `json:"sku"`
	Version     int64  `json:"version"`
	Op          string `json:"op"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	PriceCents  int64  `json:"price_cents,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

func (w WireChange) ToChange() catalog.Change {
	return catalog.Change{
		SKU:         w.SKU,
		Version:     w.Version,
		Op:          catalog.Op(w.Op),
		Name:        w.Name,
		Description: w.Description,
		PriceCents:  w.PriceCents,
		Currency:    w.Currency,
	}
}

func (w WireBatch) ToBatch() importer.Batch {
	changes := make([]catalog.Change, len(w.Changes))
	for i, c := range w.Changes {
		changes[i] = c.ToChange()
	}
	return importer.Batch{Seq: w.Seq, Changes: changes}
}
