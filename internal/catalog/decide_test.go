package catalog

import "testing"

func active(version, price int64) *Product {
	return &Product{SKU: "MUG-BLUE", Name: "Blue Mug", PriceCents: price, Currency: "EUR", Status: StatusActive, Version: version}
}

func withdrawn(version int64) *Product {
	p := active(version, 1000)
	p.Status = StatusWithdrawn
	return p
}

func upsert(version, price int64) Change {
	return Change{SKU: "MUG-BLUE", Version: version, Op: OpUpsert, Name: "Blue Mug", PriceCents: price, Currency: "EUR"}
}

func withdrawal(version int64) Change {
	return Change{SKU: "MUG-BLUE", Version: version, Op: OpWithdraw}
}

// The cases mirror the comparison table in docs/02-decisions.md (D2).
func TestDecide(t *testing.T) {
	tests := []struct {
		name        string
		current     *Product
		change      Change
		wantOutcome Outcome
		wantStatus  Status
		wantVersion int64
		wantPrice   int64
	}{
		{"unknown product is created", nil, upsert(1, 1000), OutcomeCreated, StatusActive, 1, 1000},
		{"newer version updates", active(2, 1200), upsert(3, 1500), OutcomeUpdated, StatusActive, 3, 1500},
		{"same version is a duplicate", active(3, 1500), upsert(3, 1500), OutcomeDuplicate, StatusActive, 3, 1500},
		{"older version is stale", active(3, 1500), upsert(2, 1200), OutcomeStale, StatusActive, 3, 1500},
		{"newer withdrawal with a gap withdraws", active(5, 3000), withdrawal(7), OutcomeWithdrawn, StatusWithdrawn, 7, 3000},
		{"older change cannot revive a withdrawn product", withdrawn(7), upsert(5, 3000), OutcomeStale, StatusWithdrawn, 7, 1000},
		{"newer change restocks a withdrawn product", withdrawn(7), upsert(8, 3200), OutcomeRestocked, StatusActive, 8, 3200},
		{"withdrawal of an unknown product keeps a tombstone", nil, withdrawal(4), OutcomeWithdrawn, StatusWithdrawn, 4, 0},
		{"duplicate withdrawal is ignored", withdrawn(7), withdrawal(7), OutcomeDuplicate, StatusWithdrawn, 7, 1000},
		{"older withdrawal does not withdraw", active(9, 1000), withdrawal(8), OutcomeStale, StatusActive, 9, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, got := Decide(tt.current, tt.change)

			if outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tt.wantOutcome)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Version != tt.wantVersion {
				t.Errorf("version = %d, want %d", got.Version, tt.wantVersion)
			}
			if got.PriceCents != tt.wantPrice {
				t.Errorf("price = %d, want %d", got.PriceCents, tt.wantPrice)
			}
		})
	}
}

func TestDecideIsOrderIndependent(t *testing.T) {
	changes := []Change{upsert(1, 1000), upsert(2, 1200), withdrawal(4), upsert(3, 1500)}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2, 2, 1}}

	for _, order := range orders {
		var current *Product
		for _, i := range order {
			outcome, next := Decide(current, changes[i])
			if outcome.Applies() {
				current = &next
			}
		}
		if current.Version != 4 || current.Status != StatusWithdrawn {
			t.Errorf("order %v: got version %d status %q, want 4 withdrawn", order, current.Version, current.Status)
		}
	}
}

func TestOutcomeApplies(t *testing.T) {
	for _, o := range []Outcome{OutcomeCreated, OutcomeUpdated, OutcomeWithdrawn, OutcomeRestocked} {
		if !o.Applies() {
			t.Errorf("%q should apply", o)
		}
	}
	for _, o := range []Outcome{OutcomeDuplicate, OutcomeStale} {
		if o.Applies() {
			t.Errorf("%q should not apply", o)
		}
	}
}
