package oscap

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helloOVALBz2Hex is `printf 'hello oval\n' | bzip2 -c`, hex-encoded.
// Embedded so the test doesn't need a fixture file or a bzip2 encoder
// (Go's compress/bzip2 only decompresses).
const helloOVALBz2Hex = "425a6839314159265359db976e080000025180001040002244810020002200f284302385d1217c5dc914e142436e5db820"

const helloOVALPlain = "hello oval\n"

func TestFetchOVAL_Bzip2(t *testing.T) {
	body, err := hex.DecodeString(helloOVALBz2Hex)
	if err != nil {
		t.Fatalf("hex decode fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-bzip2")
		w.Write(body)
	}))
	defer srv.Close()

	out, err := fetchOVAL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOVAL: %v", err)
	}
	if string(out) != helloOVALPlain {
		t.Errorf("decoded body = %q, want %q", out, helloOVALPlain)
	}
}

func TestFetchOVAL_PlainXMLPassthrough(t *testing.T) {
	const plain = `<?xml version="1.0"?><oval_definitions/>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(plain))
	}))
	defer srv.Close()

	out, err := fetchOVAL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOVAL: %v", err)
	}
	if string(out) != plain {
		t.Errorf("body = %q, want %q", out, plain)
	}
}

func TestFetchOVAL_ShortBody(t *testing.T) {
	// Bodies shorter than the 3-byte magic should round-trip verbatim
	// (no bzip2 decoder invoked).
	const short = "ab"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(short))
	}))
	defer srv.Close()

	out, err := fetchOVAL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOVAL: %v", err)
	}
	if string(out) != short {
		t.Errorf("body = %q, want %q", out, short)
	}
}

func TestFetchOVAL_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out, err := fetchOVAL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOVAL: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("body = %q, want empty", out)
	}
}

func TestFetchOVAL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	_, err := fetchOVAL(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 410, got nil")
	}
	if !strings.Contains(err.Error(), "410") {
		t.Errorf("expected error to mention status 410, got: %v", err)
	}
}

func TestFetchOVAL_Bzip2Corrupt(t *testing.T) {
	// Magic bytes look like bzip2 but the rest is garbage — bzip2 reader
	// should surface a decode error rather than silently returning the
	// raw bytes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("BZh garbage payload"))
	}))
	defer srv.Close()

	_, err := fetchOVAL(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected decode error for corrupt bzip2 stream, got nil")
	}
}

func TestFetchOVAL_CompressedCap(t *testing.T) {
	// Shrink the cap so the test moves kilobytes, not hundreds of MB.
	prev := maxOVALCompressed
	maxOVALCompressed = 1024
	t.Cleanup(func() { maxOVALCompressed = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve plain (non-bz2) bytes one over the compressed cap.
		w.Write(make([]byte, maxOVALCompressed+1))
	}))
	defer srv.Close()

	_, err := fetchOVAL(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected size-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("expected error to mention the cap, got: %v", err)
	}
}

func TestFetchOVAL_DecompressedCap(t *testing.T) {
	// The embedded bzip2 fixture decompresses to 11 bytes ("hello oval\n").
	// Shrink the decompressed cap below that so the cap kicks in even on
	// a tiny payload.
	prev := maxOVALDecompressed
	maxOVALDecompressed = 5
	t.Cleanup(func() { maxOVALDecompressed = prev })

	body, err := hex.DecodeString(helloOVALBz2Hex)
	if err != nil {
		t.Fatalf("hex decode fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	_, err = fetchOVAL(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected decompressed-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "decompressed") {
		t.Errorf("expected error to mention 'decompressed', got: %v", err)
	}
}

func TestFetchOVAL_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the test cancels.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling so the GET fails immediately

	_, err := fetchOVAL(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}
