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
	"go.podman.io/image/v5/docker"
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
	sys := &types.SystemContext{
		DockerInsecureSkipTLSVerify: types.NewOptionalBool(!r.tlsVerify),
		AuthFilePath:                os.Getenv("REGISTRY_AUTH_FILE"),
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
