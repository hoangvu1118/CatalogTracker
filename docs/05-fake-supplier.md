# 5. Using the fictional supplier

`cmd/fakesupplier` pretends to be the supplier "Acme Wholesale". It serves batches of product
changes over HTTP, the same way a real supplier feed would, and you can make it misbehave on
purpose to show what the application does when things go wrong.

```
 ┌──────────────────┐   GET /feed/latest            ┌─────────────────┐
 │  fakesupplier    │ ◄──────────────────────────── │  catalog (app)  │
 │  :9090           │   GET /feed/batches?after=N   │  :8080          │
 │                  │ ────────────────────────────► │                 │
 └──────────────────┘   batches (JSON)              └─────────────────┘
        ▲
        │  you: start flags, and curl /admin/... while it runs
```

All commands below are run **from the project folder** (the supplier reads
`testdata/feeds/acme-story.json` relative to it).

---

## 1. Start it and look around

```sh
make supplier                      # = go run ./cmd/fakesupplier -delay 200ms
```

It prints:

```
level=INFO msg="fictional supplier listening" addr=:9090 batches=8 visible=8
```

In another terminal, ask it what the application would ask:

```sh
curl localhost:9090/feed/latest                       # {"latest":8}: newest batch
curl 'localhost:9090/feed/batches?after=0&limit=1'    # batch #1 and its changes
curl localhost:9090/admin/state                       # its current settings
```

`/admin/state` answers, for example:

```json
{"published": 8, "total": 8, "latest": 8, "down": false, "fail_from": 0}
```

| Field | Meaning |
|---|---|
| `total` | batches in the feed |
| `published` | batches visible to the application right now (Window 1 can only show these) |
| `latest` | number of the newest visible batch |
| `down` | full outage switched on |
| `fail_from` | outage from this batch on (0 = off) |

## 2. The two kinds of controls

**Start-up flags** decide how it behaves from the beginning (restart it to change them):

| Flag | Default | What it does |
|---|---|---|
| `-addr :9090` | `:9090` | where it listens |
| `-feed FILE` | the story | which feed file to serve |
| `-generate` | off | serve a generated, messy feed instead of the file |
| `-seed N` | `42` | which generated feed (same seed = same feed); also drives `-fail-rate` |
| `-products N` / `-batches N` | `60` / `40` | size of a generated feed |
| `-publish N` | all | only the first N batches are visible at start |
| `-grow N` | `0` | after every batch request, N more batches become visible |
| `-delay D` | `0` | answer every request D later (e.g. `200ms`) |
| `-fail-rate P` | `0` | answer `503` to a random share P of requests (e.g. `0.3`) |
| `-dump` | | print the feed as JSON and exit |

**Admin commands** change it **while it runs**, no restart needed:

```sh
curl -X POST 'localhost:9090/admin/publish?count=2'   # 2 more batches become visible
curl -X POST 'localhost:9090/admin/publish'           # all batches become visible
curl -X POST 'localhost:9090/admin/outage?from=5'     # batches 1–4 still served, then 503
curl -X POST 'localhost:9090/admin/outage?on=true'    # everything answers 503
curl -X POST 'localhost:9090/admin/outage?on=false'   # back to normal
curl localhost:9090/admin/state                       # check the settings
```

> **Windows (PowerShell):** type `curl.exe` instead of `curl`, and use double quotes:
> `curl.exe -X POST "localhost:9090/admin/outage?from=5"`.

---

## 3. Recipes

For each recipe, run the application with `make run-demo` (slow, so you can watch the two
windows) and open <http://localhost:8080>.

**Starting clean.** The application remembers its bookmark. To see batches move again, either
press **Re-import everything**, or wipe the database with `make db-reset` (restart the app
afterwards). Always do one of these after switching to a **different feed** (e.g. from the story
to `-generate`), because the old bookmark belongs to the old feed.

### A. A normal update
```sh
make supplier
```
Press **Start update**. Batches #1–#8 move from Window 1 to Window 2 and leave one by one; the
bookmark ends at #8.

### B. Supplier goes down in the middle → Resume (R4, R5, R6)
```sh
curl -X POST 'localhost:9090/admin/outage?from=5'
```
Press **Start update**. #1–#4 are saved, then **#5 turns red ("stopped")**. Status: *Failed*, the
bookmark stays at #4, and *Needs attention* lists "Supplier unavailable".
```sh
curl -X POST 'localhost:9090/admin/outage?on=false'
```
Press **Resume**: it continues from #5 and completes. The supplier problem shows as resolved.

