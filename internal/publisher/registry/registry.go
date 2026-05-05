// Package registry provides a publisher that pushes images to OCI container registries.
// It supports any Docker-compatible registry (Docker Hub, Harbor, Quay, etc.).
package registry

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/container"
)

// RegistryPublisher pushes container images to OCI-compatible registries.
// It supports authentication and TLS verification options.
type RegistryPublisher struct {
	url       string // Registry URL (e.g., "registry.io/repo", "docker.io/username")
	tlsVerify bool   // Whether to verify TLS certificates when pushing
}

// New creates a new RegistryPublisher with the specified configuration.
//
// Parameters:
//   - url: Registry URL prefix (e.g., "registry.io/myrepo" or "docker.io/username")
//   - tlsVerify: Whether to verify TLS certificates (set false for self-signed certs)
//
// Authentication is handled through the system's container auth configuration
// (typically ~/.docker/config.json or /run/containers/auth.json).
func New(url string, tlsVerify bool) *RegistryPublisher {
	return &RegistryPublisher{
		url:       url,
		tlsVerify: tlsVerify,
	}
}

// Publish pushes the container image to the configured registry.
// Each tag is pushed as a separate image reference.
//
// Image references are formatted as: <url>/<name>:<tag>
// For example, with url="registry.io/myrepo", name="rocky-base", tag="9.5":
// The image is pushed to: registry.io/myrepo/rocky-base:9.5
//
// Labels should be applied to the container before calling this method.
// This publisher doesn't apply labels itself; it pushes what's already in the container.
func (r *RegistryPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
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

func (l *RegistryPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	return false, nil
}
