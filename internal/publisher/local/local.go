package local

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/container"
)

type LocalPublisher struct{}

func New() *LocalPublisher {
	return &LocalPublisher{}
}

func (l *LocalPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string) error {
	log := slog.With("component", "publisher")
	for _, tag := range tags {
		id, err := c.Commit(ctx, name, tag)
		log.Info("Committing locally", "ContainerID", id, "Image", fmt.Sprintf("localhost/%s:%s", name, tag))
		if err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		log.Info("Committed locally", "ContainerID", id, "Image", fmt.Sprintf("localhost/%s:%s", name, tag))
	}
	return nil
}
