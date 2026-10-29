# 4. Code tour: where things are, and how to change them

A guide for reading the code in order, and for making changes without help.

## Read in this order

| # | File | What to look for |
|---|---|---|
| 1 | `internal/catalog/decide.go` | `Decide`: the whole version rule in about 40 lines. Compare it with the table in D2. |
| 2 | `internal/catalog/decide_test.go` | The same table, as tests. |
| 3 | `internal/catalog/validate.go` | What counts as an invalid record. |
| 4 | `internal/importer/update.go` | The four statuses and which moves between them are allowed. |
| 5 | `internal/importer/ports.go` | The interfaces the importer needs: `Feed`, `UpdateStore`, `BatchStore`, `BatchTx`. |
| 6 | `internal/importer/service.go` | `run`, `fillWindow`, `processWindow`: the two windows and stop rule C. `stop`: failed vs interrupted. |
| 7 | `internal/importer/batch.go` | `applyBatch`: one batch in one transaction. `applyChange`: validate, decide, save, log. |
| 8 | `internal/storage/store.go`, `batchtx.go` | The same interfaces in SQL. Note `WHERE products.version < EXCLUDED.version` and `WHERE bookmark < $2`. |
| 9 | `internal/storage/migrations/001_init.sql` | The tables, and the index that allows only one unfinished update. |
| 10 | `internal/app/app.go` | How everything is connected; start-up recovery; shutdown order. |
| 11 | `internal/httpapi/api.go` | The endpoints. |
| 12 | `internal/httpapi/window.go` | `GET /api/window`: where the two windows are right now. |
| 13 | `internal/web/static/app.js` | The page: fetch, render, poll. `renderWindow` and `syncBoxes` draw and animate the two windows. |

## Go basics used in this code

| Go thing | Where | In one line |
|---|---|---|
| `package` / `import` | every file | a folder is a package; you import it by module path + folder |
| `internal/` | all packages | can only be imported from inside this module |
| `struct` | `catalog.Product` | a group of named fields, like a class without inheritance |
| method `func (s *Service) Start(...)` | `importer/service.go` | a function attached to a type; `*` means it can change the value |
| `interface` | `importer/ports.go` | a list of methods; any type with those methods satisfies it automatically |
| `error` as a return value | everywhere | Go has no exceptions: functions return `(result, error)` and callers check `if err != nil` |
| `errors.Is` / `%w` | `importer`, `storage` | wrap an error with context but still recognise it later |
| `context.Context` | every I/O call | carries cancellation (Ctrl+C) and timeouts down the call chain |
| goroutine `go func() {...}()` | `Service.launch` | runs a function concurrently; the update runs in the background |
| `sync.WaitGroup` | `Service.Wait` | wait until background goroutines finish |
| `defer` | `tx.Rollback` | run this when the function returns, whatever happens |
| `//go:embed` | `storage/migrate.go`, `web/web.go` | put files (SQL, HTML) inside the compiled program |
| table-driven tests | `*_test.go` | a list of cases plus one loop; `go test ./...` runs them |

## How to make common changes

### Add a product field (e.g. `brand`)
1. `internal/catalog/product.go`: add `Brand string` to `Product` and `Change`.
2. `internal/catalog/decide.go`: copy it in the `next := Product{...}` literal.
3. `internal/storage/migrations/002_brand.sql`: `ALTER TABLE products ADD COLUMN brand text NOT NULL DEFAULT '';`
   (new file; never edit an applied migration).
4. `internal/storage/batchtx.go` (`LockProducts`, `SaveProduct`) and `queries.go` (`ListProducts`):
   add the column.
5. `internal/supplier/wire.go`: add `Brand string \`json:"brand,omitempty"\`` and map it in `ToChange`.
6. `internal/httpapi/json.go` and `internal/web/static/app.js`: show it.
7. Optionally `importer/batch.go` `describe`: mention brand changes.

### Add a validation rule (e.g. names at most 200 characters)
`internal/catalog/validate.go`: append a reason in the `OpUpsert` case, then add a row to
`validate_test.go`. Invalid records automatically become problems.

### Add a new problem kind (e.g. "price dropped more than 50%")
1. `internal/importer/ports.go`: add a `ProblemKind` constant.
2. `internal/importer/batch.go` `applyChange`: after `Decide`, compare `stored` and `next` and call
   `tx.RecordProblem`.
3. Add a test in `internal/importer/service_test.go`.

### Change the refill limit or page size
Flags: `-max-refills`, `-page-size` (see `cmd/catalog/main.go`). Defaults are in
`importer.DefaultConfig`.

### Support a second supplier
Write a new type with `Latest` and `Batches` methods (it satisfies `importer.Feed`) and pass it to
`importer.NewService` in `internal/app/app.go`. Nothing in `importer` changes (O and D of SOLID).

### Let staff dismiss a problem by hand
1. Store: `ResolveProblem(ctx, id)`: `UPDATE problems SET resolved_at = now() WHERE id = $1`.
2. API: `POST /api/problems/{id}/resolve` in `httpapi/api.go`, add the method to `Reader`
   (or a new small interface).
3. Page: a button in `problemItem` in `app.js`.
