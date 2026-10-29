package httpapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// slowSource answers after delay with the current value.
type slowSource struct {
	mu    sync.Mutex
	value int64
	err   error
	delay time.Duration
	calls int
}

func (s *slowSource) Latest(ctx context.Context) (int64, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.value, s.err
}

func (s *slowSource) set(v int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value, s.err = v, err
}

func TestLatestCacheNeverWaitsOnceItHasAValue(t *testing.T) {
	src := &slowSource{value: 8, delay: 200 * time.Millisecond}
	c := newLatestCache(src, 50*time.Millisecond)

	if v, err := c.Get(); v != 8 || err != nil { // first call waits
		t.Fatalf("first Get = %d, %v", v, err)
	}

	src.set(12, nil)
	time.Sleep(60 * time.Millisecond) // value is now stale

	begin := time.Now()
	v, _ := c.Get()
	if took := time.Since(begin); took > 20*time.Millisecond {
		t.Errorf("stale Get waited %v for the supplier; it must answer at once", took)
	}
	if v != 8 {
		t.Errorf("while refreshing, Get = %d, want the last known 8", v)
	}

	time.Sleep(250 * time.Millisecond) // background refresh done
	if v, _ := c.Get(); v != 12 {
		t.Errorf("after refresh, Get = %d, want 12", v)
	}
}

func TestLatestCacheRefreshesOnlyOnceAtATime(t *testing.T) {
	src := &slowSource{value: 3, delay: 100 * time.Millisecond}
	c := newLatestCache(src, time.Millisecond)
	c.Get()
	time.Sleep(5 * time.Millisecond)

	for i := 0; i < 20; i++ {
		c.Get()
	}
	time.Sleep(150 * time.Millisecond)

	src.mu.Lock()
	defer src.mu.Unlock()
	if src.calls != 2 {
		t.Errorf("supplier called %d times, want 2 (first fill + one refresh)", src.calls)
	}
}

func TestLatestCacheReportsErrorsAndRecovers(t *testing.T) {
	src := &slowSource{err: errors.New("connection refused")}
	c := newLatestCache(src, time.Millisecond)

	if _, err := c.Get(); err == nil {
		t.Fatal("expected the supplier error")
	}

	src.set(8, nil)
	time.Sleep(5 * time.Millisecond)
	c.Get() // starts the refresh
	time.Sleep(20 * time.Millisecond)
	if v, err := c.Get(); v != 8 || err != nil {
		t.Errorf("after the supplier is back: %d, %v", v, err)
	}
}
