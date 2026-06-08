package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	b, err := Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(b) != "payload" {
		t.Errorf("body = %q, want %q", b, "payload")
	}
}

func TestGet_4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention status 404, got: %v", err)
	}
}

func TestGet_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestGet_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Get(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}
}

func TestGet_BadURL(t *testing.T) {
	// http.NewRequestWithContext should fail to parse this and surface a
	// descriptive error rather than panicking.
	_, err := Get(context.Background(), "ht!tp://not a url")
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestGetStream_CallerClosesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("streamed"))
	}))
	defer srv.Close()

	rc, err := GetStream(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	defer rc.Close()

	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(b) != "streamed" {
		t.Errorf("body = %q, want %q", b, "streamed")
	}
}

func TestGetStream_2xxBoundary(t *testing.T) {
	// 299 is in the 2xx range and should NOT error — this guards against
	// regressing the boundary check to `<= 200 || >= 300`.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(299)
		w.Write([]byte("edge"))
	}))
	defer srv.Close()

	rc, err := GetStream(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	rc.Close()
}

func TestGet_BodySizeCap(t *testing.T) {
	// Shrink the cap so the test allocates kilobytes, not hundreds of MB.
	prev := MaxBodyBytes
	MaxBodyBytes = 1024
	t.Cleanup(func() { MaxBodyBytes = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxBodyBytes+1)) // one byte over
	}))
	defer srv.Close()

	_, err := Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected size-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("expected error to mention the cap, got: %v", err)
	}
}

func TestGet_AtCapAccepted(t *testing.T) {
	// A body of exactly the cap must succeed — boundary test for the
	// (cap + 1) LimitReader trick.
	prev := MaxBodyBytes
	MaxBodyBytes = 1024
	t.Cleanup(func() { MaxBodyBytes = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxBodyBytes))
	}))
	defer srv.Close()

	b, err := Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("at-cap body should succeed: %v", err)
	}
	if int64(len(b)) != MaxBodyBytes {
		t.Errorf("body len = %d, want %d", len(b), MaxBodyBytes)
	}
}

func TestDefaultTimeout(t *testing.T) {
	// Sanity-check that the documented default isn't accidentally zero or
	// negative, which would disable timeouts entirely — the whole point of
	// this helper was to avoid http.Get's no-timeout default.
	if DefaultTimeout <= 0 {
		t.Errorf("DefaultTimeout must be positive, got %v", DefaultTimeout)
	}
	if DefaultTimeout > 5*time.Minute {
		t.Errorf("DefaultTimeout %v is unexpectedly large", DefaultTimeout)
	}
}
