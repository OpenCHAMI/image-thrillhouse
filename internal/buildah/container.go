// Package buildah provides container operations using the Buildah library.
// It wraps Buildah's functionality for creating, mounting, and manipulating containers.
package buildah

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.podman.io/buildah"
	"go.podman.io/buildah/define"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/image/v5/docker"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
	"github.com/travisbcotton/image-build/internal/fetch"
)

// toSpecsMounts converts our internal BindMount type to the OCI runtime
// spec.Mount slice that buildah.RunOptions expects. Always uses "rbind" so
// nested mounts under the source are visible.
func toSpecsMounts(in []container.BindMount) []specs.Mount {
	if len(in) == 0 {
		return nil
	}
	out := make([]specs.Mount, 0, len(in))
	for _, m := range in {
		opts := []string{"rbind"}
		if m.Readonly {
			opts = append(opts, "ro")
		}
		out = append(out, specs.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        "bind",
			Options:     opts,
		})
	}
	return out
}

// defaultCaps are the Linux capabilities buildah needs to grant a process so
// that package installation works inside the container. They cover ownership
// changes (CHOWN/FSETID), DAC bypass, setuid/setgid for things like rpm
// helpers, the file-capability bit RPMs apply, and chroot for installroot
// workflows. The exact same set was duplicated in two call sites in this
// file; consolidate it here so adding or removing a capability is a one-line
// change.
var defaultCaps = []string{
	"CAP_CHOWN",
	"CAP_DAC_OVERRIDE",
	"CAP_FOWNER",
	"CAP_FSETID",
	"CAP_KILL",
	"CAP_NET_BIND_SERVICE",
	"CAP_SETFCAP",
	"CAP_SETGID",
	"CAP_SETPCAP",
	"CAP_SETUID",
	"CAP_SYS_CHROOT",
}

// Container wraps a Buildah builder and implements the container.Container interface.
// It provides methods for running commands, writing files, and committing images.
type Container struct {
	Name        string           // Container name
	fromScratch bool             // True if building from scratch
	mountPath   string           // Path where the container filesystem is mounted on the host
	Builder     *buildah.Builder // Underlying Buildah builder instance
	Store       storage.Store    // Container storage backend
}

// NewContainer creates a new container from the specified base image.
//
// Parameters:
//   - ctx: Context for cancellation
//   - name: Name for the container
//   - from: Base image to build from (e.g., "scratch", "ubuntu:latest", "registry.io/myimage:tag")
//   - tlsverify: Whether to skip TLS verification when pulling images
//
// The container is created, mounted, and ready to use. The caller is responsible
// for calling Delete() when done to clean up resources.
func NewContainer(ctx context.Context, name string, from string, tlsverify bool) (container.Container, error) {
	// get container store
	store, err := openStore()
	if err != nil {
		return nil, fmt.Errorf("Container Store: %w", err)
	}

	// create new builder
	builder, err := buildah.NewBuilder(ctx, store, buildah.BuilderOptions{
		FromImage: from,
		SystemContext: &types.SystemContext{
			DockerInsecureSkipTLSVerify: types.NewOptionalBool(!tlsverify),
		},
	})
	if err != nil {
		// Builder construction failed, so there's nothing buildah-side to
		// release — but the store handle we opened above is ours to close.
		if _, shutErr := store.Shutdown(false); shutErr != nil {
			slog.With("component", "container").Warn("store shutdown after builder failure", "error", shutErr)
		}
		return nil, fmt.Errorf("new builder: %w", err)
	}

	mountPath, err := builder.Mount("")
	if err != nil {
		// Roll back the partially-initialised container so we don't leak a
		// buildah-side container row and a held-open store handle on every
		// failed startup.
		log := slog.With("component", "container")
		if delErr := builder.Delete(); delErr != nil {
			log.Warn("builder delete after mount failure", "error", delErr)
		}
		if _, shutErr := store.Shutdown(false); shutErr != nil {
			log.Warn("store shutdown after mount failure", "error", shutErr)
		}
		return nil, fmt.Errorf("mount: %w", err)
	}

	return &Container{
		Name:        name,
		fromScratch: from == "scratch",
		mountPath:   mountPath,
		Builder:     builder,
		Store:       store,
	}, nil
}

