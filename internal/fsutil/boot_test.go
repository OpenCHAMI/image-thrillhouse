// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// mkroot creates files (relative paths) and directories (trailing "/") under
// a fresh temp root.
func mkroot(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindBootFiles(t *testing.T) {
	tests := []struct {
		name          string
		paths         []string
		wantInitramfs string
		wantNoKernel  bool
		wantErr       bool
	}{
		{
			name:          "RHEL/Rocky style",
			paths:         []string{"lib/modules/5.14.0-362.el9.x86_64/", "boot/vmlinuz-5.14.0-362.el9.x86_64", "boot/initramfs-5.14.0-362.el9.x86_64.img"},
			wantInitramfs: "boot/initramfs-5.14.0-362.el9.x86_64.img",
		},
		{
			name:          "Debian style",
			paths:         []string{"lib/modules/6.1.0-18-amd64/", "boot/vmlinuz-6.1.0-18-amd64", "boot/initrd-6.1.0-18-amd64"},
			wantInitramfs: "boot/initrd-6.1.0-18-amd64",
		},
		{
			name:          "Ubuntu style",
			paths:         []string{"lib/modules/5.15.0-91-generic/", "boot/vmlinuz-5.15.0-91-generic", "boot/initrd.img-5.15.0-91-generic"},
			wantInitramfs: "boot/initrd.img-5.15.0-91-generic",
		},
		{
			name:         "no /lib/modules",
			paths:        []string{"etc/"},
			wantNoKernel: true,
		},
		{
			name:         "empty /lib/modules",
			paths:        []string{"lib/modules/"},
			wantNoKernel: true,
		},
		{
			name:    "kernel without vmlinuz",
			paths:   []string{"lib/modules/6.1.0/", "boot/initrd-6.1.0"},
			wantErr: true,
		},
		{
			name:    "kernel without initramfs",
			paths:   []string{"lib/modules/6.1.0/", "boot/vmlinuz-6.1.0"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := mkroot(t, tt.paths...)
			bf, err := FindBootFiles(root)

			if got := errors.Is(err, ErrNoKernel); got != tt.wantNoKernel {
				t.Fatalf("errors.Is(err, ErrNoKernel) = %v, want %v (err: %v)", got, tt.wantNoKernel, err)
			}
			if tt.wantNoKernel || tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := filepath.Join(root, "boot", "vmlinuz-"+bf.KernelVersion); bf.Vmlinuz != want {
				t.Errorf("Vmlinuz = %q, want %q", bf.Vmlinuz, want)
			}
			if want := filepath.Join(root, tt.wantInitramfs); bf.Initramfs != want {
				t.Errorf("Initramfs = %q, want %q", bf.Initramfs, want)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old contents"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("dst = %q, want %q", got, "new")
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("expected only src and dst in dir, got %d entries", len(entries))
	}
}
