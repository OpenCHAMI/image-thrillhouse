package zypper

import (
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

type ZypperBackend struct{}

func New(options map[string]string) *ZypperBackend {
	return &ZypperBackend{}
}

func (z *ZypperBackend) ConfigFilePath() string {
	return "/etc/zypp/zypp.conf"
}

func (z *ZypperBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "zypper", "-q", "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "zypper", "-q", "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (z *ZypperBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "zypper", "-q", "--installroot", rootPath, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "zypper", "-q", "--installroot", rootPath, "install", "-y", "-t")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (z *ZypperBackend) ValidateOptions(options map[string]string) error {
	return nil
}

func (z *ZypperBackend) SupportsInstallRoot() bool {
	return true
}
func (z *ZypperBackend) SupportsParentInstall() bool {
	return true
}

func (z *ZypperBackend) OutputWriter() container.OutputWriter {
	return &ZypperLogWriter{}
}
