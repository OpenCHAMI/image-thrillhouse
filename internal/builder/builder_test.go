// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Tier 2 builder tests — methods with a small Container or Publisher mock
// surface. These exercise the "fan out a config slice into c.WriteFile calls"
// shape that several builder methods share, plus the publisher iteration in
// allExist. See fake_container_test.go for the test doubles.
package builder

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/backend"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"
)

// builderWithLayer constructs a minimal Builder pointed at the given Layer
// config. Backend and publishers are nil — the tests in this file only touch
// methods that don't reach those fields.
func builderWithLayer(layer config.Layer) *Builder {
	return &Builder{
		cfg: &config.Config{
			Meta:  config.Meta{Name: "test", Tags: []string{"1"}, From: "scratch"},
			Layer: layer,
		},
	}
}

func TestApplyManagerConfig_NoConfigIsNoop(t *testing.T) {
	// When layer.manager.config is empty, applyManagerConfig must not invoke
	// any backend or container method — important because some backends
	// return "" from ConfigFilePath and we don't want to write to that.
	b := builderWithLayer(config.Layer{
		Manager: config.Manager{Name: "dnf"}, // no Config field
	})
	c := &fakeContainer{}
	if err := b.applyManagerConfig(context.Background(), c); err != nil {
		t.Fatalf("applyManagerConfig: %v", err)
	}
	if len(c.WriteFileCalls) != 0 {
		t.Errorf("expected no WriteFile calls, got %d", len(c.WriteFileCalls))
	}
}

func TestApplyManagerConfig_RejectsBackendWithoutConfigPath(t *testing.T) {
	// Regression guard for the mmdebstrap sentinel fix: when the backend
	// returns "" from ConfigFilePath but the user set layer.manager.config,
	// we must error out rather than silently writing to a bogus path.
	b := builderWithLayer(config.Layer{
		Manager: config.Manager{Name: "mmdebstrap", Config: "[main]\nfoo=bar"},
	})
	b.backend = &fakeBackendNoConfigPath{}
	c := &fakeContainer{}
	err := b.applyManagerConfig(context.Background(), c)
	if err == nil {
		t.Fatal("expected error when backend has empty ConfigFilePath but config is set")
	}
	if !strings.Contains(err.Error(), "does not support layer.manager.config") {
		t.Errorf("error should mention unsupported config, got: %v", err)
	}
	if len(c.WriteFileCalls) != 0 {
		t.Errorf("WriteFile must not be called on the unsupported path, got %d calls", len(c.WriteFileCalls))
	}
}

func TestApplyManagerConfig_WritesToBackendPath(t *testing.T) {
	b := builderWithLayer(config.Layer{
		Manager: config.Manager{Name: "dnf", Config: "[main]\nkeepcache=1"},
	})
	b.backend = &fakeBackendWithConfigPath{path: "/etc/dnf/dnf.conf"}
	c := &fakeContainer{}
	if err := b.applyManagerConfig(context.Background(), c); err != nil {
		t.Fatalf("applyManagerConfig: %v", err)
	}
	if len(c.WriteFileCalls) != 1 {
		t.Fatalf("expected 1 WriteFile call, got %d", len(c.WriteFileCalls))
	}
	got := c.WriteFileCalls[0]
	if got.Path != "/etc/dnf/dnf.conf" {
		t.Errorf("WriteFile path = %q, want /etc/dnf/dnf.conf", got.Path)
	}
	if got.Content != "[main]\nkeepcache=1" {
		t.Errorf("WriteFile content = %q, want the manager config", got.Content)
	}
}

func TestWriteRepos_EmptyLayerIsNoop(t *testing.T) {
	b := builderWithLayer(config.Layer{}) // no repos
	c := &fakeContainer{}
	if err := b.writeRepos(context.Background(), c); err != nil {
		t.Fatalf("writeRepos: %v", err)
	}
	if len(c.WriteFileCalls) != 0 {
		t.Errorf("expected no WriteFile calls, got %d", len(c.WriteFileCalls))
	}
}

