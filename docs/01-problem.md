# 1. The problem in plain words

## The story

A **shop** sells products that come from a **supplier**. The supplier owns the master list of
products (the **catalog**). The shop keeps **its own copy** of that list in its own database.

The supplier's catalog keeps changing: new products appear, details such as price change, and
some products are withdrawn from sale. A **staff member presses "Update"** and the application
brings the shop's copy up to date with the supplier.

We are building that **Update** feature.

## What staff need to see

After starting an update, staff want to know:

1. **Did it finish?** Completed, still running, failed, or interrupted.
2. **What changed?** Products created, updated, withdrawn, or ignored.
3. **Which problems need attention?** For example, a record with no price, or the supplier
   being unavailable.

## Words we use

| Word | Meaning |
|---|---|
| **Update** | One press of "Update" by staff. The whole job. |
| **Batch** | One piece of that job. The supplier delivers changes in batches. |
| **Change** | One message inside a batch about one product: create/change it, or withdraw it. |
| **Version** | A number per product that grows each time the supplier changes that product. |
| **Bookmark** | The last batch we have fully saved. Lets us resume. |
| **Window 1** | The supplier's pending changes we have not read yet. |
| **Window 2** | The batches our application has taken and is processing right now. |

"Did it finish?" is about the **update**. "Don't lose completed work" is counted in **batches**.

## How the supplier behaves (and why it is hard)

- Changes arrive in **batches**, not all at once.
- The same change can be **sent twice**.
- Changes can arrive **in the wrong order**: an old message may arrive after a newer one.
- The supplier can be **unavailable** in the middle of an update.
- Our own application can **crash or be closed** in the middle of an update.

## Rules we must never break

| # | Rule | Example |
|---|---|---|
| R1 | Importing the same update twice must not create duplicates. | Staff press Update twice: "Blue Mug" exists once. |
| R2 | Older information must not replace newer information. | Price €15 (newer) arrives before €12 (older): final price is €15. |
| R3 | Older information must not bring back a withdrawn product. | "Red Lamp withdrawn" then a late, older "Red Lamp €30": stays withdrawn. |
| R4 | A supplier outage or app crash must not erase the catalog. | Supplier goes down at batch 2: all existing products are still there. |
| R5 | An incomplete update can be resumed after restart without losing completed work. | Crash during batch 3 of 5: Resume continues from batch 3, batches 1–2 are kept. |
| R6 | A partially completed update must never be reported as complete. | Stopped after batch 2: status is "Failed" or "Interrupted", never "Completed". |

## Out of scope

- No real supplier account: we use a **fictional supplier** with invented data, which we can
  make misbehave on purpose (duplicates, wrong order, outages).
- No public deployment. A simple web page is enough.
- Written in **Go**, with a README, tests, and a demo video.

## In one sentence

> Build a "sync from supplier" feature that is safe to run twice, ignores out-of-date messages,
> survives crashes without losing data, can continue where it stopped, and tells staff honestly
> what happened.
