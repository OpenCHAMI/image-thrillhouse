package local

import (
	"context"
	"fmt"

	"github.com/travisbcotton/image-build/internal/container"
)

type LocalPublisher struct{}

func New() *LocalPublisher {
	return &LocalPublisher{}
}

func (l *LocalPublisher) Publish(ctx context.Context, c container.Container, name, tag string) error {
	id, err := c.Commit(ctx, name, tag)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("published local: localhost/%s:%s (%s)\n", name, tag, id)
	return nil
}
