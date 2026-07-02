// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package buildah provides container operations using the Buildah library.
package buildah

import (
	"fmt"

	"go.podman.io/storage"
)

// openStore opens the default container storage.
// This initializes access to the local container/image storage used by Podman and Buildah.
// The storage location and configuration come from the system's default settings.
func openStore() (storage.Store, error) {
	opts, err := storage.DefaultStoreOptions()
	if err != nil {
		return nil, fmt.Errorf("default store opts: %w", err)
	}
	return storage.GetStore(opts)
}
