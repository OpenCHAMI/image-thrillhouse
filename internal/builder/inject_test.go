// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package builder

import (
	"context"
	"testing"

	"github.com/openchami/image-thrillhouse/internal/config"
)

// TestInjectArtifacts_NoConfigIsNoop is the load-bearing test for backward
// compatibility: a config that never sets container_images or other_files
// must behave exactly as it did before this feature existed. injectArtifacts
// must not touch the container in any way (no Run, no WriteFile, no
// CopyDirectory) and must not error.
func TestInjectArtifacts_NoConfigIsNoop(t *testing.T) {
	b := builderWithLayer(config.Layer{
		Manager: config.Manager{Name: "dnf"},
		// ContainerImages and OtherFiles both left as their zero value (nil).
	})
	c := &fakeContainer{}

	if err := b.injectArtifacts(context.Background(), c); err != nil {
		t.Fatalf("injectArtifacts with no container_images/other_files: %v", err)
	}

	if len(c.RunCalls) != 0 {
		t.Errorf("expected no Run calls, got %d: %v", len(c.RunCalls), c.RunCalls)
	}
	if len(c.WriteFileCalls) != 0 {
		t.Errorf("expected no WriteFile calls, got %d", len(c.WriteFileCalls))
	}
	if len(c.CopyDirectoryCalls) != 0 {
		t.Errorf("expected no CopyDirectory calls, got %d", len(c.CopyDirectoryCalls))
	}
	if len(c.Events) != 0 {
		t.Errorf("expected no container interaction at all, got events: %v", c.Events)
	}
}

// TestInjectArtifacts_EmptySlicesIsNoop covers the case where the YAML keys
// are present but empty (container_images: [] / other_files: []), which
// YAML unmarshals to a non-nil, zero-length slice. Behaviour must be
// identical to the nil case above.
func TestInjectArtifacts_EmptySlicesIsNoop(t *testing.T) {
	b := builderWithLayer(config.Layer{
		Manager:         config.Manager{Name: "dnf"},
		ContainerImages: []config.ContainerImage{},
		OtherFiles:      []config.OtherFile{},
	})
	c := &fakeContainer{}

	if err := b.injectArtifacts(context.Background(), c); err != nil {
		t.Fatalf("injectArtifacts with empty container_images/other_files: %v", err)
	}
	if len(c.Events) != 0 {
		t.Errorf("expected no container interaction at all, got events: %v", c.Events)
	}
}

// TestMaybeRemount_NonRemountableContainerIsNoop verifies that maybeRemount
// degrades gracefully (no panic, no error) against a container.Container
// implementation that doesn't support Remount() — which is the case for
// fakeContainer and for any future backend that never needs it.
func TestMaybeRemount_NonRemountableContainerIsNoop(t *testing.T) {
	b := builderWithLayer(config.Layer{Manager: config.Manager{Name: "dnf"}})
	c := &fakeContainer{}

	if err := b.maybeRemount(c); err != nil {
		t.Fatalf("maybeRemount against a non-remountable container: %v", err)
	}
}
