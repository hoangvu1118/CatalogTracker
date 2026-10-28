package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/importer"
)

// ClientConfig configures the supplier HTTP client.
type ClientConfig struct {
	BaseURL string
	// Timeout limits one HTTP request.
	Timeout time.Duration
	// Attempts is how many times a request is tried before giving up.
	Attempts int
	// Backoff is the wait before the first retry; it doubles each time.
	Backoff time.Duration
}

// Client reads the supplier feed over HTTP. It implements importer.Feed.
type Client struct {
	cfg  ClientConfig
	http *http.Client
}

var _ importer.Feed = (*Client)(nil)

func NewClient(cfg ClientConfig) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Attempts < 1 {
		cfg.Attempts = 3
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 200 * time.Millisecond
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

func (c *Client) Latest(ctx context.Context) (int64, error) {
	var resp LatestResponse
	if err := c.get(ctx, "/feed/latest", nil, &resp); err != nil {
		return 0, err
	}
	return resp.Latest, nil
}

func (c *Client) Batches(ctx context.Context, after int64, limit int) ([]importer.Batch, error) {
	query := url.Values{
		"after": {strconv.FormatInt(after, 10)},
		"limit": {strconv.Itoa(limit)},
	}
	var resp BatchesResponse
	if err := c.get(ctx, "/feed/batches", query, &resp); err != nil {
		return nil, err
	}
	batches := make([]importer.Batch, len(resp.Batches))
	for i, b := range resp.Batches {
		batches[i] = b.ToBatch()
	}
	return batches, nil
}

// get calls the supplier, retrying temporary failures (network errors,
// 5xx, 429) with exponential backoff.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	wait := c.cfg.Backoff
	var lastErr error

	for attempt := 1; attempt <= c.cfg.Attempts; attempt++ {
		err := c.getOnce(ctx, path, query, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTemporary(err) || ctx.Err() != nil || attempt == c.cfg.Attempts {
			break
		}
		select {
		case <-time.After(wait):
			wait *= 2
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("GET %s: %w", path, lastErr)
}

func (c *Client) getOnce(ctx context.Context, path string, query url.Values, out any) error {
	u := c.cfg.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &temporaryError{err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("supplier answered %s", resp.Status)
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return &temporaryError{err}
		}
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode supplier response: %w", err)
	}
	return nil
}

// temporaryError marks failures worth retrying.
type temporaryError struct{ err error }

func (e *temporaryError) Error() string { return e.err.Error() }
func (e *temporaryError) Unwrap() error { return e.err }

func isTemporary(err error) bool {
	var t *temporaryError
	return errors.As(err, &t)
}
