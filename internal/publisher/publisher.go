package publisher

import (
	"context"

	"github.com/travisbcotton/image-build/internal/container"
)

type Publisher interface {
	Publish(ctx context.Context, c container.Container, name string, tags []string) error
}
