// Package storage saves products, updates, changes and problems in PostgreSQL.
// Each batch is saved in a single transaction together with the bookmark
// (docs/02-decisions.md, D4).
package storage
