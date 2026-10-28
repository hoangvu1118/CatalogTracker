package fake

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/supplier"
)

// Options controls how the fictional supplier behaves.
type Options struct {
	// Publish is how many batches are visible at start (0 = all).
	Publish int
	// Grow publishes this many more batches after every batch request,
	// like a supplier that keeps changing during an update.
	Grow int
	// Delay is added to every feed request (makes updates watchable).
	Delay time.Duration
	// FailRate is the chance (0..1) that a feed request answers 503.
	FailRate float64
	// Seed makes FailRate repeatable.
	Seed uint64
}

// Server is the fictional supplier's HTTP API.
//
//	GET  /feed/latest
//	GET  /feed/batches?after=N&limit=M
//	POST /admin/publish?count=N     make N more batches visible (no count: all)
//	POST /admin/outage?on=true      every feed request answers 503
//	POST /admin/outage?from=SEQ     requests reaching batch SEQ or later answer 503
//	POST /admin/outage?on=false     back to normal
//	GET  /admin/state
type Server struct {
	mu        sync.Mutex
	feed      Feed
	opts      Options
	published int
	down      bool
	failFrom  int64
	rng       *rand.Rand
	mux       *http.ServeMux
}

func NewServer(feed Feed, opts Options) *Server {
	s := &Server{
		feed:      feed,
		opts:      opts,
		published: len(feed.Batches),
		rng:       rand.New(rand.NewPCG(opts.Seed, opts.Seed+1)),
		mux:       http.NewServeMux(),
	}
	if opts.Publish > 0 && opts.Publish < len(feed.Batches) {
		s.published = opts.Publish
	}
	s.mux.HandleFunc("GET /feed/latest", s.latest)
	s.mux.HandleFunc("GET /feed/batches", s.batches)
	s.mux.HandleFunc("POST /admin/publish", s.publish)
	s.mux.HandleFunc("POST /admin/outage", s.outage)
	s.mux.HandleFunc("GET /admin/state", s.state)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Publish makes n more batches visible (n <= 0: all of them).
func (s *Server) Publish(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(n)
}

func (s *Server) publishLocked(n int) {
	if n <= 0 {
		s.published = len(s.feed.Batches)
		return
	}
	s.published = min(s.published+n, len(s.feed.Batches))
}

// SetOutage turns a full outage on or off.
func (s *Server) SetOutage(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down, s.failFrom = on, 0
}

// FailFrom makes requests that reach batch seq or later fail (0 = off).
func (s *Server) FailFrom(seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down, s.failFrom = false, seq
}

// Latest is the newest visible batch.
func (s *Server) Latest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestLocked()
}

func (s *Server) latestLocked() int64 {
	if s.published == 0 {
		return 0
	}
	return s.feed.Batches[s.published-1].Seq
}

// unavailable decides whether this request fails, and sleeps for Delay.
func (s *Server) unavailable() bool {
	if s.opts.Delay > 0 {
		time.Sleep(s.opts.Delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down || (s.opts.FailRate > 0 && s.rng.Float64() < s.opts.FailRate)
}

func (s *Server) latest(w http.ResponseWriter, _ *http.Request) {
	if s.unavailable() {
		http.Error(w, "supplier unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, supplier.LatestResponse{Latest: s.Latest()})
}

func (s *Server) batches(w http.ResponseWriter, r *http.Request) {
	after, err1 := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, err2 := strconv.Atoi(r.URL.Query().Get("limit"))
	if err1 != nil || err2 != nil || limit < 1 {
		http.Error(w, "after and limit are required", http.StatusBadRequest)
		return
	}
	if s.unavailable() {
		http.Error(w, "supplier unavailable", http.StatusServiceUnavailable)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := []supplier.WireBatch{}
	for _, b := range s.feed.Batches[:s.published] {
		if b.Seq <= after || len(out) == limit {
			continue
		}
		if s.failFrom > 0 && b.Seq >= s.failFrom {
			http.Error(w, "supplier unavailable", http.StatusServiceUnavailable)
			return
		}
		out = append(out, b)
	}
	if s.opts.Grow > 0 {
		s.publishLocked(s.opts.Grow)
	}
	writeJSON(w, supplier.BatchesResponse{Batches: out})
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("count"))
	s.Publish(n)
	s.state(w, r)
}

func (s *Server) outage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case q.Get("from") != "":
		seq, err := strconv.ParseInt(q.Get("from"), 10, 64)
		if err != nil {
			http.Error(w, "from must be a number", http.StatusBadRequest)
			return
		}
		s.FailFrom(seq)
	default:
		s.SetOutage(q.Get("on") == "true")
	}
	s.state(w, r)
}

func (s *Server) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"published": s.published,
		"total":     len(s.feed.Batches),
		"latest":    s.latestLocked(),
		"down":      s.down,
		"fail_from": s.failFrom,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
