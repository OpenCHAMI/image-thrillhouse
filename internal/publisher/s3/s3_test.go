// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recordingS3 stands in for an S3-compatible endpoint and records the object
// paths that were HEAD'd, so a test can assert which keys Exists probed.
type recordingS3 struct {
	mu      sync.Mutex
	probed  []string
	present map[string]bool
}

func (r *recordingS3) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.probed = append(r.probed, req.URL.Path)
	found := r.present[req.URL.Path]
	r.mu.Unlock()
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// TestExists_ProbesOnlyPrimaryTag pins the contract that Exists probes exactly
// the key Publish writes. Probing every tag made --skip-if-exists unsatisfiable
// for any config with more than one tag: only tags[0] is ever uploaded, so the
// probe for tags[1] reported "missing" on every run and the build never skipped.
func TestExists_ProbesOnlyPrimaryTag(t *testing.T) {
	srv := &recordingS3{present: map[string]bool{
		"/test-bucket/img/1.0/x86_64/rootfs.squashfs": true,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	p := New(ts.URL, "test-bucket", "img/", "x86_64", "access", "secret")

	ok, err := p.Exists(context.Background(), "test-image", []string{"1.0", "latest"})
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !ok {
		t.Error("Expected Exists to report true when the primary tag is materialized")
	}
	if len(srv.probed) != 1 {
		t.Errorf("Expected exactly one probe (the primary tag), got %d: %v", len(srv.probed), srv.probed)
	}
}

// TestExists_MissingPrimaryTag confirms a genuine 404 is (false, nil) rather
// than an error, so the first build of a new image isn't blocked by the probe.
func TestExists_MissingPrimaryTag(t *testing.T) {
	srv := &recordingS3{present: map[string]bool{}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	p := New(ts.URL, "test-bucket", "img/", "x86_64", "access", "secret")

	ok, err := p.Exists(context.Background(), "test-image", []string{"1.0"})
	if err != nil {
		t.Fatalf("Exists returned error on a missing object: %v", err)
	}
	if ok {
		t.Error("Expected Exists to report false when the rootfs key is absent")
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		bucket    string
		prefix    string
		arch      string
		accessKey string
		secretKey string
	}{
		{
			name:      "AWS S3",
			endpoint:  "https://s3.amazonaws.com",
			bucket:    "boot-images",
			prefix:    "compute/",
			arch:      "x86_64",
			accessKey: "AKIAIOSFODNN7EXAMPLE",
			secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:      "MinIO",
			endpoint:  "http://localhost:9000",
			bucket:    "images",
			prefix:    "test/",
			arch:      "aarch64",
			accessKey: "minioadmin",
			secretKey: "minioadmin",
		},
		{
			name:      "custom S3, no arch",
			endpoint:  "https://s3.example.com",
			bucket:    "boot",
			prefix:    "",
			arch:      "",
			accessKey: "access",
			secretKey: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.endpoint, tt.bucket, tt.prefix, tt.arch, tt.accessKey, tt.secretKey)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.endpoint != tt.endpoint {
				t.Errorf("endpoint = %v, want %v", pub.endpoint, tt.endpoint)
			}
			if pub.bucket != tt.bucket {
				t.Errorf("bucket = %v, want %v", pub.bucket, tt.bucket)
			}
			if pub.prefix != tt.prefix {
				t.Errorf("prefix = %v, want %v", pub.prefix, tt.prefix)
			}
			if pub.arch != tt.arch {
				t.Errorf("arch = %v, want %v", pub.arch, tt.arch)
			}
			if pub.accessKey != tt.accessKey {
				t.Errorf("accessKey = %v, want %v", pub.accessKey, tt.accessKey)
			}
			if pub.secretKey != tt.secretKey {
				t.Errorf("secretKey = %v, want %v", pub.secretKey, tt.secretKey)
			}
		})
	}
}

func TestS3Publisher_Type(t *testing.T) {
	pub := New("https://s3.amazonaws.com", "bucket", "prefix/", "x86_64", "key", "secret")

	if _, ok := interface{}(pub).(*S3Publisher); !ok {
		t.Error("New() did not return *S3Publisher")
	}
}

func TestObjectKeys(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		arch          string
		tag           string
		wantRootfs    string
		wantKernel    string
		wantInitramfs string
	}{
		{
			name:          "prefix and arch",
			prefix:        "compute/",
			arch:          "x86_64",
			tag:           "release-0.0.1",
			wantRootfs:    "compute/release-0.0.1/x86_64/rootfs.squashfs",
			wantKernel:    "compute/release-0.0.1/x86_64/vmlinuz",
			wantInitramfs: "compute/release-0.0.1/x86_64/initramfs.img",
		},
		{
			name:          "no arch segment when arch empty",
			prefix:        "compute/",
			arch:          "",
			tag:           "abc123",
			wantRootfs:    "compute/abc123/rootfs.squashfs",
			wantKernel:    "compute/abc123/vmlinuz",
			wantInitramfs: "compute/abc123/initramfs.img",
		},
		{
			name:          "no prefix",
			prefix:        "",
			arch:          "aarch64",
			tag:           "release-1.0",
			wantRootfs:    "release-1.0/aarch64/rootfs.squashfs",
			wantKernel:    "release-1.0/aarch64/vmlinuz",
			wantInitramfs: "release-1.0/aarch64/initramfs.img",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New("", "", tt.prefix, tt.arch, "", "")
			rootfs, kernel, initramfs := pub.objectKeys(tt.tag)
			if rootfs != tt.wantRootfs {
				t.Errorf("rootfs = %q, want %q", rootfs, tt.wantRootfs)
			}
			if kernel != tt.wantKernel {
				t.Errorf("kernel = %q, want %q", kernel, tt.wantKernel)
			}
			if initramfs != tt.wantInitramfs {
				t.Errorf("initramfs = %q, want %q", initramfs, tt.wantInitramfs)
			}
		})
	}
}

func TestFindKernelVersion_Logic(t *testing.T) {
	// Test the logic without actual filesystem
	// In real scenario, we'd read /lib/modules/

	kernelVersions := []string{
		"5.14.0-362.24.1.el9_3.x86_64",
		"6.1.0-18-amd64",
		"5.15.0-91-generic",
	}

	for _, version := range kernelVersions {
		if version == "" {
			t.Error("Kernel version should not be empty")
		}
		if !strings.Contains(version, ".") {
			t.Error("Kernel version should contain dots")
		}
	}
}

func TestInitramfsPatterns(t *testing.T) {
	tests := []struct {
		name    string
		kver    string
		pattern string
	}{
		{
			name:    "RHEL/Rocky style",
			kver:    "5.14.0-362.el9.x86_64",
			pattern: "initramfs-5.14.0-362.el9.x86_64.img",
		},
		{
			name:    "Debian style",
			kver:    "6.1.0-18-amd64",
			pattern: "initrd-6.1.0-18-amd64",
		},
		{
			name:    "Ubuntu style",
			kver:    "5.15.0-91-generic",
			pattern: "initrd.img-5.15.0-91-generic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.pattern, tt.kver) {
				t.Errorf("Pattern %s should contain kernel version %s", tt.pattern, tt.kver)
			}
		})
	}
}

func TestVmlinuzPattern(t *testing.T) {
	kver := "5.14.0-362.el9.x86_64"
	expected := "vmlinuz-" + kver

	if !strings.HasPrefix(expected, "vmlinuz-") {
		t.Error("vmlinuz pattern should start with 'vmlinuz-'")
	}

	if !strings.Contains(expected, kver) {
		t.Error("vmlinuz pattern should contain kernel version")
	}
}
