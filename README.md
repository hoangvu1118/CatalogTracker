# CatalogTracker

Keeps a shop's local copy of a supplier's catalog up to date. Staff start an update and see
whether it completed, what changed, and which problems still need attention.

The supplier's data is messy (batches can be repeated, arrive out of order, or contain broken
records) and the supplier or the application can fail in the middle of an update. The design
makes sure that:

- importing the same data twice never creates duplicates;
- older information never replaces newer information or brings back a withdrawn product;
- an outage or crash never erases the catalog;
- an interrupted update resumes where it stopped, without redoing or losing finished batches;
- an update is reported as **completed** only when it really is.

![Failed update with Resume button](docs/screenshots/failed-update.png)

## Documentation

1. [The problem in plain words](docs/01-problem.md): rules R1–R6
2. [Decisions: how each problem is solved](docs/02-decisions.md): D1–D8 and assumptions
3. [Implementation plan](docs/03-plan.md): structure, SOLID, milestones
4. [Code tour](docs/04-code-tour.md): reading order and how to make common changes
5. [Demo feeds](testdata/feeds/README.md): the fictional supplier's data

## How it works, in short

- **Pull, with a bookmark.** The application asks the supplier for batches after its bookmark
  (the last batch fully saved).
- **Version rule.** Each product change has a version number. Bigger wins; equal is a duplicate;
  smaller is stale and ignored. Withdrawn products are kept (never deleted), so old messages
  cannot revive them, while a newer version can restock them.
- **One batch = one transaction.** Products, change log, problems and the bookmark are saved
  together or not at all.
- **Two windows.** Window 2 is filled with everything the supplier has (Window 1) and processed
  batch by batch. When it is empty, Window 1 is checked again; new batches are taken, up to
  3 refills, so an update always ends.
- **Honest status.** `running`, `completed`, `failed` (supplier problem) and `interrupted`
  (application stopped). Failed and interrupted updates can be resumed.

## Requirements

- Go 1.24+
- Docker (for PostgreSQL), or any PostgreSQL 16 you point `DATABASE_URL` at

## Running the application

Each command in its own terminal:

```sh
make db-up      # PostgreSQL in Docker (user/password/db: catalog)
make supplier   # fictional supplier on :9090 with the demo story (slowed down to watch)
make run        # the application on :8080
```

Open <http://localhost:8080> and press **Start update**.

Without `make` (e.g. on Windows):

```sh
docker compose up -d --wait postgres
go run ./cmd/fakesupplier -delay 700ms
go run ./cmd/catalog
```

### Application options

| Flag | Environment | Default |
|---|---|---|
| `-addr` | `ADDR` | `:8080` |
| `-database-url` | `DATABASE_URL` | `postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable` |
| `-supplier-url` | `SUPPLIER_URL` | `http://localhost:9090` |
| `-supplier-attempts` | | `3` tries per request, with exponential backoff |
| `-supplier-timeout` | | `5s` per request |
| `-page-size` | | `5` batches per supplier request |
| `-max-refills` | | `3` Window 2 refills before an update completes |

### Fictional supplier options

```sh
go run ./cmd/fakesupplier -help
```

| Flag | Meaning |
|---|---|
| `-feed FILE` | serve a feed file (default: `testdata/feeds/acme-story.json`) |
| `-generate -seed N` | serve a generated messy feed (~60 products, ~40 batches); `make supplier-big` |
| `-publish N` | only the first N batches are visible at start |
| `-grow N` | publish N more batches after every batch request (a supplier that keeps changing) |
| `-delay D` | slow every request down, e.g. `700ms` |
| `-fail-rate P` | random 503 answers with probability P |
| `-dump` | print the feed as JSON and exit |

Switch failures on and off while it runs:

```sh
curl -X POST 'localhost:9090/admin/outage?from=5'   # deliver batches 1–4, then answer 503
curl -X POST 'localhost:9090/admin/outage?on=true'  # everything answers 503
curl -X POST 'localhost:9090/admin/outage?on=false' # back to normal
curl -X POST 'localhost:9090/admin/publish?count=2' # make 2 more batches visible
curl localhost:9090/admin/state
```