func TestWriteRepos_OneCallPerRepo(t *testing.T) {
	// Repos should be translated 1:1 into WriteFile calls with Path/Content/
	// URL/Src preserved. The "GPGKey" field is intentionally NOT forwarded
	// to WriteFile — it's handled separately by importGPGKeys.
	b := builderWithLayer(config.Layer{
		Repos: []config.Repo{
			{Path: "/etc/yum.repos.d/baseos.repo", Content: "[baseos]\nbaseurl=..."},
			{Path: "/etc/yum.repos.d/epel.repo", URL: "https://example.com/epel.repo"},
		},
	})
	c := &fakeContainer{}
	if err := b.writeRepos(context.Background(), c); err != nil {
		t.Fatalf("writeRepos: %v", err)
	}
	if len(c.WriteFileCalls) != 2 {
		t.Fatalf("expected 2 WriteFile calls, got %d", len(c.WriteFileCalls))
	}
	if c.WriteFileCalls[0].Path != "/etc/yum.repos.d/baseos.repo" {
		t.Errorf("first repo path = %q", c.WriteFileCalls[0].Path)
	}
	if c.WriteFileCalls[1].URL != "https://example.com/epel.repo" {
		t.Errorf("second repo URL not forwarded: got %+v", c.WriteFileCalls[1])
	}
}

func TestWriteRepos_PropagatesWriteFileError(t *testing.T) {
	// A WriteFile failure on any repo must abort the loop and bubble up
	// (not "best-effort, log and continue") — these are install-blocking.
	b := builderWithLayer(config.Layer{
		Repos: []config.Repo{{Path: "/etc/yum.repos.d/baseos.repo", Content: "x"}},
	})
	c := &fakeContainer{WriteFileErr: errors.New("disk full")}
	err := b.writeRepos(context.Background(), c)
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "write repo") {
		t.Errorf("error should be wrapped with 'write repo': %v", err)
	}
}

func TestWriteFiles_OneCallPerFile(t *testing.T) {
	b := builderWithLayer(config.Layer{
		Files: []config.File{
			{Path: "/etc/hostname", Content: "mybox"},
			{Path: "/usr/local/bin/init.sh", Src: "init.sh", Mode: "0755"},
		},
	})
	c := &fakeContainer{}
	if err := b.writeFiles(context.Background(), c); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}
	if len(c.WriteFileCalls) != 2 {
		t.Fatalf("expected 2 WriteFile calls, got %d", len(c.WriteFileCalls))
	}
	if c.WriteFileCalls[1].Mode != "0755" {
		t.Errorf("Mode must be forwarded to WriteFile, got %q", c.WriteFileCalls[1].Mode)
	}
}

func TestWriteDirectories_EmptyLayerIsNoop(t *testing.T) {
	b := builderWithLayer(config.Layer{}) // no directories
	c := &fakeContainer{}
	if err := b.writeDirectories(context.Background(), c); err != nil {
		t.Fatalf("writeDirectories: %v", err)
	}
	if len(c.CopyDirectoryCalls) != 0 {
		t.Errorf("expected no CopyDirectory calls, got %d", len(c.CopyDirectoryCalls))
	}
}

