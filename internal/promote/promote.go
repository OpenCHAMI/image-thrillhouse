// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package promote re-tags already-built, already-tested artifacts under
// human-readable release tags without rebuilding them.
//
// A release tag must point at the exact bytes that were tested, so promotion
// is always a copy or re-projection of published content — never a build.
//
//   - RetagRegistry (OCI -> OCI): a manifest copy of one content-tagged image to
//     a release tag in the same repository. The blobs already exist, so it writes
//     only a new tag. Multi-arch is this applied once per arch (each arch has its
//     own repo, e.g. <image>-x86_64 / <image>-aarch64), driven by the caller.
//
//   - MaterializeToS3 (OCI -> S3): pull the content-tagged image, mount it, and
//     project it into S3 boot artifacts (rootfs/kernel/initramfs) under the
//     release tag via the S3 publisher. A re-package of tested bytes, not a build.
package promote

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-thrillhouse/internal/buildah"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"
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
// none is present. A layer selected for promotion that lacks a registry block is
// a hard failure, not a silent skip — a mis-tagged manifest should fail loud.
func FindPublish(publishes []config.Publish, typ string) (config.Publish, error) {
	for _, p := range publishes {
		if p.Type == typ {
			return p, nil
		}
	}
	return config.Publish{}, fmt.Errorf("layer config has no %q publish block", typ)
}

// RetagRegistry promotes a registry artifact to a release tag in the same
// repository (OCI -> OCI). It is a manifest copy from the content tag to the
// release tag: the blobs already exist at the destination, so only a new tag is
// written — the exact tested bytes under a human-readable name, never a rebuild.
//
// When force is false and the release tag already exists, it fails rather than
// overwriting.
func RetagRegistry(ctx context.Context, src RegistrySource, release string, force bool) error {
	srcRef := src.Ref()
	dstRef := src.RefWithTag(release)
	log := slog.With("component", "promote", "source", srcRef, "dest", dstRef)

	if !force {
		exists, err := registry.RefExists(ctx, dstRef, src.TLSVerify)
		if err != nil {
			return fmt.Errorf("check destination %s: %w", dstRef, err)
		}
		if exists {
			return fmt.Errorf("release tag %s already exists (use --force to overwrite)", dstRef)
		}
	}

	log.Info("retagging registry image")
	if err := registry.Copy(ctx, srcRef, dstRef, src.TLSVerify); err != nil {
		return err
	}
	log.Info("retagged registry image")
	return nil
}

// MaterializeToS3 projects a registry image into S3 boot artifacts under a
// release tag. It pulls the content-tagged image, mounts its rootfs, and runs
// the S3 publisher's extraction (SquashFS + kernel + initramfs) unchanged — a
// re-package of already-tested bytes, never a rebuild.
//
// dst is a constructed S3 publisher carrying the target bucket/prefix/arch and
// credentials; name is passed through to Publish; release is the tag the S3
// object layout is keyed under. Labels are nil — the S3 publisher does not apply
// OCI labels.
func MaterializeToS3(ctx context.Context, src RegistrySource, dst publisher.Publisher, name, release string) error {
	log := slog.With("component", "promote", "source", src.Ref(), "release", release)
	log.Info("materializing registry image to s3")

	// Pull + mount the tested image. NewContainer returns a generic
	// container.Container whose MountPath() is all the S3 publisher needs, so it
	// cannot tell (and does not care) that this rootfs came from a registry pull
	// rather than a fresh build.
	c, err := buildah.NewContainer(ctx, src.Ref(), src.TLSVerify)
	if err != nil {
		return fmt.Errorf("pull source image %s: %w", src.Ref(), err)
	}
	defer c.Delete()

	if err := dst.Publish(ctx, c, name, []string{release}, nil); err != nil {
		return fmt.Errorf("publish %q to s3: %w", release, err)
	}

	log.Info("materialized to s3")
	return nil
}
