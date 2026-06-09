// Tier 2 builder tests — methods with a small Container or Publisher mock
// surface. These exercise the "fan out a config slice into c.WriteFile calls"
// shape that several builder methods share, plus the publisher iteration in
// allExist. See fake_container_test.go for the test doubles.
package builder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
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
