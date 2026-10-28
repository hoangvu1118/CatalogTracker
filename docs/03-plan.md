# 3. Implementation plan

Built from the decisions in [02-decisions.md](02-decisions.md). All milestones are done.

## Technology

| Choice | Why |
|---|---|
| **Go** | required by the assignment |
| **PostgreSQL** (via Docker) | real transactions make "all or nothing" per batch (D4) simple and reliable |
| `pgx` driver | the standard Go driver for PostgreSQL (the only external dependency) |
| Go standard library for the rest | `net/http` (API), `embed` (web page), `log/slog`, `testing` |
| **HTTP API + simple web page** | staff start updates and see status, changes and problems |

## Structure (SOLID)

```
cmd/
  catalog/          the application: reads flags, starts internal/app
  fakesupplier/     the fictional supplier, which can misbehave on purpose
internal/
  catalog/          products and the version rule (D2, D3): no database, no network
  importer/         the update process: windows, bookmark, status (D4–D6), and the
                    interfaces it needs (Feed, Store)
  supplier/         HTTP client for the supplier (implements importer.Feed)
  supplier/fake/    the fictional supplier: server, story feed loader, generator, expected results
  storage/          PostgreSQL (implements importer.Store), migrations, read queries
  httpapi/          JSON endpoints
  web/              the staff page (embedded HTML/CSS/JS)
  app/              wires everything together; start-up recovery; graceful shutdown
  testdb/           gives tests a throwaway database schema
e2e/                end-to-end tests over HTTP with real processes' building blocks
testdata/feeds/     the hand-written demo story feed
docs/               these documents
```

Dependencies point inwards: `catalog` ← `importer` ← `storage`, `supplier`, `httpapi` ← `app`.

- **S, single responsibility:** the rule, the process, storage, supplier access and the web layer
  are separate packages.
- **O, open/closed:** a different supplier means a new type implementing `importer.Feed`; the
  importer does not change.
- **L, substitution:** the in-memory test store and the PostgreSQL store pass the same importer
  tests; the fake and real suppliers are interchangeable.
- **I, small interfaces:** `Feed` has two methods; the API asks for an `Updater` and a `Reader`,
  not the whole store.
- **D, dependency inversion:** the importer defines the interfaces it needs and depends only on
  them; only `internal/app` knows about PostgreSQL and HTTP.

## Milestones (one commit each)

| # | Commit | Delivers | Verified by |
|---|---|---|---|
| 0 | `docs: requirement clarification` | these documents | review |
| 1 | `chore: project skeleton` | Go module, folders, Makefile, Docker Compose | build, run |
| 2 | `feat(catalog): product model and version rule` | D2, D3 | table tests of every D2 row; order-independence test |
| 3 | `feat(importer): update status and interfaces` | D6 | transition tests |
| 4 | `feat(importer): two-window batch loop` | D4, D5, D6, D8 | 14 tests with in-memory fakes |
| 5 | `feat(storage): database schema and migrations` | tables, one-unfinished index | migration tests on PostgreSQL |
| 6 | `feat(storage): save each batch in one transaction` | D4 in SQL | commit/rollback tests |
| 7 | `test(storage): crash and repeat tests` | R1, R4, R5, R6 on PostgreSQL | crash mid-batch, repeat imports, outage, hard crash, two instances |
| 8 | `feat(supplier): fictional supplier, datasets and client` | outages on demand, story and generated feeds | client retry tests, generator tests, curl |
| 9 | `feat(app): composition root, start-up recovery and shutdown` | Interrupted status | Ctrl+C and hard-crash tests, running the binaries |
| 10 | `feat(httpapi): JSON endpoints` | start, resume, status, changes, problems, products | handler tests, curl against the story feed |
| 11 | `feat(web): staff page` | the staff page | page tests, browser screenshots |
| 12 | `docs: README, code tour, demo script` | submission docs | review |
| 13 | `test(e2e): end-to-end scenarios` | every rule over HTTP, generated feeds | `make e2e` |

## Demo scenario (3–5 minute video)

See [the demo script in the README](../README.md#demo-script-35-minutes).
