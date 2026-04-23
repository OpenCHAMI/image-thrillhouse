package dnf

import (
	"fmt"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

type DnfBackend struct{}

func New(options map[string]string) *DnfBackend {
	return &DnfBackend{}
}

func (d *DnfBackend) ConfigFilePath() string {
	return "/etc/dnf/dnf.conf"
}

func (d *DnfBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "dnf", "-q", "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "dnf", "-q", "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	for _, mod := range install.Modules {
		cmd := make([]string, 0, 6)
		cmd = append(cmd, "dnf", "-q", "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (d *DnfBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	for _, mod := range install.Modules {
		cmd := make([]string, 0, 6)
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (d *DnfBackend) ValidateOptions(options map[string]string) error {
	return nil
}

func (d *DnfBackend) SupportsInstallRoot() bool {
	return true
}
func (d *DnfBackend) SupportsParentInstall() bool {
	return true
}

func (d *DnfBackend) OutputWriter() container.OutputWriter {
	return &dnfLogWriter{}
}