func TestWriteDirectories_OneCallPerDirectory(t *testing.T) {
	// Each config.Directory translates 1:1 into a CopyDirectory call, with
	// every option forwarded onto CopyDirectoryOptions.
	fals := false
	b := builderWithLayer(config.Layer{
		Directories: []config.Directory{
			{
				Path:     "/opt/app",
				Src:      "./build/app",
				Mode:     "0755",
				Owner:    "1000:1000",
				Excludes: []string{"*.tmp"},
				// ContentsOnly left nil — builder must default it to true.
			},
			{
				Path:              "/opt/other",
				Src:               "./other",
				PreserveOwnership: true,
				ContentsOnly:      &fals,
			},
		},
	})
	c := &fakeContainer{}
	if err := b.writeDirectories(context.Background(), c); err != nil {
		t.Fatalf("writeDirectories: %v", err)
	}
	if len(c.CopyDirectoryCalls) != 2 {
		t.Fatalf("expected 2 CopyDirectory calls, got %d", len(c.CopyDirectoryCalls))
	}

	first := c.CopyDirectoryCalls[0]
	if first.Src != "./build/app" || first.Dest != "/opt/app" {
		t.Errorf("first call src/dest wrong: %+v", first)
	}
	if first.Opts.Chmod != "0755" || first.Opts.Chown != "1000:1000" {
		t.Errorf("first call chmod/chown not forwarded: %+v", first.Opts)
	}
	if len(first.Opts.Excludes) != 1 || first.Opts.Excludes[0] != "*.tmp" {
		t.Errorf("first call excludes not forwarded: %+v", first.Opts.Excludes)
	}
	if !first.Opts.ContentsOnly {
		t.Errorf("unset ContentsOnly must default to true, got false")
	}

	second := c.CopyDirectoryCalls[1]
	if !second.Opts.PreserveOwnership {
		t.Errorf("preserve_ownership not forwarded: %+v", second.Opts)
	}
	if second.Opts.ContentsOnly {
		t.Errorf("explicit contents_only=false must be honored, got true")
	}
}

func TestWriteDirectories_PropagatesCopyError(t *testing.T) {
	// Like writeFiles/writeRepos, a single CopyDirectory failure must abort
	// the build, not log-and-continue.
	b := builderWithLayer(config.Layer{
		Directories: []config.Directory{{Path: "/opt/app", Src: "./build"}},
	})
	c := &fakeContainer{CopyDirectoryErr: errors.New("buildah add failed")}
	err := b.writeDirectories(context.Background(), c)
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "copy directory") {
		t.Errorf("error should be wrapped with 'copy directory': %v", err)
	}
}

func TestAllExist_AllReturnTrue(t *testing.T) {
	p1 := &fakePublisher{ExistsReturn: true}
	p2 := &fakePublisher{ExistsReturn: true}
	b := &Builder{publishers: []publisher.Publisher{p1, p2}}
	got, err := b.allExist(context.Background(), "img", []string{"1"})
	if err != nil {
		t.Fatalf("allExist: %v", err)
	}
	if !got {
		t.Errorf("allExist should be true when all publishers return true")
	}
	if p1.ExistsCalls != 1 || p2.ExistsCalls != 1 {
		t.Errorf("each publisher should be probed once, got %d/%d", p1.ExistsCalls, p2.ExistsCalls)
	}
}

func TestAllExist_ShortCircuitsOnFirstMissing(t *testing.T) {
	// allExist must return false as soon as one publisher reports missing —
	// no point in probing the rest. Verify the second publisher isn't called.
	p1 := &fakePublisher{ExistsReturn: false}
	p2 := &fakePublisher{ExistsReturn: true}
	b := &Builder{publishers: []publisher.Publisher{p1, p2}}
	got, err := b.allExist(context.Background(), "img", []string{"1"})
	if err != nil {
		t.Fatalf("allExist: %v", err)
	}
	if got {
		t.Errorf("allExist should be false when first publisher returns false")
	}
	if p1.ExistsCalls != 1 {
		t.Errorf("first publisher should be probed exactly once")
	}
	if p2.ExistsCalls != 0 {
		t.Errorf("second publisher must NOT be probed after a missing report; got %d calls", p2.ExistsCalls)
	}
}

