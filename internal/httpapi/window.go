package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// LatestSource tells the newest batch the supplier has (Window 1's end).
// Implemented by the supplier client.
type LatestSource interface {
	Latest(ctx context.Context) (int64, error)
}

// Progress tells how much of Window 2 is downloaded into memory.
// Implemented by importer.Service.
type Progress interface {
	Fetched(updateID int64) int64
}

// WindowSource is what the window view needs besides the stored data.
type WindowSource struct {
	Feed       LatestSource
	Progress   Progress
	PageSize   int
	MaxRefills int
}

// windowJSON describes the two windows of docs/02-decisions.md (D5) right now.
//
//	Window 2 (being processed): batches Bookmark+1 … Target, of which
//	         Bookmark+1 … Fetched are downloaded (the current page) and
//	         the rest are still at the supplier
//	Window 1 (still at supplier): batches max(Target, Bookmark)+1 … SupplierLatest
type windowJSON struct {
	UpdateID   int64  `json:"update_id"` // 0 when there has never been an update
	Status     string `json:"status"`    // the latest update's status, or "idle"
	Bookmark   int64  `json:"bookmark"`
	Target     int64  `json:"target"`     // end of Window 2; 0 or <= bookmark when empty
	Processing int64  `json:"processing"` // batch being saved now; 0 if none
	Fetched    int64  `json:"fetched"`    // newest batch downloaded into memory; 0 if none
	PageSize   int    `json:"page_size"`
	Refills    int    `json:"refills"`
	MaxRefills int    `json:"max_refills"`
	// SupplierLatest is asked live from the supplier; SupplierError is set
	// if it could not be reached.
	SupplierLatest int64  `json:"supplier_latest"`
	SupplierError  string `json:"supplier_error,omitempty"`
}

func (a *API) window(w http.ResponseWriter, r *http.Request) {
	view := windowJSON{Status: "idle", PageSize: a.windowSrc.PageSize, MaxRefills: a.windowSrc.MaxRefills}

	updates, err := a.reader.ListUpdates(r.Context(), 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bookmark, err := a.reader.LastBookmark(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view.Bookmark = bookmark

	if len(updates) == 1 {
		u := updates[0]
		view.UpdateID, view.Status, view.Refills = u.ID, string(u.Status), u.Refills
		if u.Status != importer.StatusCompleted {
			// An unfinished update owns the bookmark and still holds Window 2.
			view.Bookmark, view.Target = u.Bookmark, u.Target
			if u.Status == importer.StatusRunning && u.Target > u.Bookmark {
				view.Fetched = a.windowSrc.Progress.Fetched(u.ID)
				if view.Fetched > u.Bookmark {
					// The next batch is in memory, so it is being saved.
					view.Processing = u.Bookmark + 1
				}
			}
		}
	}

	// Ask the supplier briefly; the page must not hang if it is down.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if latest, err := a.windowSrc.Feed.Latest(ctx); err != nil {
		view.SupplierError = err.Error()
	} else {
		view.SupplierLatest = latest
	}
	writeJSON(w, http.StatusOK, view)
}
