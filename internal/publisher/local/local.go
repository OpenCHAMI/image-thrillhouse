// Package local provides a publisher that commits images to local container storage.
// This publisher uses buildah commit to store images in the local podman/buildah registry.
package local

import (
	"context"
	"fmt"
	"log/slog"

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
	log := slog.With("component", "publisher")
	for _, tag := range tags {
		id, err := c.CommitWithLabels(ctx, name, tag, labels)
		log.Info("Committing locally", "ContainerID", id, "Image", fmt.Sprintf("localhost/%s:%s", name, tag))
		if err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		log.Info("Committed locally", "ContainerID", id, "Image", fmt.Sprintf("localhost/%s:%s", name, tag))
	}
	return nil
}
