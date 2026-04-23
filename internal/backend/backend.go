package backend

import (
	"github.com/travisbcotton/image-build/internal/config"
)

type Backend interface {
	SupportsInstallRoot() bool
	SupportsParentInstall() bool
	ValidateOptions(options map[string]string) error
	ConfigFilePath() string
	InstallCommands(install config.Install) [][]string
	InstallRootCommands(install config.Install, rootPath string) [][]string
}
