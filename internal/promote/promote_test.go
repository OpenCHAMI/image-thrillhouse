// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package promote

import (
	"testing"

	"github.com/openchami/image-thrillhouse/internal/config"
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

func TestFindPublishAll(t *testing.T) {
	tlsOff := false

	t.Run("returns every block of the type in manifest order", func(t *testing.T) {
		publishes := []config.Publish{
			{Type: "registry", URL: "registry-a.example.com/images"},
			{Type: "s3", URL: "https://s3-a.example.com", Bucket: "boot-images-a"},
			{Type: "registry", URL: "registry-b.example.com/images"},
			{Type: "s3", URL: "https://s3-b.example.com", Bucket: "boot-images-b"},
		}

		regs := FindPublishAll(publishes, "registry")
		if len(regs) != 2 {
			t.Fatalf("len(registry blocks) = %d, want 2", len(regs))
		}
		if regs[0].URL != "registry-a.example.com/images" || regs[1].URL != "registry-b.example.com/images" {
			t.Errorf("registry blocks out of manifest order: %q, %q", regs[0].URL, regs[1].URL)
		}

		s3s := FindPublishAll(publishes, "s3")
		if len(s3s) != 2 {
			t.Fatalf("len(s3 blocks) = %d, want 2", len(s3s))
		}
		if s3s[0].Bucket != "boot-images-a" || s3s[1].Bucket != "boot-images-b" {
			t.Errorf("s3 blocks out of manifest order: %q, %q", s3s[0].Bucket, s3s[1].Bucket)
		}
	})

	t.Run("returns empty for an unconfigured type", func(t *testing.T) {
		publishes := []config.Publish{{Type: "registry", URL: "registry.example.com/images"}}
		if got := FindPublishAll(publishes, "s3"); len(got) != 0 {
			t.Errorf("FindPublishAll(s3) = %v, want empty", got)
		}
		if got := FindPublishAll(nil, "registry"); len(got) != 0 {
			t.Errorf("FindPublishAll(nil) = %v, want empty", got)
		}
	})

	t.Run("collapses identical blocks", func(t *testing.T) {
		publishes := []config.Publish{
			{Type: "s3", URL: "https://s3.example.com", Bucket: "boot", Prefix: "compute/", PromoteOnly: true},
			{Type: "s3", URL: "https://s3.example.com", Bucket: "boot", Prefix: "compute/", PromoteOnly: true},
		}
		if got := FindPublishAll(publishes, "s3"); len(got) != 1 {
			t.Errorf("len = %d, want 1 (identical blocks name one destination)", len(got))
		}
	})

	t.Run("keeps blocks differing only in tls-verify", func(t *testing.T) {
		publishes := []config.Publish{
			{Type: "registry", URL: "registry.example.com/images"},
			{Type: "registry", URL: "registry.example.com/images", TLSVerify: &tlsOff},
		}
		if got := FindPublishAll(publishes, "registry"); len(got) != 2 {
			t.Errorf("len = %d, want 2; tls-verify is part of a block's identity", len(got))
		}
	})

	t.Run("FindPublish returns the first of several", func(t *testing.T) {
		publishes := []config.Publish{
			{Type: "registry", URL: "registry-a.example.com/images"},
			{Type: "registry", URL: "registry-b.example.com/images"},
		}
		got, err := FindPublish(publishes, "registry")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.URL != "registry-a.example.com/images" {
			t.Errorf("URL = %q, want the first block", got.URL)
		}
	})
}
