// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package promote re-tags already-built, already-tested artifacts under
// human-readable release tags without rebuilding them.
//
// A release tag must point at the exact bytes that were tested, so promotion
// is always a copy of published content — never a build.
//
// The one operation is RetagRegistry: a manifest copy of one content-tagged
// image to a release tag in the same repository. The blobs already exist, so it
// writes only a new tag — the exact tested bytes under a human-readable name.
// Multi-arch is just this applied once per arch (each arch has its own repo,
// e.g. <image>-x86_64 / <image>-aarch64), driven by the caller.
package promote

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher/registry"
)

// RegistrySource identifies a tested artifact already published to an OCI
// registry under its content-addressed tag — the thing a promotion copies FROM.
type RegistrySource struct {
	URL       string // registry URL prefix (e.g. "registry.io/openchami")
	Name      string // image name (meta.name)
	Tag       string // content-addressed tag to promote from
	TLSVerify bool
}

// RefWithTag returns the fully-qualified reference for this source's registry
// and name under an arbitrary tag, matching the format the registry publisher
// pushes to: <url>/<name>:<tag>. Keeping this construction in one place is what
// stops the promote path and the publish path from drifting on how a reference
// is spelled.
func (s RegistrySource) RefWithTag(tag string) string {
	return fmt.Sprintf("%s/%s:%s", s.URL, s.Name, tag)
}

// Ref returns the source reference under its content tag.
func (s RegistrySource) Ref() string {
	return s.RefWithTag(s.Tag)
}

// FindPublish returns the first publish block of the given type, or an error if
// none is present. A layer selected for promotion that lacks the requested
// source/target block is a hard failure, not a silent skip — a mis-tagged
// manifest should fail loud.
func FindPublish(publishes []config.Publish, typ string) (config.Publish, error) {
	for _, p := range publishes {
		if p.Type == typ {
			return p, nil
		}
	}
	return config.Publish{}, fmt.Errorf("layer config has no %q publish block", typ)
}

// checkDestination fails when dstRef already exists and force is false, so a
// promotion never silently overwrites a release tag. With force set it skips the
// probe entirely. Shared by the single-image and image-index paths.
func checkDestination(ctx context.Context, dstRef string, tlsVerify, force bool) error {
	if force {
		return nil
	}
	exists, err := registry.RefExists(ctx, dstRef, tlsVerify)
	if err != nil {
		return fmt.Errorf("check destination %s: %w", dstRef, err)
	}
	if exists {
		return fmt.Errorf("release tag %s already exists (use --force to overwrite)", dstRef)
	}
	return nil
}

// RetagRegistry promotes a registry artifact to a release tag in the same
// repository (OCI -> OCI). It is a manifest copy from the content tag to the
// release tag: the blobs already exist at the destination, so only a new tag is
// written — the exact tested bytes under a human-readable name, never a rebuild.
func RetagRegistry(ctx context.Context, src RegistrySource, release string, force bool) error {
	srcRef := src.Ref()
	dstRef := src.RefWithTag(release)
	log := slog.With("component", "promote", "source", srcRef, "dest", dstRef)

	if err := checkDestination(ctx, dstRef, src.TLSVerify, force); err != nil {
		return err
	}

	log.Info("retagging registry image")
	if err := registry.Copy(ctx, srcRef, dstRef, src.TLSVerify); err != nil {
		return err
	}
	log.Info("retagged registry image")
	return nil
}
