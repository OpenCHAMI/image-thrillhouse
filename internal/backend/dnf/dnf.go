package dnf

import (
	"fmt"

	"github.com/travisbcotton/image-build/internal/config"
)

type DnfBackend struct{}

func New() *DnfBackend {
	return &DnfBackend{}
}

func (d *DnfBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 3+len(install.Packages))
		cmd = append(cmd, "dnf", "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 3+len(install.Groups))
		cmd = append(cmd, "dnf", "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	for _, mod := range install.Modules {
		cmd := make([]string, 0, 4)
		cmd = append(cmd, "dnf", "module", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (d *DnfBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 3+len(install.Packages))
		cmd = append(cmd, "dnf", "--installroot", rootPath, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 3+len(install.Groups))
		cmd = append(cmd, "dnf", "--installroot", rootPath, "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	for _, mod := range install.Modules {
		cmd := make([]string, 0, 4)
		cmd = append(cmd, "dnf", "--installroot", rootPath, "module", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}
