# CatalogTracker

Keeps a shop's local copy of a supplier's catalog up to date. Staff start an update and see
whether it completed, what changed, and which problems need attention.

> Status: requirements clarified; implementation not started yet.

## Documentation

1. [The problem in plain words](docs/01-problem.md)
2. [Decisions: how we solve each problem](docs/02-decisions.md)
3. [Implementation plan](docs/03-plan.md)

## Requirements

- Go 1.24+
- Docker (for PostgreSQL)

## Project layout

```
cmd/catalog/        staff application (wires everything together)
cmd/fakesupplier/   fictional supplier used for the demo
internal/catalog/   products and the version rule
internal/importer/  the update process (windows, bookmark, status)
internal/supplier/  supplier HTTP client
internal/storage/   PostgreSQL storage
internal/httpapi/   JSON API
internal/web/       staff page
docs/               problem, decisions, plan
```

## Running the application

```sh
make db-up      # start PostgreSQL in Docker
make supplier   # terminal 1: fictional supplier
make run        # terminal 2: staff application
```

_Details to be added as features land._

## Running the tests

```sh
make db-up       # tests that touch the database need PostgreSQL
make test        # all tests
make test-unit   # only tests that need no database (database tests are skipped)
```

Database tests read `DATABASE_URL` and each run in a throwaway schema, so they never touch
your data.

## Assumptions

See [Assumptions in 02-decisions.md](docs/02-decisions.md#assumptions).

## Unfinished work

_To be added._

## Use of AI tools

_To be added._
