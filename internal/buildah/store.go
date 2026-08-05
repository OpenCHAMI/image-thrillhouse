// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package buildah provides container operations using the Buildah library.
package buildah

import (
	"errors"
	"fmt"
	"log/slog"

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

// PruneImage removes the image with the given ID from local storage, along
// with any layers left unreferenced by its removal. An empty id is a no-op.
//
// A missing image is not an error — the caller's goal is that it be gone. An
// image still held by a container IS reported: that means something else on
// the host is using it, and swallowing that would hide the very leak this
// function exists to prevent.
func PruneImage(id string) error {
	if id == "" {
		return nil
	}

	store, err := openStore()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer shutdownQuietly(store)

	if _, err := store.DeleteImage(id, true); err != nil {
		if errors.Is(err, storage.ErrImageUnknown) {
			return nil
		}
		return fmt.Errorf("delete image %s: %w", id, err)
	}
	return nil
}

// imageIDSet returns the IDs of every image currently in the store. Used to
// tell a base image this run pulled from one that was already present.
func imageIDSet(store storage.Store) (map[string]struct{}, error) {
	images, err := store.Images()
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	ids := make(map[string]struct{}, len(images))
	for _, img := range images {
		ids[img.ID] = struct{}{}
	}
	return ids, nil
}

// shutdownQuietly releases a store handle opened for a one-shot query.
// ErrLayerUsedByContainer is expected on any host that shares the store with
// podman (see Container.Delete for the long version) and is logged at DEBUG.
func shutdownQuietly(store storage.Store) {
	if _, err := store.Shutdown(false); err != nil {
		log := slog.With("component", "storage")
		if errors.Is(err, storage.ErrLayerUsedByContainer) {
			log.Debug("store left running; other containers still hold mounts", "error", err)
		} else {
			log.Warn("shutdown store", "error", err)
		}
	}
}
