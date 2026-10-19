// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package backend_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/openchami/image-thrillhouse/internal/backend"
	"github.com/openchami/image-thrillhouse/internal/backend/apt"
	"github.com/openchami/image-thrillhouse/internal/backend/dnf"
	"github.com/openchami/image-thrillhouse/internal/backend/zypper"
	"github.com/openchami/image-thrillhouse/internal/config"
)

func TestCacheOptionValidation(t *testing.T) {
	for name, b := range map[string]backend.Backend{
		"dnf": dnf.New(nil), "zypper": zypper.New(nil), "apt": apt.New(nil),
	} {
		t.Run(name, func(t *testing.T) {
			keys := []string{"clean"}
			if name != "apt" {
				keys = append(keys, "refresh")
			}
			for _, key := range keys {
				for _, value := range []string{"", "true", "false", "yes"} {
					err := b.ValidateOptions(map[string]string{key: value})
					if (err != nil) != (value == "yes") {
						t.Errorf("%s=%q: unexpected validation result: %v", key, value, err)
					}
				}
			}
		})
	}
}

func TestDNFRefreshAndCleanTargetInstallRoot(t *testing.T) {
	install := config.Install{
		Packages: []string{"vim"}, Groups: []string{"Development Tools"},
		Modules: []config.Module{{Name: "nodejs", Stream: "20", Action: "enable"}},
	}
	for _, root := range []string{"", "/tmp/root with spaces"} {
		t.Run(root, func(t *testing.T) {
			b := dnf.New(map[string]string{"refresh": "true", "clean": "true", "releasever": "9", "skip-broken": "true"})
			cmds := b.InstallCommands(install)
			wantClean := []string{"dnf", "-q", "--releasever", "9"}
			if root != "" {
				cmds = b.InstallRootCommands(install, root)
				wantClean = append(wantClean, "--installroot", root)
			}
			wantClean = append(wantClean, "clean", "all")
			if len(cmds) != 4 {
				t.Fatalf("commands = %v, want module, packages, group, clean", cmds)
			}
			for _, cmd := range cmds[:3] {
				if i := slices.Index(cmd, "--refresh"); i < 0 || i > slices.Index(cmd, "--releasever") {
					t.Errorf("refresh must be a global flag: %v", cmd)
				}
				if root != "" && (!slices.Contains(cmd, root) || !slices.Contains(cmd, "--installroot")) {
					t.Errorf("install targets wrong root: %v", cmd)
				}
			}
			if !reflect.DeepEqual(cmds[3], wantClean) {
				t.Errorf("clean = %v, want %v", cmds[3], wantClean)
			}
		})
	}
}

func TestZypperRefreshBeforeInstallsAndCleanAfter(t *testing.T) {
	install := config.Install{Packages: []string{"vim"}, Groups: []string{"devel_basis"}}
	for _, root := range []string{"", "/tmp/root with spaces"} {
		t.Run(root, func(t *testing.T) {
			b := zypper.New(map[string]string{"refresh": "true", "clean": "true", "no-recommends": "true"})
			cmds := b.InstallCommands(install)
			prefix := []string{"zypper"}
			if root != "" {
				cmds = b.InstallRootCommands(install, root)
				prefix = append(prefix, "--root", root)
			}
			want := [][]string{
				append(slices.Clone(prefix), "--gpg-auto-import-keys", "refresh", "--force"),
				append(slices.Clone(prefix), "clean", "--all"),
			}
			if len(cmds) != 4 {
				t.Fatalf("commands = %v, want refresh, packages, pattern, clean", cmds)
			}
			if !reflect.DeepEqual(cmds[0], want[0]) || !reflect.DeepEqual(cmds[3], want[1]) {
				t.Errorf("refresh/clean = %v / %v, want %v", cmds[0], cmds[3], want)
			}
			for _, cmd := range cmds[1:3] {
				if !slices.Contains(cmd, "install") {
					t.Errorf("expected install: %v", cmd)
				}
			}
		})
	}
}

func TestAPTCleanRemovesArchivesAndIndexesAfterInstall(t *testing.T) {
	cmds := apt.New(map[string]string{"clean": "true"}).InstallCommands(config.Install{Packages: []string{"vim"}})
	want := [][]string{
		{"apt-get", "update", "-q"},
		{"apt-get", "install", "-y", "-q", "--no-install-recommends", "--no-install-suggests", "vim"},
		{"apt-get", "clean"},
		{"rm", "-rf", "--", "/var/lib/apt/lists"},
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("commands = %v, want %v", cmds, want)
	}
}

func TestCacheOptionsDisabledPreserveCommands(t *testing.T) {
	install := config.Install{Packages: []string{"vim"}}
	for name, newBackend := range map[string]func(map[string]string) backend.Backend{
		"dnf":    func(o map[string]string) backend.Backend { return dnf.New(o) },
		"zypper": func(o map[string]string) backend.Backend { return zypper.New(o) },
		"apt":    func(o map[string]string) backend.Backend { return apt.New(o) },
	} {
		t.Run(name, func(t *testing.T) {
			opts := map[string]string{"clean": "false"}
			if name != "apt" {
				opts["refresh"] = "false"
			}
			defaults, disabled := newBackend(nil), newBackend(opts)
			if !reflect.DeepEqual(defaults.InstallCommands(install), disabled.InstallCommands(install)) ||
				!reflect.DeepEqual(defaults.InstallRootCommands(install, "/mnt"), disabled.InstallRootCommands(install, "/mnt")) {
				t.Error("explicit false changed default commands")
			}
		})
	}
}
