package backend

import (
	"github.com/travisbcotton/image-build/internal/config"
)

type Backend interface {
	InstallCommands(install config.Install) [][]string
}
