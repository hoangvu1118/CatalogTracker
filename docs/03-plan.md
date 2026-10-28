# 3. Implementation plan

Built from the decisions in [02-decisions.md](02-decisions.md).

## Technology

| Choice | Why |
|---|---|
| **Go** | required by the assignment |
| **PostgreSQL** (via Docker) | real transactions make "all or nothing" per batch (D4) simple and reliable |
| `pgx` driver | the standard Go driver for PostgreSQL |
| Go standard library for the rest | `net/http` (API), `html/template` (web page), `testing` |
| **HTTP API + simple web page** | staff start updates and see status, changes and problems |

## Structure (SOLID)

```
cmd/
  catalog/        the application: wires everything together and starts the web server
  fakesupplier/   the fictional supplier, which can misbehave on purpose
internal/
  catalog/        products and the version rule (D2) — no database, no network
  importer/       the update process: windows, bookmark, status (D4–D6)
  supplier/       talks to the supplier over HTTP
  storage/        saves to PostgreSQL
  httpapi/        JSON endpoints
  web/            the staff page
docs/             these documents
```

- **S — single responsibility:** the rule, the process, storage, supplier access and the web layer
  are separate packages.
- **O — open/closed:** a different supplier means a new supplier type; the importer does not change.
- **L — substitution:** test fakes and real implementations are interchangeable.
- **I — small interfaces:** each interface has only the methods its user needs.
- **D — dependency inversion:** the importer depends on interfaces; only `cmd/catalog` knows
  about PostgreSQL and HTTP.

## Milestones (one commit each)

| # | Commit | Delivers |
|---|---|---|
| 0 | `docs: requirement clarification` | these documents |
| 1 | `chore: project skeleton` | Go module, folders, Makefile, Docker Compose for PostgreSQL |
| 2 | `feat(catalog): product model and version rule` | D2, D3, with table tests |
| 3 | `feat(importer): update status and interfaces` | D6 |
| 4 | `feat(importer): two-window batch loop` | D5, tested with in-memory fakes |
| 5 | `feat(storage): database schema` | tables for products, updates, changes, problems |
| 6 | `feat(storage): save each batch in one transaction` | D4 |
| 7 | `test(storage): crash and repeat tests` | R1, R4, R5 against a real database |
| 8 | `feat(supplier): fictional supplier and client` | duplicates, wrong order, outages on demand |
| 9 | `feat(app): start-up recovery and shutdown` | Interrupted status (D6) |
| 10 | `feat(httpapi): JSON endpoints` | start, resume, status, changes, problems, products |
| 11 | `feat(web): staff page` | start/resume buttons, status, changes, problems, catalog |
| 12 | `docs: README, demo script, AI usage` | submission |

Every commit builds and passes `go test ./...`.

## Demo scenario (3–5 minute video)

1. Start an update: it completes; the catalog and change log appear.
2. Run it again: everything is `duplicate`, no new products (R1).
3. Feed with old and out-of-order changes: they are `stale`; withdrawn stays withdrawn (R2, R3).
4. Supplier goes down mid-update: **Failed**, catalog intact (R4, R6).
5. Supplier comes back: **Resume** continues from the bookmark and completes (R5).
6. Stop the application mid-update, restart: **Interrupted**, then Resume (R5, R6).
