package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := Change{SKU: "LAMP-RED", Version: 1, Op: OpUpsert, Name: "Red Lamp", PriceCents: 3000, Currency: "EUR"}

	tests := []struct {
		name    string
		modify  func(c *Change)
		wantErr string // empty means valid
	}{
		{"valid upsert", func(c *Change) {}, ""},
		{"valid withdrawal without details", func(c *Change) { *c = Change{SKU: "LAMP-RED", Version: 2, Op: OpWithdraw} }, ""},
		{"missing SKU", func(c *Change) { c.SKU = "  " }, "missing SKU"},
		{"zero version", func(c *Change) { c.Version = 0 }, "version must be 1 or more"},
		{"missing name", func(c *Change) { c.Name = "" }, "missing name"},
		{"zero price", func(c *Change) { c.PriceCents = 0 }, "price must be positive"},
		{"negative price", func(c *Change) { c.PriceCents = -5 }, "price must be positive"},
		{"lower-case currency", func(c *Change) { c.Currency = "eur" }, "invalid currency"},
		{"unknown operation", func(c *Change) { c.Op = "delete" }, "unknown operation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid
			tt.modify(&c)
			err := c.Validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !errors.Is(err, ErrInvalidChange) {
				t.Errorf("error should wrap ErrInvalidChange: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReportsAllReasons(t *testing.T) {
	err := Change{Op: OpUpsert}.Validate()
	for _, want := range []string{"missing SKU", "version", "missing name", "price", "currency"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}
