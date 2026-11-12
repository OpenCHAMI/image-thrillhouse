// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// TestObjectKeys pins the published layout: all three artifacts have fixed,
// generic names under the tag's directory.
func TestObjectKeys(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		arch   string
		tag    string
		want   string // directory the three artifacts live under
	}{
		{"prefix and arch", "compute/", "x86_64", "release-0.0.1", "compute/release-0.0.1/x86_64/"},
		{"no arch segment when arch empty", "compute/", "", "abc123", "compute/abc123/"},
		{"no prefix", "", "aarch64", "release-1.0", "release-1.0/aarch64/"},
		{
			"omnia release-tag layout",
			"slurm_control_node_rhel_10_0_x86_64/rhel-slurm-imgth/", "", "10.0",
			"slurm_control_node_rhel_10_0_x86_64/rhel-slurm-imgth/10.0/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New("", "", tt.prefix, tt.arch, "", "")
			rootfs, kernel, initramfs := pub.objectKeys(tt.tag)
			if want := tt.want + "rootfs.squashfs"; rootfs != want {
				t.Errorf("rootfs = %q, want %q", rootfs, want)
			}
			if want := tt.want + "vmlinuz"; kernel != want {
				t.Errorf("kernel = %q, want %q", kernel, want)
			}
			if want := tt.want + "initramfs.img"; initramfs != want {
				t.Errorf("initramfs = %q, want %q", initramfs, want)
			}
		})
	}
}

// TestRootfsKey pins the invariant that makes Exists work: the rootfs key must
// be derivable from prefix/tag/arch alone, because Exists is called before any
// image is pulled or mounted, and must match what Publish writes.
func TestRootfsKey(t *testing.T) {
	pub := New("", "", "compute/", "x86_64", "", "")

	want := "compute/10.0/x86_64/rootfs.squashfs"
	if got := pub.rootfsKey("10.0"); got != want {
		t.Errorf("rootfsKey = %q, want %q", got, want)
	}
	if rootfs, _, _ := pub.objectKeys("10.0"); rootfs != want {
		t.Errorf("objectKeys rootfs = %q, want %q", rootfs, want)
	}
}

func TestInitramfsKernelVersion(t *testing.T) {
	tests := []struct {
		name     string
		wantKver string
		wantOK   bool
	}{
		{"initramfs-5.14.0-362.el9.x86_64.img", "5.14.0-362.el9.x86_64", true},
		{"initrd-6.1.0-18-amd64", "6.1.0-18-amd64", true},
		{"initrd.img-5.15.0-91-generic", "5.15.0-91-generic", true},
		{"vmlinuz-5.14.0-362.el9.x86_64", "", false},
		{"initramfs-.img", "", false},
		{"initrd.img", "", false},
		{"config-6.1.0-18-amd64", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kver, ok := initramfsKernelVersion(tt.name)
			if kver != tt.wantKver || ok != tt.wantOK {
				t.Errorf("initramfsKernelVersion(%q) = %q, %v; want %q, %v",
					tt.name, kver, ok, tt.wantKver, tt.wantOK)
			}
		})
	}
}

