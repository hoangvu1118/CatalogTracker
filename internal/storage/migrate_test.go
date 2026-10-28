package storage

import (
	"context"
	"testing"
)

func TestMigrateCreatesTablesAndIsRepeatable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}

	for _, table := range []string{"products", "updates", "update_changes", "problems"} {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err %v)", table, err)
		}
	}

	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("applied migrations = %d (err %v), want 1", applied, err)
	}
}

func TestSchemaAllowsOnlyOneUnfinishedUpdate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	insert := `INSERT INTO updates (status, start_bookmark, bookmark, started_at) VALUES ($1, 0, 0, now())`
	if _, err := pool.Exec(ctx, insert, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, insert, "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, insert, "running"); err == nil {
		t.Fatal("a second unfinished update must be rejected")
	}
}