func TestAllExist_PropagatesError(t *testing.T) {
	// A probe error must bubble up — we don't want to silently rebuild on
	// an infra outage. Contract is documented on registry.Exists.
	b := &Builder{publishers: []publisher.Publisher{
		&errPublisher{msg: "registry timeout"},
	}}
	_, err := b.allExist(context.Background(), "img", []string{"1"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "registry timeout") {
		t.Errorf("error should mention underlying failure, got: %v", err)
	}
}

// orderRecordingPublisher captures the state of the container's SetLabels
// call log at Publish time, so tests can assert labels were applied BEFORE
// any publisher ran (the registry publisher pushes whatever is already in
// the container config and ignores its labels parameter).
type orderRecordingPublisher struct {
	fc                  *fakeContainer
	labelCallsAtPublish int
	labelsParam         map[string]string
}

func (p *orderRecordingPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	p.labelCallsAtPublish = len(p.fc.SetLabelsCalls)
	p.labelsParam = labels
	return nil
}

func (p *orderRecordingPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	return false, nil
}

// buildableBuilder wires a Builder whose newContainer returns the given fake,
// mirroring what New does but with test doubles in every slot.
func buildableBuilder(cfg *config.Config, fc *fakeContainer, be backend.Backend, pubs []publisher.Publisher) *Builder {
	return &Builder{
		cfg:     cfg,
		backend: be,
		newContainer: func(ctx context.Context, from string, tlsverify bool) (container.Container, error) {
			return fc, nil
		},
		publishers: pubs,
	}
}

// TestBuild_AppliesLabelsBeforePublish is the regression guard for the
// registry-only label bug: Build must call c.SetLabels before the publish
// loop so a direct-to-registry push carries the labels even when no local
// publisher runs first.
func TestBuild_AppliesLabelsBeforePublish(t *testing.T) {
	fc := &fakeContainer{}
	pub := &orderRecordingPublisher{fc: fc}
	cfg := &config.Config{
		Meta:  config.Meta{Name: "test", Tags: []string{"1"}, From: "docker.io/library/alpine"},
		Layer: config.Layer{Manager: config.Manager{Name: "dnf"}},
	}
	b := buildableBuilder(cfg, fc, fakeBackendBase{}, []publisher.Publisher{pub})

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(fc.SetLabelsCalls) == 0 {
		t.Fatal("Build never called SetLabels on the container")
	}
	if pub.labelCallsAtPublish == 0 {
		t.Error("SetLabels must be called BEFORE the publish loop, not after")
	}
	if pub.labelsParam["org.openchami.image.name"] != "test" {
		t.Errorf("publisher did not receive generated labels, got %v", pub.labelsParam)
	}
}

// fakeBackendEmptyRoot simulates mmdebstrap: scratch-only, refuses a
// non-empty root, and bootstraps via a single install command.
type fakeBackendEmptyRoot struct{ fakeBackendBase }

func (fakeBackendEmptyRoot) RequiresEmptyRoot() bool { return true }
func (fakeBackendEmptyRoot) InstallRootCommands(install config.Install, rootPath string) [][]string {
	return [][]string{{"mmdebstrap", "bookworm", rootPath}}
}

// TestBuild_EmptyRootBackendInstallsBeforeWrites is the regression guard for
// the mmdebstrap ordering bug: for a backend that refuses a non-empty scratch
// root, the install (bootstrap) must run before any repo/file writes.
func TestBuild_EmptyRootBackendInstallsBeforeWrites(t *testing.T) {
	fc := &fakeContainer{MountPathReturn: "/mnt/fake"}
	cfg := &config.Config{
		Meta: config.Meta{Name: "test", Tags: []string{"1"}, From: "scratch"},
		Layer: config.Layer{
			Manager: config.Manager{Name: "mmdebstrap"},
			Repos: []config.Repo{
				{Path: "/etc/apt/sources.list.d/extra.list", Content: "deb http://example.invalid stable main"},
			},
		},
	}
	b := buildableBuilder(cfg, fc, fakeBackendEmptyRoot{}, []publisher.Publisher{&fakePublisher{}})

	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	installIdx, writeIdx := -1, -1
	for i, ev := range fc.Events {
		switch {
		case ev == "run:mmdebstrap" && installIdx == -1:
			installIdx = i
		case strings.HasPrefix(ev, "write:/etc/apt/") && writeIdx == -1:
			writeIdx = i
		}
	}
	if installIdx == -1 {
		t.Fatalf("install command never ran; events: %v", fc.Events)
	}
	if writeIdx == -1 {
		t.Fatalf("repo write never happened; events: %v", fc.Events)
	}
	if installIdx > writeIdx {
		t.Errorf("install must run before repo writes for empty-root backends; events: %v", fc.Events)
	}
}

// fakeBackendAcceptAll tolerates every non-zero exit code — used to prove
// that helper-crash classification preempts the acceptable-exit-code check.
type fakeBackendAcceptAll struct{ fakeBackendBase }

func (fakeBackendAcceptAll) IsAcceptableExitCode(int, string) bool { return true }

func TestRunInstallCommands_HelperCrashPreemptsAcceptableExitCode(t *testing.T) {
	// When the reexec'd chroot helper crashes, its exit code does not belong
	// to the package manager — even a backend that would tolerate the code
	// must not turn the crash into a "successful" install.
	b := builderWithLayer(config.Layer{Manager: config.Manager{Name: "zypper"}})
	b.backend = fakeBackendAcceptAll{}
	c := &fakeContainer{
		RunOutput: "runtime/cgo: pthread_create failed: Resource temporarily unavailable\nSIGABRT: abort\n",
		RunErr:    errors.New("buildah run: exit status 2"),
	}
	err := b.runInstallCommands(context.Background(), c,
		[][]string{{"zypper", "install", "-y", "ansible"}},
		container.RunModeContainer, "run", slog.Default())
	if err == nil {
		t.Fatal("a helper crash must fail the build even when the backend accepts every exit code")
	}
	if !strings.Contains(err.Error(), "crashed") {
		t.Errorf("error should identify the crash as such, got: %v", err)
	}
}

func TestRunInstallCommands_SurfacesOutputOnFatalFailure(t *testing.T) {
	// A failed install whose output the backend classifier doesn't recognize
	// (here: the package-manager binary missing from the parent image) must
	// still put the command's own output in front of the user at the default
	// log level — previously it was only visible under --log-level debug, which
	// is what made "exec: dnf: not found" so hard to diagnose.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	b := builderWithLayer(config.Layer{Manager: config.Manager{Name: "dnf"}})
	b.backend = fakeBackendBase{}
	c := &fakeContainer{
		RunOutput: `exec: "dnf": executable file not found in $PATH`,
		RunErr:    errors.New("buildah run: exit status 1"),
	}
	err := b.runInstallCommands(context.Background(), c,
		[][]string{{"dnf", "-q", "install", "-y", "curl"}},
		container.RunModeContainer, "install", slog.Default())
	if err == nil {
		t.Fatal("expected a fatal error when the install command fails")
	}
	if !strings.Contains(buf.String(), "executable file not found") {
		t.Errorf("the command's output must be surfaced at ERROR on the fatal path; got logs:\n%s", buf.String())
	}
}

func TestRunInstallCommands_AcceptableExitCodeStillTolerated(t *testing.T) {
	// The crash detector must not break the existing tolerance path: a
	// non-zero exit with ordinary output and an accepting backend succeeds.
	b := builderWithLayer(config.Layer{Manager: config.Manager{Name: "zypper"}})
	b.backend = fakeBackendAcceptAll{}
	c := &fakeContainer{
		RunOutput: "warning: %post scriptlet failed\n",
		RunErr:    errors.New("buildah run: exit status 107"),
	}
	err := b.runInstallCommands(context.Background(), c,
		[][]string{{"zypper", "install", "-y", "ansible"}},
		container.RunModeContainer, "run", slog.Default())
	if err != nil {
		t.Fatalf("acceptable exit code should be tolerated: %v", err)
	}
}
