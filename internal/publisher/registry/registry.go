// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package registry provides a publisher that pushes images to OCI container registries.
// It supports any Docker-compatible registry (Docker Hub, Harbor, Quay, etc.).
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/docker/distribution/registry/api/errcode"
	v2 "github.com/docker/distribution/registry/api/v2"
	"go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/docker"
	"go.podman.io/image/v5/signature"
	"go.podman.io/image/v5/types"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
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
// Labels are applied to the container by the builder (Container.SetLabels)
// before any publisher runs, so this method pushes what's already in the
// container config and ignores the labels parameter.
func (r *RegistryPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	log := slog.With("component", "publisher.registry")
	for _, t := range tags {
		ref := fmt.Sprintf("%s/%s:%s", r.url, name, t)
		log.Info("pushing to registry", "ref", ref)
		if err := c.CommitToRegistry(ctx, ref, r.tlsVerify); err != nil {
			return fmt.Errorf("push %s: %w", ref, err)
		}
		log.Info("pushed to registry", "ref", ref)
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
	sys := systemContext(r.tlsVerify)

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

// systemContext builds the containers/image SystemContext used for every
// registry interaction: TLS verification per the caller's setting and the auth
// file from REGISTRY_AUTH_FILE (falling back to the containers/image default
// search when unset). Kept in one place so pushes, existence probes, and copies
// all authenticate identically.
func systemContext(tlsVerify bool) *types.SystemContext {
	return &types.SystemContext{
		DockerInsecureSkipTLSVerify: types.NewOptionalBool(!tlsVerify),
		AuthFilePath:                os.Getenv("REGISTRY_AUTH_FILE"),
	}
}

// RefExists reports whether a specific fully-qualified reference (e.g.
// "registry.io/repo/name:release-0.0.1") resolves in its registry. Unlike the
// RegistryPublisher.Exists method — which probes a name + tag list against a
// publisher's configured URL — this takes a complete ref, so promote can gate a
// retag on whether the destination tag already exists.
//
// Failure handling matches Exists: a genuine "not found" is (false, nil); any
// auth/network/transport error surfaces as (false, err) so callers fail loud.
func RefExists(ctx context.Context, ref string, tlsVerify bool) (bool, error) {
	return manifestExists(ctx, systemContext(tlsVerify), ref)
}

// Copy performs a registry-to-registry copy of srcRef to dstRef. For the retag
// case — same repository, different tag — the destination blobs already exist,
// so copy.Image detects them and writes only the new manifest/tag: no blob
// re-upload, effectively a server-side alias of the exact tested bytes.
//
// ImageListSelection is CopyAllImages so that a multi-arch image index at the
// source is copied whole; for a single-arch source it copies the one image.
// Both endpoints share one SystemContext because retag stays within a single
// registry's auth/TLS regime.
func Copy(ctx context.Context, srcRef, dstRef string, tlsVerify bool) error {
	src, err := docker.ParseReference("//" + srcRef)
	if err != nil {
		return fmt.Errorf("parse source ref %q: %w", srcRef, err)
	}
	dst, err := docker.ParseReference("//" + dstRef)
	if err != nil {
		return fmt.Errorf("parse dest ref %q: %w", dstRef, err)
	}

	policy, err := signature.DefaultPolicy(nil)
	if err != nil {
		return fmt.Errorf("load default signature policy: %w", err)
	}
	policyCtx, err := signature.NewPolicyContext(policy)
	if err != nil {
		return fmt.Errorf("new policy context: %w", err)
	}
	defer func() {
		if err := policyCtx.Destroy(); err != nil {
			slog.With("component", "publisher.registry").Warn("destroy policy context", "error", err)
		}
	}()

	sys := systemContext(tlsVerify)
	if _, err := copy.Image(ctx, policyCtx, dst, src, &copy.Options{
		SourceCtx:          sys,
		DestinationCtx:     sys,
		ImageListSelection: copy.CopyAllImages,
	}); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", srcRef, dstRef, err)
	}
	return nil
}

// manifestExists returns true if the manifest for ref is reachable in the
// registry. Pulled into a helper because docker.NewReference / NewImageSource
// / GetManifest is the canonical "does this exist" probe in containers/image
// and we want one consistent failure-handling spot.
//
// A "manifest unknown" / 404 response is treated as (false, nil) so that
// --skip-if-exists works on the *first* build of a new image — without this,
// the very first build always failed at the existence probe because the
// target obviously didn't exist yet. Any other error (auth, DNS, TLS, 5xx)
// surfaces as (false, err) so the caller can fail loudly instead of silently
// rebuilding on infra outages.
func manifestExists(ctx context.Context, sys *types.SystemContext, ref string) (bool, error) {
	imageRef, err := docker.ParseReference("//" + ref)
	if err != nil {
		return false, fmt.Errorf("parse ref: %w", err)
	}

	src, err := imageRef.NewImageSource(ctx, sys)
	if err != nil {
		if isManifestUnknown(err) {
			return false, nil
		}
		return false, err
	}
	defer src.Close()

	if _, _, err := src.GetManifest(ctx, nil); err != nil {
		if isManifestUnknown(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// isManifestUnknown reports whether err is the registry telling us "this
// manifest doesn't exist", as opposed to an auth/network/transport failure.
// Mirrors the logic in go.podman.io/image's unexported isManifestUnknownError:
//
//   - the spec-mandated errcode.ManifestUnknown response,
//   - the registry.redhat.io / Harbor pattern of errcode.Unknown + "Not Found",
//   - and a final substring fallback for OCI-distribution-spec registries that
//     return 404 without an errcode body.
//
// The substring check is intentionally loose ("not found" / "404") because we
// can't import the upstream's unexported unexpectedHTTPResponseError type.
func isManifestUnknown(err error) bool {
	var ec errcode.ErrorCoder
	if errors.As(err, &ec) && ec.ErrorCode() == v2.ErrorCodeManifestUnknown {
		return true
	}
	var e errcode.Error
	if errors.As(err, &e) && e.ErrorCode() == errcode.ErrorCodeUnknown {
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "not found") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "manifest unknown") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "status 404")
}
