// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package promote re-tags already-built, already-tested artifacts under
// human-readable release tags without rebuilding them.
//
// A release tag must point at the exact bytes that were tested, so promotion
// is always a copy or re-package of published content — never a build.
//
// Stage 1 (this file) implements registry -> S3 "materialize": the layer's
// content-addressed image is pulled from the registry, its rootfs mounted, and
// republished to S3 under a release tag using the S3 publisher's existing
// extraction (SquashFS + kernel + initramfs). Because the rootfs is pulled by
// its content tag, the result is bit-identical to what was tested; only the
// packaging format changes.
package promote

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-thrillhouse/internal/buildah"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"
)

// RegistrySource identifies a tested artifact already published to an OCI
// registry under its content-addressed tag — the thing a promotion copies FROM.
type RegistrySource struct {
	URL       string // registry URL prefix (e.g. "registry.io/openchami")
	Name      string // image name (meta.name)
	Tag       string // content-addressed tag to promote from
	TLSVerify bool
}

// Ref returns the fully-qualified source reference, matching the format the
// registry publisher pushes to: <url>/<name>:<tag>. Keeping this construction
// in one place is what stops the promote path and the publish path from
// drifting on how a reference is spelled.
func (s RegistrySource) Ref() string {
	return fmt.Sprintf("%s/%s:%s", s.URL, s.Name, s.Tag)
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

// MaterializeToS3 promotes a registry artifact to S3 under a release tag. It
// pulls the tested image, mounts its rootfs, and runs the S3 publisher's
// existing extraction unchanged — a re-package of already-tested bytes, never a
// rebuild.
//
// dst is a constructed S3 publisher (destination bucket/prefix/creds); name is
// the image name used in the S3 object key; release is the human-readable tag.
// Labels are intentionally nil: the S3 publisher does not apply OCI labels.
//
// Scope note (stage 1): the pull is same-arch. It is meant to run on a machine
// of the target architecture — the distributed-build model where each arch
// promotes itself. Cross-arch materialization would require an explicit
// target-arch SystemContext in buildah.NewContainer and is out of scope here.
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