### C. Supplier down from the very start
```sh
curl -X POST 'localhost:9090/admin/outage?on=true'
```
Press **Re-import everything** (or Start). The update fails at once, nothing is saved, and the
catalog is untouched. The window panel says the supplier can't be reached and why. Turn the
outage off and press **Resume**.

### D. New batches arrive during an update (Window 1 grows, Window 2 refills; D5)
Restart the supplier with only part of the feed visible:
```sh
go run ./cmd/fakesupplier -delay 200ms -publish 3
```
Start from an empty database (`make db-reset`), press **Start update**, and while #1–#3 are being
saved:
```sh
curl -X POST 'localhost:9090/admin/publish?count=3'
```
#4–#6 appear in Window 1. When Window 2 is empty they move in ("refill 1 of 3"). Publish the
rest the same way to use another refill.

### E. A supplier that never stops changing (stop rule C)
```sh
go run ./cmd/fakesupplier -generate -seed 7 -publish 10 -grow 2 -delay 100ms
```
Press **Re-import everything**. Every request makes 2 more batches appear, so Window 1 never
empties. After 3 refills the update **completes anyway**; the remaining batches wait in Window 1
for the next update.

### F. A flaky supplier: retries hide small hiccups
```sh
go run ./cmd/fakesupplier -delay 100ms -fail-rate 0.3
```
With the normal app (`make run-demo`, 3 tries per request) updates usually complete: a failed
request is retried. To see every hiccup become a *Failed* update, run the app with one try:
```sh
go run ./cmd/catalog -batch-delay 900ms -page-size 3 -supplier-attempts 1
```
Keep pressing **Resume**; each time it continues from the bookmark, and the final catalog is
the same.

### G. The application crashes (R5, R6)
With any supplier, press **Start update** in `make run-demo` and, while a batch is highlighted,
press **Ctrl+C** in the app's terminal (or kill it). Start the app again: the update shows as
*Interrupted* with the batch where it stopped marked in amber. Press **Resume**.

### H. A big, messy, realistic feed
```sh
make supplier-big                  # = -generate -seed 42 -delay 150ms
```
Its first log line shows how messy it is:
```
stats={products:60 changes:262 delivered:297 late:43 repeated:30 replayed_batches:3 ...}
```
Press **Re-import everything**, then look at the change log (filter `stale`, `duplicate`) and
*Needs attention* (broken records). To read the feed itself:
```sh
go run ./cmd/fakesupplier -generate -seed 42 -dump > feed.json
```

---

## 4. Writing your own feed

A feed file is JSON: a list of batches, each with its changes.

```json
{
  "batches": [
    {"seq": 1, "changes": [
      {"sku": "MUG-BLUE", "version": 1, "op": "upsert", "name": "Blue Mug",
       "description": "Ceramic.", "price_cents": 1000, "currency": "EUR"}
    ]},
    {"seq": 2, "changes": [
      {"sku": "MUG-BLUE", "version": 2, "op": "withdraw"}
    ]}
  ]
}
```

| Field | Rules |
|---|---|
| `seq` | batch number, increasing (may be left out: then 1, 2, 3, … in file order) |
| `sku` | product code; empty = broken record |
| `version` | per product, bigger = newer |
| `op` | `upsert` (create or change) or `withdraw` |
| `name`, `price_cents`, `currency` | needed for `upsert`: name not empty, price above 0, currency like `EUR` |

Serve it with:
```sh
go run ./cmd/fakesupplier -feed path/to/my-feed.json -delay 200ms
```

`testdata/feeds/acme-story.json` is a good starting point; its
[README](../testdata/feeds/README.md) explains what each batch demonstrates.

---

## 5. Troubleshooting

| You see | Cause | Fix |
|---|---|---|
| `cannot load feed … no such file` | started outside the project folder | `cd` into the project folder |
| `address already in use` | port 9090 is taken (maybe an old supplier) | stop it, or `-addr :9091` plus app `-supplier-url http://localhost:9091` |
| Page: "Cannot reach the supplier … connection refused" | supplier not running | start it; the page picks it up by itself |
| Page: "… 503 … unavailable" | an outage is switched on | `curl -X POST 'localhost:9090/admin/outage?on=false'` |
| Window 1 empty, "Up to date" | everything visible is already imported | **Re-import everything**, `admin/publish`, or `make db-reset` |
| Only some batches in Window 1 | started with `-publish N` | `curl -X POST 'localhost:9090/admin/publish'` |
