// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

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

// MaxBodyBytes caps Get's in-memory read to a sane upper bound. The artifacts
// Get is used for today (config files, GPG keys, OVAL definitions) are all
// well under this; the cap exists so a malicious or misconfigured URL serving
// gigabytes can't OOM the build process. Callers that legitimately need to
// stream large payloads should use GetStream and handle truncation themselves.
//
// Exposed as a var rather than a const so tests can lower the cap without
// allocating hundreds of MB on the test client. Production code should treat
// it as constant.
var MaxBodyBytes int64 = 256 * 1024 * 1024 // 256 MiB

// Get performs an HTTP GET against url using a client with DefaultTimeout
// and the supplied context. It returns the response body bytes for any 2xx
// status, or a descriptive error otherwise.
//
// The body is read fully into memory and capped at MaxBodyBytes — appropriate
// for the small artifacts (config files, GPG keys, OVAL definitions) the
// build pipeline currently uses. Exceeding the cap surfaces as an error
// rather than a truncated body so callers don't silently process partial
// data. For larger payloads use GetStream.
func Get(ctx context.Context, url string) ([]byte, error) {
	rc, err := GetStream(ctx, url)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// LimitReader by (cap + 1) so we can distinguish "exactly the cap" from
	// "too big" — a body of exactly MaxBodyBytes is valid; one byte more isn't.
	// Snapshot MaxBodyBytes so a concurrent test-side mutation can't change
	// the comparison mid-call. (Tests swap the var; production never does.)
	limit := MaxBodyBytes
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: read body: %w", url, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("fetch %s: body exceeds %d-byte cap", url, limit)
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
