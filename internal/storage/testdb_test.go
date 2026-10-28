package storage

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoangvu1118/catalogtracker/internal/testdb"
)

// testPool returns a pool connected to a fresh, empty schema.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := Connect(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
