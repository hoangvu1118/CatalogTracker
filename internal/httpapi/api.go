package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/catalog"
	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// Updater starts and resumes updates (implemented by importer.Service).
type Updater interface {
	Start(ctx context.Context, fromBeginning bool) (importer.Update, error)
	Resume(ctx context.Context, id int64) (importer.Update, error)
}

// Reader answers the staff's questions (implemented by storage.Store).
type Reader interface {
	GetUpdate(ctx context.Context, id int64) (importer.Update, error)
	ListUpdates(ctx context.Context, limit int) ([]importer.Update, error)
	UnfinishedUpdate(ctx context.Context) (importer.Update, bool, error)
	ChangeCounts(ctx context.Context, updateID int64) (map[catalog.Outcome]int, error)
	ListChanges(ctx context.Context, updateID int64, outcome catalog.Outcome, limit int) ([]importer.ChangeRecord, error)
	ListProblems(ctx context.Context, updateID int64, openOnly bool) ([]importer.Problem, error)
	ListProducts(ctx context.Context, status catalog.Status) ([]catalog.Product, error)
	LastBookmark(ctx context.Context) (int64, error)
}

// API holds the JSON endpoints.
type API struct {
	updater   Updater
	reader    Reader
	windowSrc WindowSource
	latest    *latestCache
}

// New builds the API. window is only used to describe the two windows
// (GET /api/window).
func New(updater Updater, reader Reader, window WindowSource) *API {
	return &API{updater: updater, reader: reader, windowSrc: window,
		latest: newLatestCache(window.Feed, 500*time.Millisecond)}
}

// Register adds the routes to mux.
//
//	POST /api/updates                  start ({"from_beginning": true} re-reads everything)
//	POST /api/updates/{id}/resume      resume a failed or interrupted update
//	GET  /api/updates                  recent updates with their counts
//	GET  /api/updates/{id}             one update: status, progress, counts
//	GET  /api/updates/{id}/changes     what changed (?outcome=updated&limit=100)
//	GET  /api/updates/{id}/problems    problems found by this update
//	GET  /api/problems                 all problems (?open=true: only those needing attention)
//	GET  /api/products                 the catalog (?status=active|withdrawn)
//	GET  /api/window                   the two windows right now (D5)
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/updates", a.startUpdate)
	mux.HandleFunc("POST /api/updates/{id}/resume", a.resumeUpdate)
	mux.HandleFunc("GET /api/updates", a.listUpdates)
	mux.HandleFunc("GET /api/updates/{id}", a.getUpdate)
	mux.HandleFunc("GET /api/updates/{id}/changes", a.listChanges)
	mux.HandleFunc("GET /api/updates/{id}/problems", a.listUpdateProblems)
	mux.HandleFunc("GET /api/problems", a.listProblems)
	mux.HandleFunc("GET /api/products", a.listProducts)
	mux.HandleFunc("GET /api/window", a.window)
}

func (a *API) startUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromBeginning bool `json:"from_beginning"`
	}
	// The body is optional: an empty body starts a normal update.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "body must be JSON like {\"from_beginning\": false}")
		return
	}

	u, err := a.updater.Start(r.Context(), body.FromBeginning)
	if errors.Is(err, importer.ErrUnfinishedUpdate) {
		resp := map[string]any{"error": err.Error()}
		if unfinished, ok, _ := a.reader.UnfinishedUpdate(r.Context()); ok {
			resp["unfinished_update_id"] = unfinished.ID
		}
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeUpdate(w, r, http.StatusAccepted, u)
}

func (a *API) resumeUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := a.updater.Resume(r.Context(), id)
	switch {
	case errors.Is(err, importer.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, importer.ErrNotResumable), errors.Is(err, importer.ErrUnfinishedUpdate):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		a.writeUpdate(w, r, http.StatusAccepted, u)
	}
}

func (a *API) listUpdates(w http.ResponseWriter, r *http.Request) {
	updates, err := a.reader.ListUpdates(r.Context(), queryInt(r, "limit", 20, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]updateJSON, 0, len(updates))
	for _, u := range updates {
		view, err := a.updateView(r.Context(), u)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := a.reader.GetUpdate(r.Context(), id)
	if errors.Is(err, importer.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeUpdate(w, r, http.StatusOK, u)
}

func (a *API) listChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	outcome := catalog.Outcome(r.URL.Query().Get("outcome"))
	changes, err := a.reader.ListChanges(r.Context(), id, outcome, queryInt(r, "limit", 200, 5000))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]changeJSON, len(changes))
	for i, c := range changes {
		out[i] = toChangeJSON(c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listUpdateProblems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a.writeProblems(w, r, id, r.URL.Query().Get("open") == "true")
}

func (a *API) listProblems(w http.ResponseWriter, r *http.Request) {
	a.writeProblems(w, r, 0, r.URL.Query().Get("open") == "true")
}

func (a *API) writeProblems(w http.ResponseWriter, r *http.Request, updateID int64, openOnly bool) {
	problems, err := a.reader.ListProblems(r.Context(), updateID, openOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]problemJSON, len(problems))
	for i, p := range problems {
		out[i] = toProblemJSON(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	status := catalog.Status(r.URL.Query().Get("status"))
	if status != "" && status != catalog.StatusActive && status != catalog.StatusWithdrawn {
		writeError(w, http.StatusBadRequest, "status must be active or withdrawn")
		return
	}
	products, err := a.reader.ListProducts(r.Context(), status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]productJSON, len(products))
	for i, p := range products {
		out[i] = toProductJSON(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) writeUpdate(w http.ResponseWriter, r *http.Request, status int, u importer.Update) {
	view, err := a.updateView(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, view)
}

func (a *API) updateView(ctx context.Context, u importer.Update) (updateJSON, error) {
	counts, err := a.reader.ChangeCounts(ctx, u.ID)
	if err != nil {
		return updateJSON{}, err
	}
	open, err := a.reader.ListProblems(ctx, u.ID, true)
	if err != nil {
		return updateJSON{}, err
	}
	return toUpdateJSON(u, counts, len(open)), nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive number")
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, key string, fallback, maximum int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 1 {
		return fallback
	}
	return min(n, maximum)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
