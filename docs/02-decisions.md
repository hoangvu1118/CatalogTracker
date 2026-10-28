# 2. Decisions: how we solve each problem

Each decision links back to the rules R1–R6 in [01-problem.md](01-problem.md).

---

## D1. The shop pulls, the supplier does not push

Staff start the update, so the **application asks** the supplier for changes. The supplier never
sends anything on its own; it keeps a growing list of changes (Window 1) and answers our requests.

A request looks like: *"give me the batches after my bookmark"*. The supplier answers with
batches and tells us whether more are waiting.

---

## D2. Every product change carries a version number (R1, R2, R3)

The supplier counts the changes **per product**: 1, 2, 3, …

```
Blue Mug  v1 → created, €10
Blue Mug  v2 → price €12
Blue Mug  v3 → price €15
Blue Mug  v4 → withdrawn
```

We store the version **together with the product** in our database.

**Why not a timestamp?** Two changes can share the same timestamp, and supplier clocks can be
wrong. **Why not a message id?** An id says *which* message it is, not *which is newer*.

### The comparison rule

When a change arrives, compare its version with the version stored for that product:

| Database has | Change says | Result | Reason |
|---|---|---|---|
| nothing | Blue Mug v1 | **create** | new product |
| v2 | v3 | **update** | newer |
| v3 | v3 | **ignore (duplicate)** | the same change sent twice — R1 |
| v3 | v2 | **ignore (stale)** | old change arrived late — R2 |
| v5 active | v7 withdrawn | **withdraw** | newer; gaps (no v6) are fine |
| v7 withdrawn | v5 price €30 | **ignore (stale)** | old change must not revive it — R3 |
| v7 withdrawn | v8 back in stock | **active again** | newer change may restock it |

We never wait for missing versions. We only compare **bigger vs. smaller**.

---

## D3. Withdrawn products are kept, never deleted (R3, R4)

A withdrawn product stays in the database with status `withdrawn` and its version
(a "tombstone").

Two reasons:

1. **It remembers the latest version.** If we deleted it, a late, older change would find "nothing"
   and re-create it, breaking R3.
2. **It can come back.** A later, higher version can make it active and sellable again.

More generally, **the application never deletes products.** A product leaves sale only through an
explicit withdrawal from the supplier. A failed or empty fetch removes nothing (R4).

---

## D4. Each batch is saved all or nothing, together with the bookmark (R4, R5)

For one batch, these are saved **in a single database transaction**:

- the product changes,
- the "what changed" log entries,
- the problems found in that batch,
- the **bookmark** moving forward to this batch.

Either everything is saved, or nothing is. If the application crashes while saving product 6 of 10
in batch 3, **none** of batch 3 is kept, and the bookmark still points to batch 2.

### Two layers of protection

1. **All-or-nothing saving**: products and bookmark always agree.
2. **The version rule (D2)**: even if a batch is processed again, every change in it is ignored as
   a duplicate. Repeating work is harmless.

The database adds two more checks underneath: the product write itself refuses to replace a
newer version, and the bookmark can only move forward.

---

## D5. Two windows, with a refill limit (stop rule C)

```
Window 1 = supplier's pending changes     Window 2 = batches we are processing now
┌─────────────────────────┐               ┌──────────────────┐
│ b1 b2 b3 b4 b5          │ ── take ───▶  │ b1 b2 b3 b4 b5   │ ── process one by one
└─────────────────────────┘               └──────────────────┘
          ▲                                        │
          │  Window 2 empty → look at Window 1 again ◀┘
          │
   new changes arrived meanwhile (b6, b7)? → take them, repeat
   nothing new?                            → FINISH
```

1. Fill **Window 2** with the batches waiting in **Window 1**.
2. Process the batches in Window 2 one by one (each saved per D4).
3. When Window 2 is empty, check Window 1 again.
   - Nothing new → the update is **Completed**.
   - New changes → refill Window 2 and continue.
4. **Stop rule C:** refill Window 2 at most **N times** (default `N = 3`, configurable). After the
   last allowed round, the update is **Completed**, and anything still waiting is picked up by
   the next update.

Why: this catches up with changes that arrive *during* the update, and still **guarantees the
update ends**, even with a supplier that keeps changing.

Details:

- The **bookmark moves per batch, not per window.** A crash during b4 resumes at b4, not b1.
- Window 2 is read from the supplier in **pages** (5 batches per request by default), so a long
  window never has to be held in memory at once.
- **Window 2 is not saved.** On resume we ask the supplier again for batches after the bookmark,
  and Window 2 rebuilds itself.

---

## D6. Update status: honest reporting (R5, R6)

| Status | Meaning | Can resume? |
|---|---|---|
| **Running** | currently processing batches | – |
| **Completed** | Window 1 was empty, or the refill limit was reached, and every taken batch is saved | – |
| **Failed** | the supplier was unavailable or returned an error (after retries) | yes |
| **Interrupted** | the application stopped while the update was running | yes |

- **Completed** is set **only** at the very end, after the last batch is saved. Never earlier.
- **Interrupted** is detected on start-up: any update still marked `Running` while nothing is
  actually running must have been cut off by a crash or shutdown.
- **Only one unfinished update at a time.** Pressing Update while an update is running, failed or
  interrupted is refused, and staff are pointed to the update to resume. The database enforces
  this too (a unique index), so even two copies of the application cannot both start one.
- **Resume** continues the same update from its bookmark.
- **Re-import everything** starts an update from bookmark 0. It is safe because of D2: every
  change already stored comes back as `duplicate` or `stale`.

### What staff see

> **Interrupted** · 2 batches done · next: batch 3 · [Resume]

We may not know the total number of batches (the supplier only says "more" or "that's all").
The fictional supplier may report a total; if it does, we show "2 of 5".

---

## D7. What changed: the change log

Every change processed in an update is recorded with its outcome:

| Kind | Meaning |
|---|---|
| `created` | new product added |
| `updated` | details changed (e.g. price) |
| `withdrawn` | product withdrawn from sale |
| `restocked` | withdrawn product made active again by a newer version |
| `duplicate` | same version already stored, ignored |
| `stale` | older than the stored version, ignored |

Staff see counts per kind, and the full list per update.

---

## D8. Problems that need attention

Problems are recorded and shown per update. A bad record **does not stop** the update; it is
skipped and reported.

| Problem | Example | Effect |
|---|---|---|
| Invalid record | missing name, missing SKU, price not positive, bad currency, unknown operation | record skipped, update continues |
| Supplier unavailable | timeout, server error after retries | update becomes **Failed** (resumable) |

Problems resolve themselves when they no longer need attention:

- An invalid record is resolved when a **newer valid version** of the same product is applied.
- A supplier-unavailable problem is resolved when its update **completes** after a resume.
- An invalid record that is **not newer** than the stored version is not reported at all: it would
  be ignored anyway.
- The same broken record delivered twice raises **one** problem, not two.

---

## Assumptions

- The supplier gives each product a **version that only grows**.
- The supplier keeps its changes available, so we can ask again from our bookmark.
- The application talks to one supplier feed; the bookmark belongs to that feed.
- The feed contains **changes**, not a full snapshot: a product missing from a batch is **not**
  withdrawn; withdrawal is always explicit.
- One shop, one running copy of the application, no login.
- Prices are stored as whole cents to avoid rounding errors.
- An update's refill limit defaults to 3 rounds.
