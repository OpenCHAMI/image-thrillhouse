package mmdebstrap

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/config"
)

type MmdebstrapBackend struct {
	suite   string
	mirror  string
	variant string
	mode    string
}

func New(options map[string]string) *MmdebstrapBackend {
	variant := options["variant"]
	if variant == "" {
		variant = "minbase"
	}
	mode := options["mode"]
	if mode == "" {
		mode = "fakechroot"
	}
	return &MmdebstrapBackend{
		suite:   options["suite"],
		mirror:  options["mirror"],
		variant: variant,
		mode:    mode,
	}
}

func (d *MmdebstrapBackend) ConfigFilePath() string {
	return "/etc/dnf/dnf.conf"
}

func (m *MmdebstrapBackend) InstallCommands(install config.Install) [][]string {
	slog.Warn("mmdebstrap does not support parent image installs, use apt backend instead")
	return nil
}

func (m *MmdebstrapBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	cmd := make([]string, 0)
	cmd = append(cmd, "mmdebstrap")
	cmd = append(cmd, "--mode="+m.mode)
	cmd = append(cmd, "--variant="+m.variant)

	// add packages as --include
	if len(install.Packages) > 0 {
		cmd = append(cmd, "--include="+strings.Join(install.Packages, ","))
	}

	cmd = append(cmd, m.suite)
	cmd = append(cmd, rootPath)
	cmd = append(cmd, m.mirror)

	return [][]string{cmd}
}

func (m *MmdebstrapBackend) ValidateOptions(options map[string]string) error {
	if options["suite"] == "" {
		return fmt.Errorf("mmdebstrap requires options.suite (e.g. bookworm)")
	}
	if options["mirror"] == "" {
		return fmt.Errorf("mmdebstrap requires options.mirror (e.g. http://deb.debian.org/debian)")
	}
	return nil
}

func (d *MmdebstrapBackend) SupportsInstallRoot() bool {
	return true
}
func (d *MmdebstrapBackend) SupportsParentInstall() bool {
	return true
}
