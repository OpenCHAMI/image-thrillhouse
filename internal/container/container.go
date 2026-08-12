// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package container defines interfaces for container operations.
// It provides abstractions for running commands, writing files, and managing containers.
package container

import (
	"context"
	"io"

	"github.com/openchami/image-thrillhouse/internal/config"
	"go.podman.io/buildah/define"
)

// RunMode specifies how a command should be executed.
type RunMode int

// Numbering starts at 1 so the zero value is not a usable mode: callers must
// name the one they want. There used to be a RunModeAuto at zero that only ever
// meant "same as RunModeContainer" and which nothing passed, so it added a
// silent fallback without adding a choice.
const (
	RunModeHost      RunMode = iota + 1 // Run on the host system (e.g., for --installroot)
	RunModeContainer                    // Run inside the container using buildah run
)

// RunOptions holds optional behavior for Run/RunScript. Callers should build
// it with the With* option functions rather than constructing the struct
// directly, so future fields don't break existing call sites.
type RunOptions struct {
	// Env is a list of "KEY=VALUE" pairs added to the command's environment.
	// In RunModeContainer the values are passed via buildah.RunOptions.Env;
	// in RunModeHost they are appended to the host process's environment.
	Env []string

	// Mounts is a list of host→container bind mounts to set up for the
	// duration of the command. Only honored in RunModeContainer; ignored on
	// the host runner.
	Mounts []BindMount
}

// BindMount describes a host→container bind mount that lives only for the
// duration of one Run invocation. Use it when you want to expose host files
// to the command without committing them into the image layer.
type BindMount struct {
	Source      string // absolute host path (file or directory)
	Destination string // absolute container path
	Readonly    bool   // if true, mount with the "ro" flag
}

// RunOption is a functional option for Run/RunScript.
type RunOption func(*RunOptions)

// WithEnv adds one or more "KEY=VALUE" environment variables to the command.
// Multiple WithEnv calls are additive. Used by callers that need to set a
// per-invocation env var (e.g. ANSIBLE_CONFIG) without going through a shell
// wrapper.
func WithEnv(kv ...string) RunOption {
	return func(o *RunOptions) {
		o.Env = append(o.Env, kv...)
	}
}

// WithBindMount adds a host→container bind mount that lives only for this
// invocation. Both paths should be absolute. Use this to expose payloads (a
// playbook, a roles directory, a generated config dir) to a single Run
// without copying them into the image layer.
func WithBindMount(source, destination string, readonly bool) RunOption {
	return func(o *RunOptions) {
		o.Mounts = append(o.Mounts, BindMount{
			Source:      source,
			Destination: destination,
			Readonly:    readonly,
		})
	}
}

// CopyDirectoryOptions configures a CopyDirectory call. All fields are
// optional and map onto buildah.AddAndCopyOptions:
//
//   - Chmod: applied uniformly to files and directories. Empty preserves
//     host modes (buildah's natural default).
//   - Chown: "uid:gid" or "user:group". Empty resets to 0:0 unless
//     PreserveOwnership is set.
//   - PreserveOwnership: keep host ownership instead of buildah's 0:0
//     default. Ignored when Chown is non-empty.
//   - Excludes: .containerignore-style patterns evaluated by the same
//     matcher buildah uses, so the tag hasher and the copy step see the
//     same file set.
//   - ContentsOnly: true copies srcDir/. into destDir (cp -a semantics).
//     false copies srcDir as a subdirectory under destDir (Dockerfile
//     COPY-of-directory semantics).
type CopyDirectoryOptions struct {
	Chmod             string
	Chown             string
	PreserveOwnership bool
	Excludes          []string
	ContentsOnly      bool
}

// Container abstracts the buildah operations a build performs, so the builder
// can be tested without a container runtime.
type Container interface {
	Run(ctx context.Context, cmd []string, mode RunMode, out OutputWriter, opts ...RunOption) error

	RunScript(ctx context.Context, script string, out OutputWriter, opts ...RunOption) error

	// WriteFile resolves File.Content/Src/URL and writes it into the container.
	// ctx covers the network fetch when URL is set.
	WriteFile(ctx context.Context, file config.File) error

	// CopyDirectory copies a host directory into the container in one buildah
	// operation. See CopyDirectoryOptions for the per-call knobs.
	CopyDirectory(ctx context.Context, srcDir, destDir string, opts CopyDirectoryOptions) error

	// SetLabels applies OCI labels to the image config so that every subsequent
	// commit — local or direct-to-registry — carries them. The builder calls
	// this once before the publish loop; publishers must not rely on each other
	// to have applied labels.
	SetLabels(labels map[string]string)

	// CommitWithLabelsTags commits the container once and applies all tags at
	// the same time, which is significantly cheaper than one commit per tag —
	// each commit re-serializes the layer to storage.
	CommitWithLabelsTags(ctx context.Context, name string, tags []string, labels map[string]string) (string, error)

	GetID() string

	// GetParent returns the source/from image for the container.
	GetParent() string

	// PulledParentID returns the ID of the base image if and only if
	// creating this container pulled it into local storage. It is empty for
	// scratch builds and whenever the base image was already present, so a
	// caller pruning on this value never removes an image that predates the
	// build.
	PulledParentID() string

	GetName() string

	// Delete unmounts and removes the container and releases the store handle.
	Delete()

	// MountPath is the host path the container filesystem is mounted at.
	MountPath() string

	GetIsolation() define.Isolation

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
