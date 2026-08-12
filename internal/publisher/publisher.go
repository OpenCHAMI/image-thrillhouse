// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package publisher defines the interface for image publishing destinations.
// Publishers determine where built images are stored or uploaded after the build completes.
package publisher

import (
	"context"

	"github.com/openchami/image-thrillhouse/internal/container"
)

// Publisher is a destination a built image can be written to. A build may use
// several at once. Implementations live in the sibling packages: local,
// squashfs, registry, s3.
type Publisher interface {
	Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error

	// Exists reports whether the image is already present at the destination.
	// When existence can't be determined up front, return false — that means
	// "rebuild", the conservative direction for skip-if-exists. Genuine errors
	// (auth, network) must be returned rather than reported as absent, so a
	// build fails loud instead of silently rebuilding on an outage.
	Exists(ctx context.Context, name string, tags []string) (bool, error)
}
