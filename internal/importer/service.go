package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Config tunes how an update reads the supplier feed.
type Config struct {
	// PageSize is how many batches are requested from the supplier at once.
	PageSize int
	// MaxRefills is how many times Window 2 may be refilled after the first
	// fill before the update completes (stop rule C, D5).
	MaxRefills int
	// BatchDelay pauses before each batch is saved. Only for demonstrations,
	// so people can watch batches leave Window 2; 0 in normal use.
	BatchDelay time.Duration
}

// DefaultConfig matches docs/02-decisions.md.
func DefaultConfig() Config {
	return Config{PageSize: 5, MaxRefills: 3}
}

// Service starts, resumes and runs updates. Each update runs in its own
// goroutine; only one update can be unfinished at a time.
type Service struct {
	feed  Feed
	store Store
	cfg   Config
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex // serialises Start and Resume
	runCtx  context.Context
	stopAll context.CancelFunc
	wg      sync.WaitGroup
}

func NewService(feed Feed, store Store, cfg Config, log *slog.Logger) *Service {
	if cfg.PageSize < 1 {
		cfg.PageSize = DefaultConfig().PageSize
	}
	if cfg.MaxRefills < 0 {
		cfg.MaxRefills = 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		feed:    feed,
		store:   store,
		cfg:     cfg,
		log:     log,
		now:     time.Now,
		runCtx:  ctx,
		stopAll: cancel,
	}
}

// Recover marks updates left "running" by a crash as interrupted (D6).
// Call it once at start-up, before serving requests.
func (s *Service) Recover(ctx context.Context) ([]int64, error) {
	ids, err := s.store.MarkRunningAsInterrupted(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover interrupted updates: %w", err)
	}
	for _, id := range ids {
		s.log.Warn("update was interrupted by a restart", "update", id)
	}
	return ids, nil
}

// Start creates a new update and runs it in the background. Normally it
// continues from the furthest bookmark; fromBeginning re-reads the whole
// feed, which is safe because every change is checked by version.
func (s *Service) Start(ctx context.Context, fromBeginning bool) (Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var bookmark int64
	if !fromBeginning {
		b, err := s.store.LastBookmark(ctx)
		if err != nil {
			return Update{}, err
		}
		bookmark = b
	}

	u, err := s.store.CreateUpdate(ctx, Update{
		Status:        StatusRunning,
		StartBookmark: bookmark,
		Bookmark:      bookmark,
		StartedAt:     s.now(),
	})
	if err != nil {
		return Update{}, err
	}
	s.launch(u.ID)
	return u, nil
}

// Resume continues a failed or interrupted update from its bookmark.
func (s *Service) Resume(ctx context.Context, id int64) (Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, err := s.store.GetUpdate(ctx, id)
	if err != nil {
		return Update{}, err
	}
	if !u.Status.CanResume() {
		return Update{}, fmt.Errorf("%w: status is %s", ErrNotResumable, u.Status)
	}
	u.Status = StatusRunning
	u.Error = ""
	if err := s.store.SaveState(ctx, u); err != nil {
		return Update{}, err
	}
	s.launch(u.ID)
	return u, nil
}

// Shutdown stops running updates and waits for them. Their status becomes
// interrupted, so they can be resumed after the next start.
func (s *Service) Shutdown() {
	s.stopAll()
	s.wg.Wait()
}

// Wait blocks until all background updates have stopped.
func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) launch(id int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.run(s.runCtx, id); err != nil {
			s.log.Error("update stopped", "update", id, "error", err)
		}
	}()
}

// run is the two-window loop (D5):
//
//  1. Fill Window 2: note the newest batch the supplier has (the target).
//  2. Save batches one by one up to the target, moving the bookmark.
//  3. Window 2 empty: look at Window 1 again. Nothing new, or the refill
//     limit reached: completed. Otherwise refill and repeat.
//
// Any error leaves the update failed or interrupted with its bookmark at the
// last saved batch, so Resume continues from there.
func (s *Service) run(ctx context.Context, id int64) error {
	u, err := s.store.GetUpdate(ctx, id)
	if err != nil {
		return err
	}
	s.log.Info("update running", "update", id, "bookmark", u.Bookmark)

	// A resumed update has filled its window before; refills keep counting.
	filled := u.Target > 0
	for {
		if u.Target <= u.Bookmark {
			done, err := s.fillWindow(ctx, &u, !filled)
			if err != nil {
				return s.stop(ctx, &u, err)
			}
			if done {
				return s.complete(ctx, &u)
			}
			filled = true
		}
		if err := s.processWindow(ctx, &u); err != nil {
			return s.stop(ctx, &u, err)
		}
	}
}

