// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package buildah provides container operations using the Buildah library.
package buildah

import (
	"fmt"

	"go.podman.io/storage"
)

// storageOptions holds the storage configuration for this package.
// When nil, the default system storage is used. When set, it overrides
// the default storage location (e.g., for temporary isolated storage).
var storageOptions *storage.StoreOptions

// SetStorageOptions configures custom storage options for all subsequent
// container and store operations. Pass nil to restore default behavior.
// This must be called before any containers are created.
//
// The caller is responsible for cleaning up the storage directory after
// all operations are complete.
func SetStorageOptions(opts *storage.StoreOptions) {
	storageOptions = opts
}

// openStore opens the container storage, either using custom options set
// via SetStorageOptions or falling back to the system's default settings.
// This initializes access to the local container/image storage used by Podman and Buildah.
func openStore() (storage.Store, error) {
	var opts storage.StoreOptions
	var err error

	if storageOptions != nil {
		opts = *storageOptions
	} else {
		opts, err = storage.DefaultStoreOptions()
		if err != nil {
			return nil, fmt.Errorf("default store opts: %w", err)
		}
	}
	return storage.GetStore(opts)
}
