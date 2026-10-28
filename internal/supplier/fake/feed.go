// Package fake is a fictional supplier ("Acme Wholesale") used for the demo
// and tests. It serves a feed of batches over HTTP and can misbehave on
// purpose: outages, random errors, slow answers and a feed that grows while
// it is being read.
package fake

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoangvu1118/catalogtracker/internal/supplier"
)

// Feed is the complete list of batches the supplier will ever publish.
type Feed struct {
	Batches []supplier.WireBatch `json:"batches"`
	// Stats describes how messy a generated feed is (empty for files).
	Stats *Stats `json:"stats,omitempty"`
}

// LoadFile reads a feed from a JSON file. Batches without a seq are
// numbered in file order.
func LoadFile(path string) (Feed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Feed{}, err
	}
	var f Feed
	if err := json.Unmarshal(data, &f); err != nil {
		return Feed{}, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range f.Batches {
		if f.Batches[i].Seq == 0 {
			f.Batches[i].Seq = int64(i + 1)
		}
	}
	return f, nil
}

// Changes returns every change in the feed, in delivery order.
func (f Feed) Changes() []supplier.WireChange {
	var out []supplier.WireChange
	for _, b := range f.Batches {
		out = append(out, b.Changes...)
	}
	return out
}
