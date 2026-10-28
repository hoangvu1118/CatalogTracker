# Fictional supplier feeds

## `acme-story.json`: the demo story

A small hand-written feed in which every rule from `docs/01-problem.md` happens at least once.

| Batch | What happens | Rule |
|---|---|---|
| 1 | Blue Mug, Red Lamp and Oak Chair are created | — |
| 2 | Blue Mug price €10 → €12; Glass Vase and Linen Cushion (GBP) are created | — |
| 3 | Blue Mug price €12 → €15; Red Lamp is withdrawn | — |
| 4 | Late, old messages: Blue Mug v2 and Red Lamp v1 are ignored as **stale**, so the lamp stays withdrawn. Glass Vase v2 is **invalid** (no name, negative price). | R2, R3, D8 |
| 5 | The supplier re-sends batch 3 (**duplicates**); Cotton Towel (USD) is created | R1 |
| 6 | Steel Kettle is withdrawn before the shop has ever seen it (a **tombstone**); Oak Chair jumps from v1 to v4 (a **version gap**) | D2, D3 |
| 7 | An old Steel Kettle v2 arrives and is **stale**; Red Lamp v5 is **restocked**; Glass Vase v3 is valid and **resolves** the v2 problem | R3, D8 |
| 8 | A record with **no SKU** and a Bamboo Tray with currency `eur` are invalid (they stay open problems); Linen Cushion price changes | D8 |

Final catalog: Blue Mug €15 (v3), Red Lamp €32 (v5, active), Oak Chair €95 (v4), Glass Vase
€27 (v3), Linen Cushion £19 (v2), Cotton Towel $9 (v1), Steel Kettle withdrawn (v3).
Two problems remain open (the record with no SKU, and Bamboo Tray v1).

## Generated feeds

`fakesupplier -generate -seed N` builds a larger, messier feed: about 60 products, around 40
batches of 0–12 changes, and three currencies. It includes price changes, renames, withdrawals,
restocks and version gaps, plus late, repeated, replayed and shuffled deliveries and broken
records. The same seed always gives the same feed. `-dump` prints it as JSON.
