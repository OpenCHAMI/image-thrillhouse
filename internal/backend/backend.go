package backend

import (
	"github.com/travisbcotton/image-build/internal/config"
)

type Backend interface {
	ConfigFilePath() string
	InstallCommands(install config.Install) [][]string
	InstallRootCommands(install config.Install, rootPath string) [][]string
}
