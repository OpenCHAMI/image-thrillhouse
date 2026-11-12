// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.podman.io/buildah/define"

	"github.com/openchami/image-thrillhouse/internal/config"
	"github.com/openchami/image-thrillhouse/internal/container"
)

// fakeContainer is a container.Container that only knows where its rootfs is
// mounted. Publish reads nothing else off the container, so the rest of the
// interface is deliberately inert — this keeps the test honest about which
// dependency is actually being exercised.
type fakeContainer struct {
	mountPath string
}

func (f *fakeContainer) MountPath() string { return f.mountPath }

func (f *fakeContainer) Run(context.Context, []string, container.RunMode, container.OutputWriter, ...container.RunOption) error {
	return nil
}

func (f *fakeContainer) RunScript(context.Context, string, container.OutputWriter, ...container.RunOption) error {
	return nil
}
func (f *fakeContainer) WriteFile(context.Context, config.File) error { return nil }
func (f *fakeContainer) CopyDirectory(context.Context, string, string, container.CopyDirectoryOptions) error {
	return nil
}
func (f *fakeContainer) SetLabels(map[string]string) {}
func (f *fakeContainer) CommitWithLabelsTags(context.Context, string, []string, map[string]string) (string, error) {
	return "", nil
}
func (f *fakeContainer) GetID() string                                        { return "fake" }
func (f *fakeContainer) GetParent() string                                    { return "" }
func (f *fakeContainer) PulledParentID() string                               { return "" }
func (f *fakeContainer) GetName() string                                      { return "fake" }
func (f *fakeContainer) Delete()                                              {}
func (f *fakeContainer) GetIsolation() define.Isolation                       { return define.IsolationDefault }
func (f *fakeContainer) CommitToRegistry(context.Context, string, bool) error { return nil }

// uploadRecorder stands in for an S3-compatible endpoint and records the keys
// that were written, so a test can assert the published object layout.
type uploadRecorder struct {
	mu       sync.Mutex
	uploaded []string
}

func (u *uploadRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPut {
		u.mu.Lock()
		u.uploaded = append(u.uploaded, req.URL.Path)
		u.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (u *uploadRecorder) keys() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := append([]string(nil), u.uploaded...)
	sort.Strings(out)
	return out
}

// newFakeRootfs builds the minimum tree Publish inspects: the /boot kernel and
// initramfs (the initramfs name is how the kernel version is discovered), plus
// a matching kernel module directory as a real image would have.
func newFakeRootfs(t *testing.T, kver, initramfsName string) string {
	t.Helper()

	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "lib", "modules", kver))
	mustMkdirAll(t, filepath.Join(root, "boot"))

	mustWrite(t, filepath.Join(root, "boot", "vmlinuz-"+kver), "fake kernel")
	mustWrite(t, filepath.Join(root, "boot", initramfsName), "fake initramfs")

	return root
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPublish_UploadsGenericKeys is the end-to-end proof of the layout: given
// an image whose /boot carries versioned kernel artifacts, Publish uploads
// them under the generic names vmlinuz and initramfs.img, since the tag's
// directory already identifies the image.
//
// It exercises the real Publish path (kernel discovery, initramfs discovery,
// squashfs, upload) against a recording HTTP endpoint, so it would catch a
// regression anywhere in that chain — not just in key construction.
func TestPublish_UploadsGenericKeys(t *testing.T) {
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("mksquashfs not installed; skipping end-to-end publish test")
	}

	const kver = "6.12.0-55.94.1.el10_0.x86_64"
	root := newFakeRootfs(t, kver, "initramfs-"+kver+".img")

	rec := &uploadRecorder{}
	ts := httptest.NewServer(rec)
	defer ts.Close()

	// Mirrors Omnia's layout: the OS version is the tag and no arch segment is
	// configured.
	pub := New(ts.URL, "boot-images", "slurm_control_node_rhel_10_0_x86_64/rhel-slurm-imgth/", "", "access", "secret")

	err := pub.Publish(context.Background(), &fakeContainer{mountPath: root}, "slurm", []string{"10.0"}, nil)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	base := "/boot-images/slurm_control_node_rhel_10_0_x86_64/rhel-slurm-imgth/10.0/"
	want := []string{
		base + "initramfs.img",
		base + "rootfs.squashfs",
		base + "vmlinuz",
	}
	if got := rec.keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("uploaded keys =\n %v\nwant\n %v", got, want)
	}
}

// TestPublish_NormalizesDebianInitrdName confirms Debian/Ubuntu images, whose
// /boot names the initramfs initrd.img-<kver>, publish it as initramfs.img
// like every other distro.
func TestPublish_NormalizesDebianInitrdName(t *testing.T) {
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("mksquashfs not installed; skipping end-to-end publish test")
	}

	const kver = "6.1.0-18-amd64"
	root := newFakeRootfs(t, kver, "initrd.img-"+kver)

	rec := &uploadRecorder{}
	ts := httptest.NewServer(rec)
	defer ts.Close()

	pub := New(ts.URL, "boot-images", "compute/", "x86_64", "access", "secret")

	if err := pub.Publish(context.Background(), &fakeContainer{mountPath: root}, "compute", []string{"bookworm"}, nil); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	base := "/boot-images/compute/bookworm/x86_64/"
	want := []string{
		base + "initramfs.img",
		base + "rootfs.squashfs",
		base + "vmlinuz",
	}
	if got := rec.keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("uploaded keys =\n %v\nwant\n %v", got, want)
	}
}
