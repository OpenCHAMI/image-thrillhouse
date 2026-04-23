package apt

import (
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

type AptBackend struct{}

func New(options map[string]string) *AptBackend {
	return &AptBackend{}
}

func (a *AptBackend) ValidateOptions(options map[string]string) error {
	return nil
}

func (a *AptBackend) SupportsInstallRoot() bool   { return false }
func (a *AptBackend) SupportsParentInstall() bool { return true }

func (a *AptBackend) ConfigFilePath() string {
	return "/etc/apt/apt.conf.d/99-image-build.conf"
}

func (a *AptBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	// always update first
	cmds = append(cmds, []string{"apt-get", "update", "-q"})

	if len(install.Groups) > 0 {
		slog.Warn("apt backend does not support package groups, ignoring", "groups", install.Groups)
	}
	if len(install.Modules) > 0 {
		slog.Warn("apt backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "apt-get", "install", "-y", "-q")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (a *AptBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	return nil
}

func (a *AptBackend) OutputWriter() container.OutputWriter {
	return container.NewBufLogWriter("apt")
}
