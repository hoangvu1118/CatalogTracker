// Package importer runs a catalog update: it takes batches from the supplier
// using two windows, saves each batch with the bookmark, and tracks the update
// status (docs/02-decisions.md, D4 to D6).
//
// It depends only on interfaces, never on PostgreSQL or HTTP directly.
package importer
