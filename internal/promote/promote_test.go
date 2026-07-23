// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package promote

import (
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

func TestRegistrySourceRef(t *testing.T) {
	src := RegistrySource{
		URL:  "registry.example:5000/openchami",
		Name: "demo-base",
		Tag:  "abc123",
	}

	if got, want := src.Ref(), "registry.example:5000/openchami/demo-base:abc123"; got != want {
		t.Errorf("Ref() = %q, want %q", got, want)
	}
	if got, want := src.RefWithTag("release-0.0.1"), "registry.example:5000/openchami/demo-base:release-0.0.1"; got != want {
		t.Errorf("RefWithTag() = %q, want %q", got, want)
	}
	// Ref must be exactly RefWithTag(Tag) so the source and retag paths spell a
	// reference identically.
	if src.Ref() != src.RefWithTag(src.Tag) {
		t.Errorf("Ref() = %q, RefWithTag(Tag) = %q; must match", src.Ref(), src.RefWithTag(src.Tag))
	}
}

func TestFindPublish(t *testing.T) {
	publishes := []config.Publish{
		{Type: "local"},
		{Type: "registry", URL: "registry.example:5000/openchami"},
		{Type: "s3", Bucket: "boot-images", Prefix: "compute/"},
	}

	t.Run("returns the first matching block", func(t *testing.T) {
		reg, err := FindPublish(publishes, "registry")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reg.URL != "registry.example:5000/openchami" {
			t.Errorf("URL = %q, want registry.example:5000/openchami", reg.URL)
		}

		s3, err := FindPublish(publishes, "s3")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s3.Bucket != "boot-images" {
			t.Errorf("Bucket = %q, want boot-images", s3.Bucket)
		}
	})

	t.Run("errors when no block of the type is present", func(t *testing.T) {
		_, err := FindPublish(publishes, "squashfs")
		if err == nil {
			t.Fatal("expected error for missing block type, got nil")
		}
	})

	t.Run("errors on empty publish list", func(t *testing.T) {
		if _, err := FindPublish(nil, "registry"); err == nil {
			t.Fatal("expected error for empty publish list, got nil")
		}
	})
}
