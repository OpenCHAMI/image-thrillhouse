// Package container defines interfaces for container operations.
// It provides abstractions for running commands, writing files, and managing containers.
package container

import (
	"context"
	"io"

	"github.com/containers/buildah/define"
	"github.com/travisbcotton/image-build/internal/config"
)

// RunMode specifies how a command should be executed.
type RunMode int

const (
	RunModeAuto      RunMode = iota // Auto-detect the appropriate mode
	RunModeHost                     // Run on the host system (e.g., for --installroot)
	RunModeContainer                // Run inside the container using buildah run
)

// Container provides an abstraction for container operations.
// It encapsulates buildah functionality for creating, modifying, and committing containers.
type Container interface {
	// Run executes a command in the container or on the host
	Run(ctx context.Context, cmd []string, mode RunMode, out OutputWriter) error
	
	// RunScript writes a script to the container and executes it
	RunScript(ctx context.Context, script string, out OutputWriter) error
	
	// WriteFile writes a file into the container filesystem. The context is
	// used for any network fetches (when File.URL is set) and for cancellation
	// of the underlying buildah Add operation.
	WriteFile(ctx context.Context, file config.File) error
	
	// Commit commits the container to local storage with the given name and tag
	Commit(ctx context.Context, name, tag string) (string, error)

	// CommitWithLabels commits the container with OCI labels under a single tag.
	CommitWithLabels(ctx context.Context, name, tag string, labels map[string]string) (string, error)

	// CommitWithLabelsTags commits the container once and applies all tags
	// at the same time. This is significantly cheaper than calling
	// CommitWithLabels in a loop, which writes the layer to storage per tag.
	CommitWithLabelsTags(ctx context.Context, name string, tags []string, labels map[string]string) (string, error)
	
	// GetID returns the container ID
	GetID() string

	// GetParent returns the source/from image for the container.
	GetParent() string

	// GetName returns the container name
	GetName() string
	
	// Delete removes the container and frees resources
	Delete()
	
	// MountPath returns the filesystem path where the container is mounted
	MountPath() string
	
	// GetIsolation returns the isolation mode for running commands
	GetIsolation() define.Isolation
	
	// CommitToRegistry commits the container directly to a remote registry
	CommitToRegistry(ctx context.Context, ref string, tlsVerify bool) error
}

// OutputWriter is an io.Writer that can buffer output and flush it with error context.
// Implementations can parse and format output from package managers or commands.
type OutputWriter interface {
	io.Writer
	// Flush processes and logs the buffered output.
	// The err parameter indicates if the command that produced the output failed.
	Flush(err error)
}
