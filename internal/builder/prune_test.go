// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package builder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"
)

// pruneRecorder captures what the prune seams were asked to remove, and in
// what order relative to the container's own teardown.
type pruneRecorder struct {
	imageIDs []string
	err      error
	events   *[]string // shared with the fake container so ordering is observable
}

func (r *pruneRecorder) image(id string) error {
	r.imageIDs = append(r.imageIDs, id)
	if r.events != nil {
		*r.events = append(*r.events, "prune-image")
	}
	return r.err
}

// pruneBuilder wires a Builder around the given fake container and recorder.
func pruneBuilder(fc *fakeContainer, rec *pruneRecorder, pubs []publisher.Publisher) *Builder {
	cfg := &config.Config{
		Meta:  config.Meta{Name: "test", Tags: []string{"1.0", "latest"}, From: "docker.io/library/alpine"},
		Layer: config.Layer{Manager: config.Manager{Name: "dnf"}},
	}
	b := buildableBuilder(cfg, fc, fakeBackendBase{}, pubs)
	b.pruneImage = rec.image
	return b
}

// failingPublisher stands in for a publish destination that rejects the image.
type failingPublisher struct{}

func (failingPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	return fmt.Errorf("registry unreachable")
}

func (failingPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	return false, nil
}

// TestBuild_PruneParentRemovesPulledImage is the core of the parent-image
// leak fix: a base image this run pulled must not survive the build.
func TestBuild_PruneParentRemovesPulledImage(t *testing.T) {
	fc := &fakeContainer{PulledParent: "sha256:abc123"}
	rec := &pruneRecorder{events: &fc.Events}
	b := pruneBuilder(fc, rec, []publisher.Publisher{&fakePublisher{}})
	b.SetPruneParent(true)

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !slices.Equal(rec.imageIDs, []string{"sha256:abc123"}) {
		t.Errorf("pruned image ids = %v, want [sha256:abc123]", rec.imageIDs)
	}
}

// TestBuild_PruneParentLeavesPreexistingImage guards the safety property that
// makes the flag usable on a shared runner: an image the host already had is
// never taken away, because NewContainer only reports an ID it pulled itself.
func TestBuild_PruneParentLeavesPreexistingImage(t *testing.T) {
	fc := &fakeContainer{PulledParent: ""} // already in the store, or a scratch build
	rec := &pruneRecorder{}
	b := pruneBuilder(fc, rec, []publisher.Publisher{&fakePublisher{}})
	b.SetPruneParent(true)

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rec.imageIDs) != 0 {
		t.Errorf("pruned %v, want nothing removed when the image was already present", rec.imageIDs)
	}
}

// TestBuild_PruneParentOffByDefault confirms the flag is opt-in: without it,
// the layer cache behaves exactly as it did before.
func TestBuild_PruneParentOffByDefault(t *testing.T) {
	fc := &fakeContainer{PulledParent: "sha256:abc123"}
	rec := &pruneRecorder{}
	b := pruneBuilder(fc, rec, []publisher.Publisher{&fakePublisher{}})

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rec.imageIDs) != 0 {
		t.Errorf("pruned %v with --prune-parent unset, want nothing removed", rec.imageIDs)
	}
}

// TestBuild_PruneParentRunsAfterContainerDelete pins the defer ordering.
// Deleting the image while the working container still references it fails,
// so the prune must come after the container is gone.
func TestBuild_PruneParentRunsAfterContainerDelete(t *testing.T) {
	fc := &fakeContainer{PulledParent: "sha256:abc123"}
	rec := &pruneRecorder{events: &fc.Events}
	b := pruneBuilder(fc, rec, []publisher.Publisher{&fakePublisher{}})
	b.SetPruneParent(true)

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	del := slices.Index(fc.Events, "delete")
	prune := slices.Index(fc.Events, "prune-image")
	if del < 0 || prune < 0 {
		t.Fatalf("expected both delete and prune-image events, got %v", fc.Events)
	}
	if prune < del {
		t.Errorf("prune ran before container delete (events: %v)", fc.Events)
	}
}

// TestBuild_PruneParentRunsOnFailedBuild: a build that dies partway has still
// pulled the base image, and that copy is exactly what accumulates on a CI
// runner across repeated failures.
func TestBuild_PruneParentRunsOnFailedBuild(t *testing.T) {
	fc := &fakeContainer{PulledParent: "sha256:abc123"}
	rec := &pruneRecorder{}
	b := pruneBuilder(fc, rec, []publisher.Publisher{failingPublisher{}})
	b.SetPruneParent(true)

	if err := b.Build(context.Background()); err == nil {
		t.Fatal("Build succeeded, want the publisher error")
	}
	if !slices.Equal(rec.imageIDs, []string{"sha256:abc123"}) {
		t.Errorf("pruned image ids = %v, want the pulled parent removed even on failure", rec.imageIDs)
	}
}

// TestBuild_PruneFailureDoesNotFailBuild: the image reached every destination
// the user asked for, so a cleanup problem is a warning, not a build failure.
func TestBuild_PruneFailureDoesNotFailBuild(t *testing.T) {
	fc := &fakeContainer{PulledParent: "sha256:abc123"}
	rec := &pruneRecorder{err: errors.New("image used by container")}
	b := pruneBuilder(fc, rec, []publisher.Publisher{&fakePublisher{}})
	b.SetPruneParent(true)

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build failed on a prune error: %v", err)
	}
}