// Run executes a command in the container or on the host.
//
// The execution mode depends on whether the container was created from scratch
// and the specified RunMode:
//
//   - RunModeHost: Runs the command on the host system (used for --installroot operations).
//     Only valid for scratch containers — host execution on a parent-image
//     container has no defined semantics (there's no mount path to target with
//     --root/--installroot) and was previously silently treated as
//     RunModeContainer, which surprised callers that wanted to bail.
//   - RunModeContainer: Runs the command inside the container using buildah run.
//   - RunModeAuto: Chooses RunModeContainer (the only mode usable for both
//     scratch and parent builds). Reserved for callers that don't care.
//
// For scratch builds in RunModeContainer, the container must have a shell and basic utilities.
// The command runs with elevated capabilities needed for package installation.
//
// Output is written to the provided OutputWriter, which is flushed after execution.
func (c *Container) Run(ctx context.Context, cmd []string, mode container.RunMode, out container.OutputWriter, opts ...container.RunOption) error {
	var runOpts container.RunOptions
	for _, opt := range opts {
		opt(&runOpts)
	}

	if mode == container.RunModeAuto {
		mode = container.RunModeContainer
	}

	if c.fromScratch {
		switch mode {
		default:
			return fmt.Errorf("run %v: unsupported run mode %d for scratch container", cmd, mode)
		case container.RunModeHost:
			// exec directly, used for dnf --installroot
			command := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			command.Stdout = out
			command.Stderr = out
			// Set environment variables for RPM/DNF to work in containers,
			// then layer any caller-supplied env on top so it can override.
			command.Env = append(os.Environ(),
				"LANG=C.UTF-8",
				"LC_ALL=C.UTF-8",
				"RPM_INSTALL_PREFIX="+c.MountPath(),
			)
			command.Env = append(command.Env, runOpts.Env...)
			err := command.Run()
			out.Flush(err)
			if err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
			return nil
		case container.RunModeContainer:
			// chroot into mountpath, rootfs must have a shell
			err := c.Builder.Run(cmd, buildah.RunOptions{
				Isolation:       c.GetIsolation(),
				Stdout:          out,
				Stderr:          out,
				AddCapabilities: defaultCaps,
				Env:             runOpts.Env,
				Mounts:          toSpecsMounts(runOpts.Mounts),
				// Force pipes (not a pty) so the container's stdout/stderr
				// reliably reach the writer above. Buildah's default
				// auto-detects the parent terminal, which can intercept
				// streaming output (notably ansible-playbook with -v).
				Terminal: buildah.WithoutTerminal,
			})
			out.Flush(err)
			if err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
			return nil
		}
	} else {
		if mode == container.RunModeHost {
			// Parent-image containers aren't mounted on the host, so a
			// "run on the host" mode has no rootPath to target. Silently
			// treating it as RunModeContainer (the previous behaviour) hid
			// caller bugs — surface the mismatch instead.
			return fmt.Errorf("run %v: RunModeHost is only valid for scratch containers", cmd)
		}
		err := c.Builder.Run(cmd, buildah.RunOptions{
			Isolation:       c.GetIsolation(),
			Stdout:          out,
			Stderr:          out,
			AddCapabilities: defaultCaps,
			Env:             runOpts.Env,
			Mounts:          toSpecsMounts(runOpts.Mounts),
			// See comment in the scratch branch above for why we pin this.
			Terminal: buildah.WithoutTerminal,
		})
		out.Flush(err)
		if err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

// RunScript writes a shell script to the container and executes it.
//
// The script is:
//  1. Written to a temporary file in /tmp
//  2. Made executable with chmod +x
//  3. Executed via /bin/sh so a missing shebang doesn't fail with "exec format error"
//  4. Removed after execution
//
// This is useful for running complex multi-line scripts without escaping issues.
//
// Optional RunOptions (e.g. WithEnv) are forwarded only to the actual script
// execution step — chmod and rm don't need the caller's env.
func (c *Container) RunScript(ctx context.Context, script string, out container.OutputWriter, opts ...container.RunOption) error {
	// Validate that script is not empty
	if strings.TrimSpace(script) == "" {
		return fmt.Errorf("script content is empty")
	}

	// write script to temp file in container
	tmpPath := fmt.Sprintf("/tmp/image-build-script-%d.sh", time.Now().UnixNano())

	if err := c.WriteFile(ctx, config.File{
		Path:    tmpPath,
		Content: script,
	}); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	// make executable and run
	if err := c.Run(ctx, []string{"chmod", "+x", tmpPath}, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("chmod script: %w", err)
	}

	// Invoke via /bin/sh rather than executing the script path directly. Direct
	// exec relies on the kernel finding a valid shebang or ELF header; a
	// shebang-less script fails with "exec format error" before any output is
	// produced. Routing through /bin/sh lets the shell parse the file.
	runErr := c.Run(ctx, []string{"/bin/sh", tmpPath}, container.RunModeContainer, out, opts...)
	if runErr != nil {
		slog.With("component", "container").Error("script failed", "path", tmpPath, "script", script)
	}

	// Cleanup always runs so we don't leave script files behind in the layer.
	// A cleanup failure is logged but does NOT override the script's exit
	// status — masking the real error here was the previous behaviour and it
	// hid genuine script failures behind "cleanup script" errors.
	if rmErr := c.Run(ctx, []string{"rm", tmpPath}, container.RunModeContainer, out); rmErr != nil {
		slog.With("component", "container").Warn("cleanup script (continuing)", "path", tmpPath, "error", rmErr)
	}

	if runErr != nil {
		return fmt.Errorf("exec script: %w", runErr)
	}
	return nil
}

// WriteFile writes a file into the container filesystem.
//
// The file content can be provided in three ways (checked in this order):
//  1. Content: Inline string content (useful for YAML multiline blocks)
//  2. Src: Path to a file on the host filesystem
//  3. URL: HTTP URL to fetch the content from
//
// The file is written through a temporary file on the host and then added
// to the container using Buildah's Add method.
func (c *Container) WriteFile(ctx context.Context, file config.File) error {
	if file.Path == "" {
		return fmt.Errorf("write file: path is required")
	}
	var content []byte
	var err error
	log := slog.With("component", "container")
	switch {
	case file.Content != "":
		// yaml scalar block or string
		content = []byte(file.Content)
	case file.Src != "":
		// local file on the host
		content, err = os.ReadFile(file.Src)
		if err != nil {
			return fmt.Errorf("read src %s: %w", file.Src, err)
		}
	case file.URL != "":
		// remote URL — ctx-aware fetch with timeout
		content, err = fetch.Get(ctx, file.URL)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("write file %s: one of content, src, or url is required", file.Path)
	}
	log.Debug("Write file", "path", file.Path)

	// Write to a host-side temp file and hand the path to buildah.Add.
	// We close explicitly (rather than just defer-close) so the bytes are
	// flushed before Add reads them; the only deferred work is removal.
	tmp, err := os.CreateTemp("", "image-build-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Set up AddAndCopyOptions with chmod if specified
	addOpts := buildah.AddAndCopyOptions{}
	if file.Mode != "" {
		addOpts.Chmod = file.Mode
	}

	if err := c.Builder.Add(file.Path, false, addOpts, tmp.Name()); err != nil {
		return fmt.Errorf("add file %s: %w", file.Path, err)
	}

	return nil
}

// CopyDirectory copies an entire directory from the host to the container.
// This is much faster than walking through individual files because it uses
// buildah's native directory copy functionality in a single operation.
//
// srcDir: source directory path on the host (must exist)
// destDir: destination directory path in the container
//
// The directory contents are copied recursively, preserving the directory structure.
func (c *Container) CopyDirectory(ctx context.Context, srcDir, destDir string) error {
	log := slog.With("component", "container")
	
	// Validate source directory exists
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("stat source directory %s: %w", srcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path %s is not a directory", srcDir)
	}

	log.Debug("Copying directory to container", "src", srcDir, "dest", destDir)

	// Use buildah's Add method which can handle entire directories efficiently.
	// The trailing slash ensures we copy the contents, not the directory itself.
	// For example: srcDir="/path/to/roles" with destDir="/tmp/ansible/roles"
	// will copy all contents of roles/ into /tmp/ansible/roles/
	addOpts := buildah.AddAndCopyOptions{
		// ContextDir tells buildah where to find the source
		ContextDir: srcDir,
	}

	// Add "." as source (relative to ContextDir) to copy all contents
	// destDir is where the contents will be placed in the container
	if err := c.Builder.Add(destDir, false, addOpts, "."); err != nil {
		return fmt.Errorf("copy directory %s to %s: %w", srcDir, destDir, err)
	}

	return nil
}

// Commit commits the container as an image to local storage.
// This is a convenience wrapper around CommitWithLabels that passes no labels.
func (c *Container) Commit(ctx context.Context, name, tag string) (string, error) {
	return c.CommitWithLabels(ctx, name, tag, nil)
}

// CommitWithLabels commits the container as an image with labels to local
// storage under a single tag. Equivalent to CommitWithLabelsTags with
// []string{tag}; preserved as a convenience for the common case.
func (c *Container) CommitWithLabels(ctx context.Context, name, tag string, labels map[string]string) (string, error) {
	return c.CommitWithLabelsTags(ctx, name, []string{tag}, labels)
}

// CommitWithLabelsTags commits the container once and applies every tag in a
// single buildah Commit call. Images are tagged as "localhost/<name>:<tag>"
// in the local container storage. Labels are applied to the image metadata
// before committing.
//
// Calling Commit in a loop (one tag per call) re-serializes the layer to
// storage every time; this method writes once and lets buildah register the
// additional tags as image references against the same blob.
//
// Returns the container ID on success.
func (c *Container) CommitWithLabelsTags(ctx context.Context, name string, tags []string, labels map[string]string) (string, error) {
	log := slog.With("component", "container")
	log.Debug("Commit Container", "ID", c.GetID(), "Name", c.GetName(), "name", name, "tags", tags, "labels", len(labels))

	if len(tags) == 0 {
		return "", fmt.Errorf("commit %s: at least one tag is required", name)
	}

	additional := make([]string, len(tags))
	for i, t := range tags {
		additional[i] = fmt.Sprintf("localhost/%s:%s", name, t)
	}

	// Add labels if provided
	for key, value := range labels {
		c.Builder.SetLabel(key, value)
	}

	options := buildah.CommitOptions{
		AdditionalTags: additional,
	}

	if _, _, _, err := c.Builder.Commit(ctx, nil, options); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return c.GetID(), nil
}

// Delete cleans up the container and releases all resources.
// This unmounts the container filesystem, deletes the container, and shuts down the storage.
// Should be called when the container is no longer needed.
//
// All three operations are best-effort: if one fails (typically Unmount, when
// something else is holding the mount), the remaining steps still run so we
// don't compound a partial leak. Failures are logged at WARN — silent failure
// here was masking storage leaks that only surfaced as disk pressure days later.
func (c *Container) Delete() {
	log := slog.With("component", "container")
	log.Debug("Deleting Container", "ID", c.GetID(), "Name", c.GetName())
	if err := c.Builder.Unmount(); err != nil {
		log.Warn("unmount container", "id", c.GetID(), "error", err)
	}
	if err := c.Builder.Delete(); err != nil {
		log.Warn("delete container", "id", c.GetID(), "error", err)
	}
	if _, err := c.Store.Shutdown(false); err != nil {
		log.Warn("shutdown store", "error", err)
	}
}

// MountPath returns the host filesystem path where the container is mounted.
// This path can be used to directly manipulate files in the container filesystem.
func (c *Container) MountPath() string {
	return c.mountPath
}

// GetID returns the unique container ID.
func (c *Container) GetID() string {

	return c.Builder.ContainerID
}

// GetParent returns the source ("from") image used to create this container.
func (c *Container) GetParent() string {

	if c.fromScratch {
		return "scratch"
	}
	return c.Builder.FromImage
}

// GetName returns the container name.
func (c *Container) GetName() string {

	return c.Builder.Container
}

// GetIsolation returns the isolation mode to use for running commands.
// The mode can be controlled via the BUILDAH_ISOLATION environment variable:
//   - "chroot": Use chroot isolation
//   - "rootless": Use OCI rootless isolation
//   - "oci": Use standard OCI runtime
//   - default: Let Buildah choose the appropriate mode
func (c *Container) GetIsolation() define.Isolation {
	if iso := os.Getenv("BUILDAH_ISOLATION"); iso != "" {
		switch iso {
		case "chroot":
			return define.IsolationChroot
		case "rootless":
			return define.IsolationOCIRootless
		case "oci":
			return define.IsolationOCI
		}
	}
	return define.IsolationDefault
}

// CommitToRegistry commits the container directly to a remote registry.
//
// Parameters:
//   - ctx: Context for cancellation
//   - ref: Registry reference (e.g., "registry.io/repo/image:tag")
//   - tlsVerify: Whether to verify TLS certificates when pushing
//
// This bypasses local storage and pushes directly to the registry,
// which can be more efficient for CI/CD pipelines.
func (c *Container) CommitToRegistry(ctx context.Context, ref string, tlsVerify bool) error {
	imageRef, err := docker.ParseReference("//" + ref)
	if err != nil {
		return fmt.Errorf("parse registry ref: %w", err)
	}

	_, _, _, err = c.Builder.Commit(ctx, imageRef, buildah.CommitOptions{
		SystemContext: &types.SystemContext{
			DockerInsecureSkipTLSVerify: types.NewOptionalBool(!tlsVerify),
		},
	})
	if err != nil {
		return fmt.Errorf("commit to registry: %w", err)
	}
	return nil
}
