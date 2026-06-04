package buildah

import (
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

	// Log the storage configuration for debugging
	slog.Debug("Opening container storage",
		"graphRoot", opts.GraphRoot,
		"runRoot", opts.RunRoot,
		"driver", opts.GraphDriverName)

	store, err := storage.GetStore(opts)
	if err != nil {
		return nil, fmt.Errorf("get store (graphRoot=%s, driver=%s): %w",
			opts.GraphRoot, opts.GraphDriverName, err)
	}

	return store, nil
}