// writeBootFiles creates each named file under root/boot with the given
// mtime offset from a fixed base, and a /lib/modules directory per kernel so
// the tree resembles a multi-kernel image.
func writeBootFiles(t *testing.T, root string, kvers []string, files map[string]time.Duration) {
	t.Helper()
	for _, kver := range kvers {
		if err := os.MkdirAll(filepath.Join(root, "lib", "modules", kver), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	boot := filepath.Join(root, "boot")
	if err := os.MkdirAll(boot, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for name, age := range files {
		p := filepath.Join(boot, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func assertBootInitramfs(t *testing.T, root, wantKver, wantName string) {
	t.Helper()
	path, kver, err := (&S3Publisher{}).findBootInitramfs(root)
	if err != nil {
		t.Fatalf("findBootInitramfs: %v", err)
	}
	if kver != wantKver {
		t.Errorf("kernel version = %q, want %q", kver, wantKver)
	}
	if want := filepath.Join(root, "boot", wantName); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// TestFindBootInitramfs covers the case /lib/modules can't disambiguate:
// modules for several kernels are present. The highest kernel version must
// win even when an older kernel's initramfs was rebuilt more recently, and
// kdump and rescue images must be ignored even when they are newest.
func TestFindBootInitramfs(t *testing.T) {
	const (
		oldKver = "5.14.0-362.el9.x86_64"
		newKver = "5.14.0-427.el9.x86_64"
	)
	root := t.TempDir()
	writeBootFiles(t, root, []string{oldKver, newKver}, map[string]time.Duration{
		"vmlinuz-" + oldKver:                 0,
		"vmlinuz-" + newKver:                 0,
		"initramfs-" + newKver + ".img":      10 * time.Minute,
		"initramfs-" + oldKver + ".img":      20 * time.Minute, // rebuilt later
		"initramfs-" + oldKver + "kdump.img": 30 * time.Minute,
		"vmlinuz-0-rescue-abc123":            0,
		"initramfs-0-rescue-abc123.img":      40 * time.Minute,
	})
	assertBootInitramfs(t, root, newKver, "initramfs-"+newKver+".img")
}

// TestFindBootInitramfs_EqualMtimes is the regression for selection by
// filename: with identical mtimes (common in image layers), 6.10 must beat
// 6.9 even though "6.9" sorts after "6.10" as a string.
func TestFindBootInitramfs_EqualMtimes(t *testing.T) {
	const (
		kver9  = "6.9.0-1-amd64"
		kver10 = "6.10.0-1-amd64"
	)
	root := t.TempDir()
	writeBootFiles(t, root, []string{kver9, kver10}, map[string]time.Duration{
		"vmlinuz-" + kver9:     0,
		"vmlinuz-" + kver10:    0,
		"initrd.img-" + kver9:  0,
		"initrd.img-" + kver10: 0,
	})
	assertBootInitramfs(t, root, kver10, "initrd.img-"+kver10)
}

// TestFindBootInitramfs_DefaultSymlink checks that a distro's default-kernel
// symlink overrides version ordering, at either supported location.
func TestFindBootInitramfs_DefaultSymlink(t *testing.T) {
	const (
		oldKver = "6.1.0-17-amd64"
		newKver = "6.1.0-18-amd64"
	)
	for _, link := range defaultKernelLinks {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			writeBootFiles(t, root, []string{oldKver, newKver}, map[string]time.Duration{
				"vmlinuz-" + oldKver:    0,
				"vmlinuz-" + newKver:    0,
				"initrd.img-" + oldKver: 0,
				"initrd.img-" + newKver: 0,
			})
			// Absolute target, as Debian writes it: only the basename may
			// be used, never resolved on the host.
			if err := os.Symlink("/boot/vmlinuz-"+oldKver, filepath.Join(root, link)); err != nil {
				t.Fatal(err)
			}
			assertBootInitramfs(t, root, oldKver, "initrd.img-"+oldKver)
		})
	}
}

// TestFindBootInitramfs_DanglingDefault ignores a default symlink naming a
// kernel that has no usable initramfs, falling back to version ordering.
func TestFindBootInitramfs_DanglingDefault(t *testing.T) {
	const kver = "6.1.0-18-amd64"
	root := t.TempDir()
	writeBootFiles(t, root, []string{kver}, map[string]time.Duration{
		"vmlinuz-" + kver:    0,
		"initrd.img-" + kver: 0,
	})
	if err := os.Symlink("boot/vmlinuz-6.1.0-99-amd64", filepath.Join(root, "vmlinuz")); err != nil {
		t.Fatal(err)
	}
	assertBootInitramfs(t, root, kver, "initrd.img-"+kver)
}

func TestFindBootInitramfs_NoneFound(t *testing.T) {
	root := t.TempDir()
	// An initramfs without its kernel is not bootable and must not be chosen.
	writeBootFiles(t, root, nil, map[string]time.Duration{"initramfs-6.1.0.img": 0})
	if _, _, err := (&S3Publisher{}).findBootInitramfs(root); err == nil {
		t.Error("expected error when no initramfs has a matching vmlinuz")
	}
}

func TestCompareKernelVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"6.10.0", "6.9.0", 1},
		{"6.9.0-1-amd64", "6.10.0-1-amd64", -1},
		{"5.14.0-427.el9.x86_64", "5.14.0-362.24.1.el9_3.x86_64", 1},
		{"5.14.0-362.24.1.el9_3.x86_64", "5.14.0-362.el9.x86_64", 1},
		{"6.12.0-55.94.1.el10_0.x86_64", "6.12.0-55.94.1.el10_0.x86_64", 0},
		{"6.1.0-18-amd64", "6.1.0-9-amd64", 1},
		{"5.15.0-91-generic", "5.15.0-101-generic", -1},
		{"6.1", "6.1.0", -1},
		{"6.1~rc1", "6.1", -1},
		{"6.1.0", "6.1.a", 1},
		{"6.01", "6.1", -1}, // rpm-equal; falls back to string order
	}
	for _, tt := range tests {
		if got := compareKernelVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareKernelVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := compareKernelVersions(tt.b, tt.a); got != -tt.want {
			t.Errorf("compareKernelVersions(%q, %q) = %d, want %d", tt.b, tt.a, got, -tt.want)
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
