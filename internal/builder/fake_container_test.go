// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Test doubles for the container.Container and publisher.Publisher
// interfaces, used by the builder unit tests. Kept tiny on purpose — each
// stub returns zero values or records the call. Tests that need behaviour
// can override individual fields.
package builder

import (
	"context"
	"fmt"

	"go.podman.io/buildah/define"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
)

// fakeContainer records WriteFile / Run / RunScript calls and stubs out
// every other interface method with safe defaults. Only the methods the
// builder unit tests exercise have meaningful behaviour.
type fakeContainer struct {
	WriteFileCalls     []config.File
	RunCalls           [][]string
	RunModes           []container.RunMode
	RunScriptCalls     []string
	CopyDirectoryCalls []copyDirectoryCall
	SetLabelsCalls     []map[string]string
	Events             []string // interleaved call log ("run:<argv0>", "write:<path>", "setlabels") for ordering assertions
	WriteFileErr       error
	RunErr             error
	RunScriptErr       error
	CopyDirectoryErr   error
	MountPathReturn    string
}

// copyDirectoryCall records a single CopyDirectory invocation so builder
// tests can assert how config.Directory entries get mapped onto
// container.CopyDirectoryOptions.
type copyDirectoryCall struct {
	Src  string
	Dest string
	Opts container.CopyDirectoryOptions
}

func (f *fakeContainer) Run(ctx context.Context, cmd []string, mode container.RunMode, out container.OutputWriter, opts ...container.RunOption) error {
	f.RunCalls = append(f.RunCalls, cmd)
	f.RunModes = append(f.RunModes, mode)
	if len(cmd) > 0 {
		f.Events = append(f.Events, "run:"+cmd[0])
	}
	return f.RunErr
}

func (f *fakeContainer) RunScript(ctx context.Context, script string, out container.OutputWriter, opts ...container.RunOption) error {
	f.RunScriptCalls = append(f.RunScriptCalls, script)
	return f.RunScriptErr
}

func (f *fakeContainer) WriteFile(ctx context.Context, file config.File) error {
	f.WriteFileCalls = append(f.WriteFileCalls, file)
	f.Events = append(f.Events, "write:"+file.Path)
	return f.WriteFileErr
}

func (f *fakeContainer) CopyDirectory(ctx context.Context, srcDir, destDir string, opts container.CopyDirectoryOptions) error {
	f.CopyDirectoryCalls = append(f.CopyDirectoryCalls, copyDirectoryCall{
		Src:  srcDir,
		Dest: destDir,
		Opts: opts,
	})
	return f.CopyDirectoryErr
}

func (f *fakeContainer) Commit(ctx context.Context, name, tag string) (string, error) {
	return "fake-id", nil
}

func (f *fakeContainer) CommitWithLabels(ctx context.Context, name, tag string, labels map[string]string) (string, error) {
	return "fake-id", nil
}

func (f *fakeContainer) CommitWithLabelsTags(ctx context.Context, name string, tags []string, labels map[string]string) (string, error) {
	return "fake-id", nil
}

func (f *fakeContainer) SetLabels(labels map[string]string) {
	f.SetLabelsCalls = append(f.SetLabelsCalls, labels)
	f.Events = append(f.Events, "setlabels")
}

func (f *fakeContainer) GetID() string                                                    { return "fake-id" }
func (f *fakeContainer) GetParent() string                                                { return "scratch" }
func (f *fakeContainer) GetName() string                                                  { return "fake" }
func (f *fakeContainer) Delete()                                                          {}
func (f *fakeContainer) MountPath() string                                                { return f.MountPathReturn }
func (f *fakeContainer) GetIsolation() define.Isolation                                   { return define.IsolationDefault }
func (f *fakeContainer) CommitToRegistry(ctx context.Context, ref string, tls bool) error { return nil }

// fakePublisher records Publish/Exists calls and lets the test control the
// answers. Used by allExist tests so we can simulate "all present" / "first
// missing" / "error" without standing up real publishers.
type fakePublisher struct {
	PublishCalls int
	ExistsCalls  int
	ExistsReturn bool
	ExistsErr    error
	LastName     string
	LastTags     []string
}

func (p *fakePublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	p.PublishCalls++
	p.LastName = name
	p.LastTags = tags
	return nil
}

func (p *fakePublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	p.ExistsCalls++
	p.LastName = name
	p.LastTags = tags
	return p.ExistsReturn, p.ExistsErr
}

// errPublisher is a one-liner publisher that always errors from Exists,
// used to confirm error propagation.
type errPublisher struct{ msg string }

func (p *errPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	return fmt.Errorf("not implemented")
}

func (p *errPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	return false, fmt.Errorf("%s", p.msg)
}

// fakeBackendBase is the common no-op stub for the backend.Backend interface.
// Tests that need behaviour on a specific method embed this and override.
type fakeBackendBase struct{}

func (fakeBackendBase) Bootstrap(ctx context.Context, c container.Container, rootPath string) error {
	return nil
}
func (fakeBackendBase) SupportsInstallRoot() bool                       { return true }
func (fakeBackendBase) RequiresEmptyRoot() bool                         { return false }
func (fakeBackendBase) SupportsParentInstall() bool                     { return true }
func (fakeBackendBase) ValidateOptions(options map[string]string) error { return nil }
func (fakeBackendBase) ConfigFilePath() string                          { return "" }
func (fakeBackendBase) InstallCommands(install config.Install) [][]string {
	return nil
}
func (fakeBackendBase) InstallRootCommands(install config.Install, rootPath string) [][]string {
	return nil
}
func (fakeBackendBase) RemovePackagesCommand(packages []string, rootPath string) []string {
	return nil
}
func (fakeBackendBase) ImportGPGKeyCommand(keyPath, rootPath string) []string { return nil }
func (fakeBackendBase) OutputWriter() container.OutputWriter                  { return &container.NopWriter{} }
func (fakeBackendBase) IsAcceptableExitCode(exitCode int, output string) bool { return false }

// fakeBackendNoConfigPath simulates a backend (like mmdebstrap) that has no
// persistent config file. The builder must refuse to apply
// layer.manager.config in this case.
type fakeBackendNoConfigPath struct{ fakeBackendBase }

// fakeBackendWithConfigPath simulates a backend (like dnf) that has a
// canonical config path. Configurable per-test via the `path` field.
type fakeBackendWithConfigPath struct {
	fakeBackendBase
	path string
}

func (b *fakeBackendWithConfigPath) ConfigFilePath() string { return b.path }
