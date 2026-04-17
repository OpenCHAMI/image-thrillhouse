package builder

import "github.com/travisbcotton/image-build/internal/config"

type container interface {
	Run(cmd []string) error
	WriteFile(file config.File) error
	Commit(name, tag string) error
	Delete()
}