### JSON API

| Method and path | Purpose |
|---|---|
| `POST /api/updates` | start an update; body `{"from_beginning": true}` re-imports everything; `409` with `unfinished_update_id` if one must be resumed first |
| `POST /api/updates/{id}/resume` | resume a failed or interrupted update |
| `GET /api/updates` | recent updates with status, progress and counts |
| `GET /api/updates/{id}` | one update: status, `batches_done`, `next_batch`, `target`, change counts, open problems |
| `GET /api/updates/{id}/changes?outcome=stale` | what changed |
| `GET /api/updates/{id}/problems` | problems found by the update |
| `GET /api/problems?open=true` | everything that still needs attention |
| `GET /api/products?status=withdrawn` | the catalog |

## Running the tests

```sh
make db-up       # tests that touch the database need PostgreSQL
make test        # all tests, with the race detector
make e2e         # only the end-to-end scenarios, with details
make test-unit   # only tests that need no database (database tests are skipped)
```

Database tests read `DATABASE_URL` and each run in a throwaway schema, so they never touch your
data.

| Package | What the tests prove |
|---|---|
| `catalog` | every row of the version-rule table; the result does not depend on arrival order |
| `importer` | full import, continue from bookmark, re-import, out-of-order, outage + resume, crash mid-batch rollback, recovery, shutdown, window refill, refill limit, invalid records |
| `storage` | the same guarantees on PostgreSQL, plus: hard crash recovery, two instances starting at once, SQL-level safety nets |
| `supplier` | retries, giving up, no retry on 4xx, cancellation; generated feeds really contain every kind of mess |
| `app` | Ctrl+C mid-update → interrupted → resume after restart; crash recovery at start-up |
| `httpapi`, `web` | endpoints, status codes, filters; the page and assets are served |

### End-to-end scenarios (`make e2e`)

These drive the whole system over HTTP: the real application and database, with the fictional
supplier misbehaving. Each scenario ends by comparing the catalog and the open problems with
`fake.ExpectedFor`, which works out the correct result from the feed alone (for each product, the
valid change with the highest version wins).

| Scenario | Rules |
|---|---|
| Demo story: outage after batch 4, start refused, resume, exact counts, each batch logged once | R1–R6 |
| Re-import a generated feed twice: nothing applied, catalog identical | R1 |
| 6 generated feeds (25–46 batches, up to 387 messages, late, repeated, replayed, shuffled, broken records), supplier failing 25% of requests, page sizes 1–50 | R1–R5 |
| Application stopped and restarted 20+ times in the middle of one update | R4–R6 |
| Real compiled program killed with `kill -9` several times mid-update | R4–R6 |
| Supplier keeps publishing while being read: refill limit holds, later updates catch up | D5 |
| 10 simultaneous "Start update" requests: exactly one starts | D6 |
| Supplier down from the start and still down at resume: catalog untouched, one problem | R4, D8 |
| Gaps in batch numbers, empty batches, a product only ever seen as a withdrawal | D2, D3 |

The suite is checked against itself: with the version rule deliberately broken (older versions
allowed to overwrite newer ones), 9 of the 10 scenarios fail.

## Demo script (3–5 minutes)

Start with an empty database: `make db-reset`.

1. **Outage in the middle (R4, R6).** With `make supplier` and `make run` running:
   `curl -X POST 'localhost:9090/admin/outage?from=5'`, then press **Start update**.
   The update stops as **Failed** after 4 batches. The catalog keeps what was saved, the page
   says "next: batch 5 of 8", and *Needs attention* shows "Supplier unavailable".
