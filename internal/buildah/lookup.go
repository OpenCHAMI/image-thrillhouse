// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package buildah provides container operations using the Buildah library.
package buildah

import (
	"errors"
	"fmt"

	"go.podman.io/storage"
)

// ImageExists reports whether an image with the given name (e.g.
// "localhost/rocky-base:9.5") exists in the default local container storage.
//
// Used by the local publisher's Exists check to support skip-if-exists builds.
// Returns (false, nil) if the image is not found, (true, nil) if it is, or
// (false, err) on any other storage error.
func ImageExists(name string) (bool, error) {
	store, err := openStore()
	if err != nil {
		return false, fmt.Errorf("open store: %w", err)
	}
	defer func() { _, _ = store.Shutdown(false) }()

	_, err = store.Image(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, storage.ErrImageUnknown) {
		return false, nil
	}
	return false, fmt.Errorf("lookup image %q: %w", name, err)
}
