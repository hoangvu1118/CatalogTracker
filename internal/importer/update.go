package importer

import "time"

// Status is where an update is in its life (docs/02-decisions.md, D6).
type Status string

const (
	StatusRunning     Status = "running"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"      // supplier unavailable; resumable
	StatusInterrupted Status = "interrupted" // application stopped; resumable
)

// CanResume reports whether staff may resume an update in this status.
func (s Status) CanResume() bool {
	return s == StatusFailed || s == StatusInterrupted
}

// CanBecome reports whether moving from s to next is allowed.
//
//	running → completed | failed | interrupted
//	failed | interrupted → running (resume)
//
// Completed is final: a partial update can never become completed without
// running again, and a completed update never changes.
func (s Status) CanBecome(next Status) bool {
	switch s {
	case StatusRunning:
		return next == StatusCompleted || next == StatusFailed || next == StatusInterrupted
	case StatusFailed, StatusInterrupted:
		return next == StatusRunning
	}
	return false
}

// Update is one press of "Update" by staff: the whole job.
type Update struct {
	ID     int64
	Status Status

	// StartBookmark is the bookmark the update started from (0 when staff
	// asked to import everything again from the beginning).
	StartBookmark int64
	// Bookmark is the sequence number of the last batch fully saved.
	// It only moves forward, together with the batch (D4).
	Bookmark int64
	// Target is the end of the current Window 2: the newest batch the
	// supplier had when the window was filled (D5).
	Target int64
	// Refills counts how many times Window 2 was refilled after the first fill.
	Refills int
	// BatchesDone counts the batches saved by this update.
	BatchesDone int

	Error      string
	StartedAt  time.Time
	FinishedAt *time.Time
}
