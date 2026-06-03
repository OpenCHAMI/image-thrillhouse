// Package registry provides a publisher that pushes images to OCI container registries.
// It supports any Docker-compatible registry (Docker Hub, Harbor, Quay, etc.).
package registry

import (
	"context"
	"fmt"
	"log/slog"

	"go.podman.io/image/v5/docker"
	"go.podman.io/image/v5/types"

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

// Exists reports whether every (name, tag) pair is already present in the
// remote registry. It probes each tag's manifest endpoint and short-circuits
// on the first missing tag.
//
// A network/auth/transport error surfaces as (false, err) rather than being
// silently treated as "missing" — skip-if-exists should fail loud when it
// can't tell. Operators who want best-effort behaviour can disable the flag.
func (r *RegistryPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	sys := &types.SystemContext{
		DockerInsecureSkipTLSVerify: types.NewOptionalBool(!r.tlsVerify),
	}

	for _, t := range tags {
		ref := fmt.Sprintf("%s/%s:%s", r.url, name, t)
		ok, err := manifestExists(ctx, sys, ref)
		if err != nil {
			return false, fmt.Errorf("probe %s: %w", ref, err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// manifestExists returns true if the manifest for ref is reachable in the
// registry. Pulled into a helper because docker.NewReference / NewImageSource
// / GetManifest is the canonical "does this exist" probe in containers/image
// and we want one consistent failure-handling spot.
func manifestExists(ctx context.Context, sys *types.SystemContext, ref string) (bool, error) {
	imageRef, err := docker.ParseReference("//" + ref)
	if err != nil {
		return false, fmt.Errorf("parse ref: %w", err)
	}

	src, err := imageRef.NewImageSource(ctx, sys)
	if err != nil {
		// Authoritative "not found" surfaces as a manifest-not-found error
		// here on most registries. We can't reliably distinguish 404 from
		// other transport failures without inspecting registry-specific
		// error types, so surface every failure to the caller — the
		// skip-if-exists feature must not silently treat an outage as
		// "build it anyway".
		return false, err
	}
	defer src.Close()

	if _, _, err := src.GetManifest(ctx, nil); err != nil {
		return false, err
	}
	return true, nil
}
