// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openchami/image-thrillhouse/internal/config"
	"github.com/openchami/image-thrillhouse/internal/container"
	"github.com/openchami/image-thrillhouse/internal/fetch"
)

// artifactsBaseDir is the standard location for versioned tar artifacts.
const artifactsBaseDir = "/opt/artifacts"

// versionRegex matches semantic-version-like directory names used to derive
// artifact component and version from a source path or URL.
var versionRegex = regexp.MustCompile(`^v?\d+(\.\d+){1,3}[a-zA-Z0-9_-]*$`)

// injectArtifacts bakes container images and injects other_files (tar)
// artifacts into the OS image. It runs after package installation and before
// custom commands so later commands can reference the injected content. It
// also remounts the container when host-side writes (skopeo loading into
// containers-storage) may have bypassed the merged overlay view.
func (b *Builder) injectArtifacts(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder.artifacts")

	hasAny := len(b.cfg.Layer.ContainerImages) > 0 ||
		len(b.cfg.Layer.OtherFiles) > 0
	if !hasAny {
		return nil
	}

	log.Info("starting artifact injection",
		"container_images", len(b.cfg.Layer.ContainerImages),
		"other_files", len(b.cfg.Layer.OtherFiles))
	start := time.Now()

	if err := b.injectContainerImages(ctx, c); err != nil {
		return fmt.Errorf("inject container images: %w", err)
	}
	if err := b.injectOtherFiles(ctx, c); err != nil {
		return fmt.Errorf("inject other files: %w", err)
	}

	// Remount so host-side writes to the upperdir (skopeo containers-storage
	// loads) become visible through the merged mount point before later
	// commands and publishers inspect the filesystem.
	if err := b.maybeRemount(c); err != nil {
		return fmt.Errorf("remount after artifact injection: %w", err)
	}

	log.Info("artifact injection complete", "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

// injectContainerImages pulls each configured image, saves it as a docker-archive
// tar, copies the tar into the OS image, and optionally loads it into the OS
// image's own containers storage so `podman images` shows it after boot.
func (b *Builder) injectContainerImages(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder.artifacts.container")

	for _, img := range b.cfg.Layer.ContainerImages {
		if err := b.injectContainerImage(ctx, c, img); err != nil {
			return err
		}
	}

	log.Debug("container image injection complete", "count", len(b.cfg.Layer.ContainerImages))
	return nil
}

func (b *Builder) injectContainerImage(ctx context.Context, c container.Container, img config.ContainerImage) error {
	log := slog.With("component", "builder.artifacts.container")

	imageRef := img.Image
	if imageRef == "" {
		return fmt.Errorf("container image entry missing image")
	}

	dest := img.Dest
	if dest == "" {
		dest = "/var/lib/containers/images"
	}
	load := true
	if img.Load != nil {
		load = *img.Load
	}
	tlsVerify := true
	if img.TLSVerify != nil {
		tlsVerify = *img.TLSVerify
	}

	log.Info("injecting container image", "image", imageRef, "dest", dest, "load", load, "tls_verify", tlsVerify)

	sourceRef, bareRef := normalizeImageRef(imageRef)

	// Save the pulled image to a docker-archive tar on the build host.
	hostTarFile, err := os.CreateTemp("", "image-thrillhouse-container-*.tar")
	if err != nil {
		return fmt.Errorf("create temp tar: %w", err)
	}
	hostTarFile.Close()
	hostTarPath := hostTarFile.Name()
	// os.CreateTemp creates mode 0600; make it readable inside the buildah container.
	_ = os.Chmod(hostTarPath, 0644)

	tarRef := fmt.Sprintf("docker-archive:%s:%s", hostTarPath, bareRef)
	if err := b.runHostCmd(ctx, "builder.artifacts.container", "skopeo", skopeoCopyArgs(sourceRef, tarRef, tlsVerify)...); err != nil {
		os.Remove(hostTarPath)
		return fmt.Errorf("pull/save container image %s: %w", imageRef, err)
	}

	safeName := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(bareRef)
	tarName := safeName + ".tar"

	// Copy the saved tar into the OS image.
	containerTarPath, err := b.copyHostFileIntoContainer(ctx, c, hostTarPath, dest, tarName)
	if err != nil {
		os.Remove(hostTarPath)
		return fmt.Errorf("copy container image tar into container: %w", err)
	}

	if load {
		// Load the image directly into the OS image's containers-storage by
		// writing to the backing upperdir. This avoids overlay-on-overlay
		// whiteout issues that occur when writing through the merged view.
		graphroot, runroot, err := b.containerStorageRoots(c)
		if err != nil {
			os.Remove(hostTarPath)
			return err
		}
		defer os.RemoveAll(runroot)

		if err := os.MkdirAll(graphroot, 0755); err != nil {
			os.Remove(hostTarPath)
			return fmt.Errorf("create containers graphroot %s: %w", graphroot, err)
		}

		storageRef := fmt.Sprintf("containers-storage:[overlay@%s+%s]%s", graphroot, runroot, bareRef)

		if err := b.runHostCmd(ctx, "builder.artifacts.container", "skopeo", "copy", tarRef, storageRef); err != nil {
			os.Remove(hostTarPath)
			return fmt.Errorf("load container image %s into OS storage: %w", imageRef, err)
		}
		if err := b.runHostCmd(ctx, "builder.artifacts.container", "skopeo", "inspect", storageRef); err != nil {
			os.Remove(hostTarPath)
			return fmt.Errorf("verify container image %s in OS storage: %w", imageRef, err)
		}

		if err := container.RunCmd(ctx, c, "builder.artifacts.container", []string{"rm", "-f", containerTarPath}, container.RunModeContainer); err != nil {
			log.Warn("failed to remove container image tar from OS image after load", "path", containerTarPath, "error", err)
		}
	}

	os.Remove(hostTarPath)
	log.Info("successfully injected container image", "image", imageRef, "path", containerTarPath)
	return nil
}

// injectOtherFiles copies each configured other_files tar artifact into the
// OS image, derives a versioned destination when none is given, and
// auto-extracts based on the file's MIME type.
func (b *Builder) injectOtherFiles(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder.artifacts.other")

	for _, of := range b.cfg.Layer.OtherFiles {
		if err := b.injectTarFile(ctx, c, of.ToTarFile()); err != nil {
			return err
		}
	}

	log.Debug("other file injection complete", "count", len(b.cfg.Layer.OtherFiles))
	return nil
}

func (b *Builder) injectTarFile(ctx context.Context, c container.Container, tf config.TarFile) error {
	log := slog.With("component", "builder.artifacts.other")

	if tf.Src == "" {
		return fmt.Errorf("tar file missing src")
	}
	extract := true
	if tf.Extract != nil {
		extract = *tf.Extract
	}
	tlsVerify := true
	if tf.TLSVerify != nil {
		tlsVerify = *tf.TLSVerify
	}

	localSrc, basename, cleanup, err := b.resolveArtifactSource(ctx, tf.Src, tlsVerify)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	component, version := parseComponentVersion(tf.Src)
	dest := tf.Dest
	if dest == "" {
		dest = filepath.Join(artifactsBaseDir, component, version)
	}

	log.Info("injecting tar file", "src", tf.Src, "dest", dest, "component", component, "version", version, "extract", extract, "tls_verify", tlsVerify)

	containerArchivePath, err := b.copyHostFileIntoContainer(ctx, c, localSrc, dest, basename)
	if err != nil {
		return fmt.Errorf("copy tar file to container: %w", err)
	}

	if extract {
		if err := b.extractArchive(ctx, c, dest, basename, localSrc, "builder.artifacts.other"); err != nil {
			return err
		}
		if err := container.RunCmd(ctx, c, "builder.artifacts.other", []string{"rm", "-f", containerArchivePath}, container.RunModeContainer); err != nil {
			log.Warn("failed to remove tar archive after extraction", "path", containerArchivePath, "error", err)
		}
	}

	log.Info("successfully injected tar file", "src", tf.Src, "dest", dest)
	return nil
}

// resolveArtifactSource turns a tar src into a local path. If src is an
// HTTP(S) URL, it is downloaded to a temp file and the caller owns cleanup via
// the returned function.
func (b *Builder) resolveArtifactSource(ctx context.Context, src string, tlsVerify bool) (localPath, basename string, cleanup func(), err error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		tmpFile, err := os.CreateTemp("", "image-thrillhouse-artifact-*")
		if err != nil {
			return "", "", nil, fmt.Errorf("create temp file for artifact: %w", err)
		}
		tmpFile.Close()
		localPath = tmpFile.Name()
		// os.CreateTemp creates mode 0600; make it readable inside the buildah container.
		_ = os.Chmod(localPath, 0644)
		cleanup = func() { os.Remove(localPath) }

		if err := b.downloadURL(ctx, src, localPath, tlsVerify); err != nil {
			cleanup()
			return "", "", nil, err
		}

		// Derive the basename from the URL path, stripping query/fragment.
		urlPath := strings.Split(src, "?")[0]
		urlPath = strings.Split(urlPath, "#")[0]
		basename = filepath.Base(urlPath)
		if basename == "" || basename == "/" {
			basename = filepath.Base(localPath)
		}
		return localPath, basename, cleanup, nil
	}

	if _, err := os.Stat(src); err != nil {
		return "", "", nil, fmt.Errorf("artifact src %s not found: %w", src, err)
	}
	return src, filepath.Base(src), nil, nil
}

// downloadURL fetches a URL to a local file, streaming so large artifacts are
// not held in memory. When tlsVerify is false, TLS certificate verification is
// skipped for self-signed HTTPS endpoints.
func (b *Builder) downloadURL(ctx context.Context, url, dst string, tlsVerify bool) error {
	rc, err := fetch.GetStreamTLS(ctx, url, tlsVerify)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer rc.Close()

	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}

// copyHostFileIntoContainer copies a host file into the OS image using a
// bind-mount and cp inside the container, avoiding loading large files into
// memory.
func (b *Builder) copyHostFileIntoContainer(ctx context.Context, c container.Container, hostPath, containerDir, containerFileName string) (string, error) {
	if err := container.RunCmd(ctx, c, "builder.artifacts", []string{"mkdir", "-p", containerDir}, container.RunModeContainer); err != nil {
		return "", fmt.Errorf("mkdir %s in container: %w", containerDir, err)
	}

	containerSrc := filepath.Join("/tmp", "image-thrillhouse-artifact-"+containerFileName)
	dest := filepath.Join(containerDir, containerFileName)

	if err := container.RunCmd(ctx, c, "builder.artifacts",
		[]string{"cp", containerSrc, dest},
		container.RunModeContainer,
		container.WithBindMount(hostPath, containerSrc, true)); err != nil {
		return "", fmt.Errorf("copy %s -> %s: %w", hostPath, dest, err)
	}

	return dest, nil
}

// extractArchive detects the archive type and extracts it in the container.
func (b *Builder) extractArchive(ctx context.Context, c container.Container, destDir, basename, hostPath, component string) error {
	mimeType, err := b.detectFileType(hostPath)
	if err != nil {
		return fmt.Errorf("detect file type for %s: %w", basename, err)
	}

	log := slog.With("component", component)

	switch mimeType {
	case "application/x-tar", "application/gzip", "application/x-gzip",
		"application/x-bzip2", "application/x-xz", "application/x-compress":
		script := fmt.Sprintf("cd %q && tar -xf %q", destDir, basename)
		return container.RunCmd(ctx, c, component, []string{"/bin/sh", "-c", script}, container.RunModeContainer)
	case "application/zip":
		script := fmt.Sprintf("cd %q && unzip -o %q", destDir, basename)
		return container.RunCmd(ctx, c, component, []string{"/bin/sh", "-c", script}, container.RunModeContainer)
	case "application/x-rpm", "application/x-redhat-package-manager":
		log.Warn("artifact is an RPM package, copying without extraction", "basename", basename, "mime", mimeType)
	case "application/vnd.debian.binary-package":
		log.Warn("artifact is a DEB package, copying without extraction", "basename", basename, "mime", mimeType)
	default:
		log.Warn("unrecognized archive type, copying without extraction", "basename", basename, "mime", mimeType)
	}

	return nil
}

// detectFileType returns the MIME type of a local file using the `file` command.
func (b *Builder) detectFileType(path string) (string, error) {
	out, err := exec.Command("file", "--brief", "--mime-type", path).Output()
	if err != nil {
		return "", fmt.Errorf("file command failed for %s: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// containerStorageRoots returns the graphroot and a per-build runroot for the
// OS image's containers-storage. It writes directly to the upperdir when the
// buildah mount point is an overlay so that skopeo can create whiteout files
// without hitting overlay-on-overlay errors.
func (b *Builder) containerStorageRoots(c container.Container) (graphroot, runroot string, err error) {
	mountPath := c.MountPath()
	upperdir := mountPath
	if strings.HasSuffix(mountPath, "/merged") {
		upperdir = strings.TrimSuffix(mountPath, "/merged") + "/diff"
	}

	graphroot = filepath.Join(upperdir, "var/lib/containers/storage")
	runroot, err = os.MkdirTemp("", "image-thrillhouse-containers-run-*")
	if err != nil {
		return "", "", fmt.Errorf("create containers runroot: %w", err)
	}
	return graphroot, runroot, nil
}

// maybeRemount unmounts and remounts the container if the concrete container
// implementation supports it. This makes host-side writes to the upperdir
// (e.g. skopeo loading a container image) visible through the merged mount.
func (b *Builder) maybeRemount(c container.Container) error {
	r, ok := c.(interface{ Remount() error })
	if !ok {
		return nil
	}
	slog.With("component", "builder.artifacts").Debug("remounting container to reflect artifact writes")
	return r.Remount()
}

// runHostCmd runs a command on the build host with logging and context
// cancellation. Output is captured and flushed through the standard container
// log writer for consistent formatting.
func (b *Builder) runHostCmd(ctx context.Context, component string, name string, args ...string) error {
	log := slog.With("component", component)
	log.Debug("running host command", "cmd", append([]string{name}, args...))

	cmd := exec.CommandContext(ctx, name, args...)
	out := container.NewBufLogWriter(component, "stdout")
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	out.Flush(err)
	if err != nil {
		return fmt.Errorf("run %s %v: %w", name, args, err)
	}
	return nil
}

// parseComponentVersion extracts a component and version from an artifact path
// or URL. It mirrors the Python image-builder's _parse_component_version logic.
func parseComponentVersion(src string) (component, version string) {
	path := strings.Split(src, "?")[0]
	path = strings.Split(path, "#")[0]

	var segments []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) == 0 {
		return "unknown", "unknown"
	}

	dirSegments := segments[:len(segments)-1]

	for i := len(dirSegments) - 1; i >= 0; i-- {
		if versionRegex.MatchString(dirSegments[i]) {
			version = dirSegments[i]
			if i > 0 {
				component = dirSegments[i-1]
			} else {
				component = "unknown"
			}
			return
		}
	}

	switch len(dirSegments) {
	case 0:
		return "unknown", "unknown"
	case 1:
		return dirSegments[0], "unknown"
	default:
		return dirSegments[len(dirSegments)-2], dirSegments[len(dirSegments)-1]
	}
}

// normalizeImageRef returns a skopeo source reference and a bare image
// reference suitable for docker-archive and containers-storage destinations.
func normalizeImageRef(ref string) (sourceRef, bareRef string) {
	bareRef = ref
	if strings.HasPrefix(ref, "docker://") {
		return ref, strings.TrimPrefix(ref, "docker://")
	}
	return "docker://" + ref, ref
}

// skopeoCopyArgs builds the argument list for `skopeo copy`. It adds
// --src-tls-verify=false when the caller requests an insecure source.
func skopeoCopyArgs(sourceRef, destRef string, tlsVerify bool) []string {
	args := []string{"copy"}
	if !tlsVerify {
		args = append(args, "--src-tls-verify=false")
	}
	return append(args, sourceRef, destRef)
}
