// Package local provides a publisher that commits images to local container storage.
// This publisher uses buildah commit to store images in the local podman/buildah registry.
package local

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/buildah"
	"github.com/travisbcotton/image-build/internal/container"
)

// LocalPublisher publishes images to the local container storage.
// Images are committed using buildah and stored in the local podman/buildah registry.
// These images can then be used as parent images for other builds or run with podman.
type LocalPublisher struct{}

// New creates a new LocalPublisher instance.
func New() *LocalPublisher {
	return &LocalPublisher{}
}

// Publish commits the container to local storage with the provided labels.
// Each tag is committed separately with the same labels.
//
// The image is tagged as "localhost/<name>:<tag>" in the local container storage.
func (l *LocalPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	log := slog.With("component", "publisher.local")
	if len(tags) == 0 {
		return fmt.Errorf("local publisher requires at least one tag")
	}

	images := make([]string, len(tags))
	for i, tag := range tags {
		images[i] = fmt.Sprintf("localhost/%s:%s", name, tag)
	}

	log.Info("Committing locally", "images", images)
	id, err := c.CommitWithLabelsTags(ctx, name, tags, labels)
	if err != nil {
		return fmt.Errorf("commit %v: %w", images, err)
	}
	log.Info("Committed locally", "images", images, "containerID", id)
	return nil
}

// Exists reports whether every (name, tag) pair is already present in local
// container storage as "localhost/<name>:<tag>". Returns false as soon as any
// tag is missing.
func (l *LocalPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	for _, t := range tags {
		ref := fmt.Sprintf("localhost/%s:%s", name, t)
		ok, err := buildah.ImageExists(ref)
		if err != nil {
			return false, fmt.Errorf("check local image %s: %w", ref, err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
