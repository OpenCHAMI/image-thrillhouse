// internal/publisher/registry/registry.go
package registry

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/container"
)

type RegistryPublisher struct {
	url       string
	tlsVerify bool
}

func New(url string, tlsVerify bool) *RegistryPublisher {
	return &RegistryPublisher{
		url:       url,
		tlsVerify: tlsVerify,
	}
}

func (r *RegistryPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string) error {
	for _, t := range tags {
		ref := fmt.Sprintf("%s/%s:%s", r.url, name, t)
		slog.Info("pushing to registry", "ref", ref)
		if err := c.CommitToRegistry(ctx, ref, r.tlsVerify); err != nil {
			return fmt.Errorf("push %s: %w", ref, err)
		}
		slog.Info("pushed to registry", "ref", ref)
	}
	return nil
}
