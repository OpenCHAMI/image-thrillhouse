// Package fetch provides a small HTTP GET helper that respects the caller's
// context and applies a sane timeout. It exists so that callers don't reach
// for http.Get (which uses Go's default client — no timeout, no cancellation
// — and silently ignores context), and so that fetching logic isn't
// duplicated across the builder, container, and backend layers.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout is applied to the underlying *http.Client when callers use
// Get. It bounds the total time the request can spend across DNS, dial, TLS,
// and body read combined. Cancel sooner by cancelling the supplied context.
const DefaultTimeout = 60 * time.Second

// Get performs an HTTP GET against url using a client with DefaultTimeout
// and the supplied context. It returns the response body bytes for any 2xx
// status, or a descriptive error otherwise.
//
// The body is read fully into memory — appropriate for the small artifacts
// (config files, GPG keys, OVAL definitions) the build pipeline currently
// uses. For larger payloads use GetStream.
func Get(ctx context.Context, url string) ([]byte, error) {
	rc, err := GetStream(ctx, url)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: read body: %w", url, err)
	}
	return b, nil
}

// GetStream is the streaming variant of Get. The caller must Close the
// returned io.ReadCloser.
func GetStream(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: build request: %w", url, err)
	}
	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}