// fillWindow sets the next target. It reports done when there is nothing
// new in Window 1, or when the refill limit is reached.
func (s *Service) fillWindow(ctx context.Context, u *Update, firstFill bool) (done bool, err error) {
	latest, err := s.feed.Latest(ctx)
	if err != nil {
		return false, &supplierError{err}
	}
	if latest <= u.Bookmark {
		return true, nil
	}

	if !firstFill {
		if u.Refills >= s.cfg.MaxRefills {
			s.log.Info("refill limit reached; newer batches wait for the next update",
				"update", u.ID, "waiting_after", u.Bookmark, "latest", latest)
			return true, nil
		}
		u.Refills++
	}
	u.Target = latest
	s.log.Info("window filled", "update", u.ID, "from", u.Bookmark, "to", latest, "refills", u.Refills)
	return false, s.store.SaveState(ctx, *u)
}

// processWindow saves every batch after the bookmark up to the target.
func (s *Service) processWindow(ctx context.Context, u *Update) error {
	for u.Bookmark < u.Target {
		batches, err := s.feed.Batches(ctx, u.Bookmark, s.cfg.PageSize)
		if err != nil {
			return &supplierError{err}
		}

		inWindow := 0
		for _, b := range batches {
			if b.Seq <= u.Bookmark || b.Seq > u.Target {
				continue
			}
			inWindow++
			// Demo pacing: the batch about to be saved is shown as "saving".
			if err := pause(ctx, s.cfg.BatchDelay); err != nil {
				return err
			}
			if err := s.applyBatch(ctx, u.ID, b); err != nil {
				if !errors.Is(err, ErrBatchAlreadyApplied) {
					return err
				}
				// Already saved (and nothing was written twice): catch up
				// with the stored bookmark.
				saved, err := s.store.GetUpdate(ctx, u.ID)
				if err != nil {
					return err
				}
				u.Bookmark = saved.Bookmark
				u.BatchesDone = saved.BatchesDone
				continue
			}
			u.Bookmark = b.Seq
			u.BatchesDone++
		}
		if inWindow == 0 {
			// The supplier has no batch up to the target (sequence gap):
			// the window is done.
			u.Target = u.Bookmark
		}
	}
	return nil
}

func (s *Service) complete(ctx context.Context, u *Update) error {
	if err := s.store.ResolveProblems(ctx, u.ID, ProblemSupplierUnavailable); err != nil {
		return err
	}
	finished := s.now()
	u.Status = StatusCompleted
	u.FinishedAt = &finished
	u.Error = ""
	s.log.Info("update completed", "update", u.ID, "batches", u.BatchesDone, "bookmark", u.Bookmark)
	return s.store.SaveState(ctx, *u)
}

// stop records why the update stopped. A cancelled context means the
// application is shutting down (interrupted); anything else is a failure.
func (s *Service) stop(ctx context.Context, u *Update, cause error) error {
	// Use a fresh context: ctx may be the one that was cancelled.
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	u.Status = StatusFailed
	if ctx.Err() != nil {
		u.Status = StatusInterrupted
	}
	u.Error = cause.Error()

	var supErr *supplierError
	if u.Status == StatusFailed && errors.As(cause, &supErr) {
		problem := Problem{
			UpdateID:  u.ID,
			BatchSeq:  u.Bookmark + 1,
			Kind:      ProblemSupplierUnavailable,
			Message:   supErr.Error(),
			CreatedAt: s.now(),
		}
		if err := s.store.AddProblem(saveCtx, problem); err != nil {
			s.log.Error("could not record problem", "update", u.ID, "error", err)
		}
	}

	if err := s.store.SaveState(saveCtx, *u); err != nil {
		return fmt.Errorf("%v (and saving status failed: %w)", cause, err)
	}
	return cause
}

// pause waits for d, or returns early if ctx is cancelled.
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// supplierError marks errors that came from the supplier, so they can be
// reported as "supplier unavailable" problems.
type supplierError struct{ err error }

func (e *supplierError) Error() string { return "supplier unavailable: " + e.err.Error() }
func (e *supplierError) Unwrap() error { return e.err }
