// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package squashfs

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "absolute path",
			path: "/output/images",
		},
		{
			name: "relative path",
			path: "./output",
		},
		{
			name: "home directory",
			path: "~/images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := New(tt.path)
			if pub == nil {
				t.Fatal("New() returned nil")
			}
			if pub.path != tt.path {
				t.Errorf("New() path = %v, want %v", pub.path, tt.path)
			}
		})
	}
}

func TestSquashfsPublisher_Type(t *testing.T) {
	pub := New("/output")

	if _, ok := interface{}(pub).(*SquashfsPublisher); !ok {
		t.Error("New() did not return *SquashfsPublisher")
	}
}

func TestRemoveArtifacts(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "image.squashfs"),
		filepath.Join(dir, "image.vmlinuz"),
		filepath.Join(dir, "image.initramfs.img"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("artifact"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	removeArtifacts(slog.Default(), paths...)
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("artifact %s still exists; err = %v", path, err)
		}
	}
}
