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
		cmds = append(cmds, append([]string{"dnf", "install", "-y"}, install.Packages...))
	}
	if len(install.Groups) > 0 {
		cmds = append(cmds, append([]string{"dnf", "groupinstall", "-y"}, install.Groups...))
	}
	for _, mod := range install.Modules {
		cmds = append(cmds, []string{"dnf", "module", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream)})
	}

	return cmds
}