2. **Resume (R5).** `curl -X POST 'localhost:9090/admin/outage?on=false'`, then press **Resume**.
   The update continues at batch 5 and becomes **Completed**. Point out:
   - `stale`: Blue Mug v2 and Red Lamp v1 arrived late and were ignored (R2, R3);
   - `duplicate`: batch 3 was re-sent (R1);
   - Red Lamp was withdrawn, stayed withdrawn against an old message, then **restocked** by v5;
   - Steel Kettle: withdrawn before the shop ever saw it, and the late older message was ignored;
   - problems: the outage and Glass Vase v2 are **resolved**; the record without SKU and Bamboo
     Tray (`eur`) still need attention.
3. **Import again (R1).** Press **Re-import everything**: every change is `duplicate` or
   `stale`, and the catalog is unchanged.
4. **Crash and restart (R5, R6).** Stop the supplier and run `make supplier-big` (a large, messy
   generated feed). Because this is a different feed, press **Re-import everything** (a normal
   update would continue from the old feed's bookmark). While it runs, press Ctrl+C in the
   application's terminal (or `kill -9` it). Run `make run` again: the update shows
   **Interrupted · next: batch N**. Press **Resume**; it finishes from batch N.
5. **A supplier that keeps changing (D5).** Restart the supplier with
   `go run ./cmd/fakesupplier -generate -seed 7 -publish 10 -grow 2 -delay 100ms` and press
   **Re-import everything**: the window is refilled up to 3 times, then the update completes.
   Press **Start update** again to pick up the batches that were left waiting.

![Completed update after resume](docs/screenshots/resumed-update.png)

## Assumptions

Full list in [02-decisions.md](docs/02-decisions.md#assumptions). The main ones:

- The supplier gives each product a version number that only grows (gaps allowed) and numbers its
  batches in increasing order.
- The supplier keeps its batches, so they can be requested again from a bookmark.
- The application talks to one supplier feed. The bookmark belongs to that feed; pointing the
  application at a different feed needs a **Re-import everything**.
- The feed contains changes, not full snapshots: a product missing from a batch is not
  withdrawn; withdrawal is always explicit.
- One shop, one application instance at a time, no login.
- Prices are whole cents in the product's own currency; no currency conversion.
- While an update is unfinished, staff must resume it before starting another.

## Unfinished work

- **No authentication or roles.** Anyone who can reach the page can start updates.
- **Multiple application instances.** The database prevents two unfinished updates, but start-up
  recovery assumes a single instance: a second instance starting up would mark the first one's
  running update as interrupted. Leases or heartbeats would be needed.
- **A stuck (not crashed) application** leaves its update "running" until restart; there is no
  timeout or heartbeat.
- **Problems cannot be dismissed by hand**; they are only resolved automatically (see
  [code tour](docs/04-code-tour.md#let-staff-dismiss-a-problem-by-hand) for how to add it).
- **Leftover batches** after the refill limit are not shown on the page; the next update picks
  them up.
- **Long lists are not paginated** (the change log is capped at 200 rows per request by default).
- **No scheduled or automatic updates**, and no notifications.
- **No real supplier integration.** Mapping a real supplier's API onto `importer.Feed`
  (authentication, its own paging and versioning) is left for when one exists.

## Use of AI tools

I used Claude Code (Anthropic) as a pair programmer throughout:

- **Understanding the problem.** I asked it to rephrase the assignment and walked through edge
  cases with it until I could explain them myself (see the docs folder, written from that
  discussion).
- **Design.** The decisions came out of that discussion. Some were my own ideas, such as the
  two-window polling with a refill limit (stop rule C). Others I agreed to after questioning
  them, such as version numbers instead of timestamps and keeping withdrawn products.
- **Code and tests.** The AI wrote most of the Go code and tests, one commit per milestone,
  following the plan. I reviewed each commit, ran the tests and the demo, and asked for changes.
- **Verification.** Every functional commit has automated tests, plus manual checks (curl, browser
  screenshots), and the end-to-end suite checks the final catalog against an independently
  computed expected result.

<!-- Adjust this section so it honestly matches how you used AI. -->
