// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package promote re-tags already-built, already-tested artifacts under
// human-readable release tags without rebuilding them.
//
// A release tag must point at the exact bytes that were tested, so promotion
// is always a copy of published content — never a build. Two OCI -> OCI
// promotions are implemented, both within a single registry repository:
//
//   - RetagRegistry: a manifest copy of one content-tagged image to a release
//     tag. The blobs already exist, so it writes only a new tag — the exact
//     tested bytes under a human-readable name.
//
//   - RetagIndex: for a multi-arch layer, assembles an OCI image index over
//     every arch's content-tagged image and pushes it under one release tag, so
//     a single tag resolves to the right image per platform.
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

// IndexMember pairs a manifest arch name (as used in the manifest, e.g.
// "x86_64") with the content tag its single-arch image was pushed under.
type IndexMember struct {
	Arch string
	Tag  string
}

// ociPlatform maps a manifest arch name (RPM/dpkg style, e.g. "x86_64") to the
// OCI platform os/architecture pair ("linux"/"amd64") used in image-index
// descriptors. It is the inverse of the CLI's host-arch canonicalisation.
// Unknown arches pass through unchanged under os "linux" — a niche arch named
// with an OCI-style value still works; a mismatch would only make consumers
// skip that platform, never corrupt the index.
func ociPlatform(arch string) (osName, ociArch string) {
	switch arch {
	case "x86_64":
		return "linux", "amd64"
	case "aarch64":
		return "linux", "arm64"
	case "i386":
		return "linux", "386"
	default:
		return "linux", arch
	}
}

// RetagIndex assembles an OCI image index over the given per-arch members and
// pushes it under release in the shared repository url/name (OCI -> OCI,
// multi-arch). All members must already be pushed to that same repository — the
// index references them by digest, valid only within one repo — which the
// caller enforces before calling.
//
// When force is false and the release tag already exists, it fails rather than
// overwriting.
func RetagIndex(ctx context.Context, url, name, release string, members []IndexMember, tlsVerify, force bool) error {
	repo := fmt.Sprintf("%s/%s", url, name)
	dstRef := fmt.Sprintf("%s:%s", repo, release)
	log := slog.With("component", "promote", "dest", dstRef, "members", len(members))

	if err := checkDestination(ctx, dstRef, tlsVerify, force); err != nil {
		return err
	}

	entries := make([]registry.IndexEntry, 0, len(members))
	for _, m := range members {
		osName, ociArch := ociPlatform(m.Arch)
		entries = append(entries, registry.IndexEntry{Tag: m.Tag, OS: osName, Arch: ociArch})
	}

	log.Info("assembling image index")
	if err := registry.PushIndex(ctx, repo, release, entries, tlsVerify); err != nil {
		return err
	}
	log.Info("pushed image index")
	return nil
}
