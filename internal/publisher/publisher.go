// Package publisher defines the interface for image publishing destinations.
// Publishers determine where built images are stored or uploaded after the build completes.
package publisher

import (
	"context"

	"github.com/travisbcotton/image-build/internal/container"
)

// Publisher is the interface that all image publishers must implement.
// A publisher takes a built container and publishes it to a specific destination.
//
// Implementations exist for:
//   - Local: Commit to local container storage (podman/buildah)
//   - SquashFS: Create a SquashFS filesystem image
//   - Registry: Push to OCI container registry
//   - S3: Upload to S3-compatible storage
//
// Multiple publishers can be used simultaneously to publish to multiple destinations.
type Publisher interface {
	// Publish takes a built container and publishes it to the destination.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeouts
	//   - c: The container to publish
	//   - name: Image name from configuration
	//   - tags: Image tags from configuration
	//   - labels: Map of image labels to apply
	//
	// Returns an error if publishing fails.
	Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error

	// Exists reports whether an image with the given name and tags is already
	// present at the publish destination. Implementations may return false
	// when the destination has no notion of "exists" (e.g., local storage).
	Exists(ctx context.Context, name string, tags []string) (bool, error)
}
