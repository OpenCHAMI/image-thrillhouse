// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package main is the entry point for the image-thrillhouse CLI tool.
// It provides commands for building container images using various package managers
// and publishing them to different destinations.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"go.podman.io/buildah"
	"go.podman.io/storage/pkg/reexec"
	"go.podman.io/storage/pkg/unshare"

	"github.com/openchami/image-thrillhouse/internal/backend"
	"github.com/openchami/image-thrillhouse/internal/backend/apt"
	"github.com/openchami/image-thrillhouse/internal/backend/dnf"
	"github.com/openchami/image-thrillhouse/internal/backend/mmdebstrap"
	"github.com/openchami/image-thrillhouse/internal/backend/zypper"
	"github.com/openchami/image-thrillhouse/internal/builder"
	"github.com/openchami/image-thrillhouse/internal/config"
	"github.com/openchami/image-thrillhouse/internal/container"
	"github.com/openchami/image-thrillhouse/internal/manifest"
	"github.com/openchami/image-thrillhouse/internal/promote"
	"github.com/openchami/image-thrillhouse/internal/publisher"
	"github.com/openchami/image-thrillhouse/internal/publisher/local"
	"github.com/openchami/image-thrillhouse/internal/publisher/registry"
	s3pub "github.com/openchami/image-thrillhouse/internal/publisher/s3"
	"github.com/openchami/image-thrillhouse/internal/publisher/squashfs"
)

// Global CLI flags that are shared across all subcommands
var (
	cfgPath        string   // Path to the YAML configuration file
	logLevel       string   // Logging level: debug, info, warn, error
	logFormat      string   // Logging format: json or text
	containerDebug bool     // Enable debug logging from buildah/containers-storage internals
	varFile        string   // Path to a variables file (yaml or json) used for templating
	vars           []string // Variable overrides in key=value format
	renderOutput   string   // Output path for the render command (default: stdout)
	manifestPath   string   // Path to a manifest file describing a DAG of layers
	layerName      string   // Layer name (within the manifest) to build
	archName       string   // Target architecture for a multi-arch manifest build (defaults to host arch)
	skipIfExists   bool     // Skip build when every configured publisher reports the image already exists
	pruneParent    bool     // Remove the base image after the build when this run pulled it

	// promote-specific flags
	releaseTag   string   // Human-readable tag to publish under (e.g. release-0.0.1)
	toTypes      []string // Promotion targets: registry (retag) and/or s3 (materialize)
	forcePromote bool     // Overwrite an existing release artifact instead of failing
	dryRun       bool     // Resolve and print actions without contacting the target
)

// canonicalHostArch returns the arch name the manifest is likely to use
// for the current host. runtime.GOARCH speaks the Go idiom ("amd64",
// "arm64") but manifests and package repositories use the RPM/dpkg names
// ("x86_64", "aarch64"). We map the two ubiquitous cases and pass
// anything else through unchanged — if the user is on a niche arch and
// names it with a distro convention we don't know, they can still set
// --arch explicitly.
func canonicalHostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i386"
	default:
		return runtime.GOARCH
	}
}

// validateManifestFlags rejects nonsensical --config/--manifest/--layer/--arch
// combinations before any file I/O happens. Shared by build, validate,
// and render so all three surface the same error text.
func validateManifestFlags() error {
	if manifestPath != "" && cfgPath != "" {
		return fmt.Errorf("--config and --manifest are mutually exclusive")
	}
	if manifestPath != "" && layerName == "" {
		return fmt.Errorf("--layer is required when using --manifest")
	}
	if layerName != "" && manifestPath == "" {
		return fmt.Errorf("--manifest is required when using --layer")
	}
	if archName != "" && manifestPath == "" {
		return fmt.Errorf("--arch is only meaningful with --manifest")
	}
	return nil
}

// resolveManifestLayer maps the CLI --layer/--arch pair to a concrete DAG
// layer name. When the manifest has an architectures block and --arch was
// not supplied, we fall back to the canonicalised host arch — dag.Resolve
// still produces a helpful error listing the manifest's declared arches
// when the host arch isn't one of them.
func resolveManifestLayer(dag *manifest.DAG) (string, error) {
	effectiveArch := archName
	if effectiveArch == "" && dag.IsMultiArch() {
		effectiveArch = canonicalHostArch()
	}
	return dag.Resolve(layerName, effectiveArch)
}

// rootCmd is the base command that is run when no subcommands are provided.
// It serves as the entry point for the CLI and holds all subcommands.
var rootCmd = &cobra.Command{
	Use:           "image-thrillhouse",
	Short:         "Build OS images for multiple distros",
	SilenceUsage:  true, // Don't show usage on errors during execution
	SilenceErrors: true, // Don't let Cobra print errors (we handle them ourselves)
}

// buildCmd builds a container image from the provided configuration file.
// This is the primary command for creating new images.
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build an image layer from a config file",
	Long: `Build an OS image using the specified configuration file.

The configuration defines:
  - Base image to build from (scratch or existing image)
  - Package manager and repositories
  - Packages and package groups to install
  - Commands to run during build
  - Publishing destinations (local, squashfs, registry, s3)`,
	RunE: runBuild,
}

// validateCmd validates a configuration file without actually building the image.
// This is useful for CI/CD pipelines and quick syntax checking.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a config file without building",
	Long: `Validate the syntax and structure of a configuration file.

This checks:
  - YAML syntax is correct
  - Required fields are present
  - Package manager is supported
  - Publisher types are valid`,
	RunE: runValidate,
}

// renderCmd renders a config file template against the provided variables
// and prints (or writes) the result without executing a build.
var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a config file template and print the result",
	RunE:  runRender,
}

// version is the release version stamped at build time via
//
//	go build -ldflags "-X main.version=<version>"
//
// (see the Makefile, which passes its VERSION variable through). The default
// here only shows up for plain `go build` / `go run` invocations.
var version = "dev"

// versionCmd prints the version information for the image-thrillhouse tool.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("image-thrillhouse %s\n", version)
	},
}

// promoteCmd promotes an already-built, already-tested artifact to a
// human-readable release tag without rebuilding it.
var promoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a tested image to a release tag",
	Long: `Promote an already-built, already-tested image to a human-readable
release tag, without rebuilding it. The content tag is recomputed from the
manifest, so promote must run from the same checkout that built the image.

--to registry (default): retag within the registry. The content-addressed image
is copied to the release tag (blobs already exist, so only a new tag is written).

--to s3: project the image into S3 boot artifacts (rootfs/kernel/initramfs) under
the release tag, laid out as <prefix><release>/<arch>/. Pulls the content-tagged
image and re-extracts it — a re-package of tested bytes, not a rebuild.

--to is repeatable (--to registry --to s3), and each requested type promotes to
every matching publish block the layer declares — so a layer with one registry
and two s3 blocks reaches all three destinations in one invocation. A requested
type the layer has no block for is skipped; destinations are attempted
independently and a failure of one does not withhold the others, but any failure
makes the command exit non-zero.

Omitting --layer promotes every layer in the manifest that declares a publish
block of a requested type, so a whole release is one command. Mark a block
promote-only: true to declare a destination that only promote writes (build
skips it) — typically the s3 block on release targets.

For a multi-arch layer, omitting --arch promotes every arch; passing --arch
promotes just that one.`,
	RunE: runPromote,
}

// newBackend constructs the package-manager backend named by the config and
// runs its option validation. See internal/backend for which backends support
// scratch vs parent builds.
func newBackend(manager config.Manager) (backend.Backend, error) {
	var b backend.Backend

	switch manager.Name {
	case "dnf":
		b = dnf.New(manager.Options)
	case "mmdebstrap":
		b = mmdebstrap.New(manager.Options)
	case "apt":
		b = apt.New(manager.Options)
	case "zypper":
		b = zypper.New(manager.Options)
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager.Name)
	}

	// Validate backend-specific options
	if err := b.ValidateOptions(manager.Options); err != nil {
		return nil, fmt.Errorf("invalid options for %s backend: %w", manager.Name, err)
	}

	return b, nil
}

// newPublishers constructs the publishers a build should write to. Structural
// validation of each block happens in config.Publish.Validate; what's left here
// is construction plus the credential lookup that only build-time needs.
func newPublishers(publishes []config.Publish, arch string) ([]publisher.Publisher, error) {
	// Drop promote-only blocks: they describe where `promote` materializes to,
	// not somewhere `build` writes. Filtering here (rather than in each case)
	// also keeps them out of the skip-if-exists probe.
	buildTargets := make([]config.Publish, 0, len(publishes))
	for _, p := range publishes {
		if p.PromoteOnly {
			continue
		}
		buildTargets = append(buildTargets, p)
	}

	// Default to local publisher when nothing is left to publish at build time
	// (none configured, or all of them promote-only) so the image still lands
	// somewhere for child layers to build on.
	if len(buildTargets) == 0 {
		return []publisher.Publisher{local.New()}, nil
	}

	var publishers []publisher.Publisher
	for _, p := range buildTargets {
		switch p.Type {
		case "local":
			publishers = append(publishers, local.New())
		case "squashfs":
			if p.Path == "" {
				return nil, fmt.Errorf("squashfs publisher requires path")
			}
			publishers = append(publishers, squashfs.New(p.Path))
		case "registry":
			if p.URL == "" {
				return nil, fmt.Errorf("registry publisher requires url")
			}
			tlsVerify := true
			if p.TLSVerify != nil {
				tlsVerify = *p.TLSVerify
			}
			publishers = append(publishers, registry.New(p.URL, tlsVerify))
		case "s3":
			pub, err := newS3Publisher(p, arch)
			if err != nil {
				return nil, err
			}
			publishers = append(publishers, pub)
		default:
			return nil, fmt.Errorf("unsupported publisher type: %s", p.Type)
		}
	}
	return publishers, nil
}

// newS3Publisher constructs an S3 publisher from a publish block for the given
// arch, reading credentials from S3_ACCESS/S3_SECRET. Shared by build's
// newPublishers and promote's OCI->S3 path so required-field validation, the
// key layout, and credential handling stay in one place.
func newS3Publisher(p config.Publish, arch string) (*s3pub.S3Publisher, error) {
	if p.URL == "" {
		return nil, fmt.Errorf("s3 publisher requires url")
	}
	if p.Bucket == "" {
		return nil, fmt.Errorf("s3 publisher requires bucket")
	}
	accessKey := os.Getenv("S3_ACCESS")
	secretKey := os.Getenv("S3_SECRET")
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("s3 publisher requires S3_ACCESS and S3_SECRET environment variables")
	}
	return s3pub.New(p.URL, p.Bucket, p.Prefix, arch, accessKey, secretKey), nil
}

// resolveTargetTypes validates --to and de-duplicates it, preserving the order
// the flags were given so logs and the promotion sequence follow what the user
// typed. An absent flag means the historical default of registry only.
func resolveTargetTypes(flags []string) ([]string, error) {
	if len(flags) == 0 {
		return []string{"registry"}, nil
	}
	out := make([]string, 0, len(flags))
	seen := make(map[string]struct{}, len(flags))
	for _, t := range flags {
		if t != "registry" && t != "s3" {
			return nil, fmt.Errorf("--to %q is not supported (use registry or s3)", t)
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out, nil
}

// runPromote implements the promote command: re-tag or re-project already-built
// artifacts under a release tag.
//
// It recomputes each layer's content tag from the manifest and resolves the
// registry source from the layer's own publish blocks, then promotes to every
// destination matching every requested --to type.
func runPromote(cmd *cobra.Command, args []string) error {
	ctx, stop := buildContext()
	defer stop()

	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	// Manifest-driven: the content tag to promote is recomputed from the
	// manifest, so a single --config has nothing to compute against.
	if manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	if releaseTag == "" {
		return fmt.Errorf("--release is required")
	}
	targets, err := resolveTargetTypes(toTypes)
	if err != nil {
		return err
	}
	toTypes = targets

	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	dag, err := loadDAG(manifestPath)
	if err != nil {
		return err
	}

	log := slog.With("component", "cli")

	wanted := strings.Join(toTypes, ", ")

	// A named --layer promotes just that one. Omitting it walks the whole
	// manifest and promotes every layer declaring a publish block of one of the
	// target types — so which layers reach s3 is a config decision (promote-only
	// s3 blocks on release targets), not something the pipeline has to enumerate.
	if layerName != "" {
		err := promoteLayer(ctx, dag, cliVars, layerName, false, log)
		if errors.Is(err, errNoTarget) {
			return fmt.Errorf("layer %q declares no %s publish block", layerName, wanted)
		}
		return err
	}

	// Promotion is best-effort across layers as well as across destinations: one
	// unreachable endpoint must not stop the rest of a release from going out.
	// Failures are collected and reported together, and any of them makes the
	// command exit non-zero.
	// attempted counts layers that had somewhere to promote to, successful or
	// not: a destination that was tried and failed is reported as a failure, not
	// as "nothing to promote".
	attempted := 0
	var failures []error
	for _, name := range dag.LogicalNames() {
		err := promoteLayer(ctx, dag, cliVars, name, true, log)
		switch {
		case errors.Is(err, errNoTarget):
			log.Debug("skipping layer: no publish block for target", "layer", name, "targets", wanted)
		case err != nil:
			failures = append(failures, fmt.Errorf("layer %s: %w", name, err))
			attempted++
		default:
			attempted++
		}
	}
	if attempted == 0 {
		if archName != "" {
			return fmt.Errorf("nothing to promote: no layer declares a %s publish block for arch %q", wanted, archName)
		}
		return fmt.Errorf("nothing to promote: no layer declares a %s publish block", wanted)
	}
	return errors.Join(failures...)
}

// errNoTarget marks a layer that declares no publish block for the promotion
// target: skipped during a whole-manifest promote, an error when the layer was
// named explicitly with --layer.
var errNoTarget = errors.New("no publish block for promotion target")

// concreteLayersFor expands a logical layer name into the concrete DAG layers
// to promote: every arch it builds for, or just --arch when set. Returns an
// empty slice (and no error) when --arch excludes the layer entirely.
func concreteLayersFor(dag *manifest.DAG, logicalName string) ([]string, error) {
	if !dag.IsMultiArch() {
		if _, err := dag.Get(logicalName); err != nil {
			return nil, err
		}
		return []string{logicalName}, nil
	}
	arches := dag.ArchesFor(logicalName)
	if len(arches) == 0 {
		return nil, fmt.Errorf("unknown layer %q in manifest", logicalName)
	}
	if archName != "" {
		for _, a := range arches {
			if a == archName {
				return []string{logicalName + "-" + archName}, nil
			}
		}
		return nil, nil
	}
	out := make([]string, 0, len(arches))
	for _, a := range arches {
		out = append(out, logicalName+"-"+a)
	}
	return out, nil
}

// renderLayer computes a concrete layer's content tag and renders its config —
// the two things every promotion needs before it can resolve source or target.
func renderLayer(dag *manifest.DAG, cliVars map[string]interface{}, concreteName string) (*config.Config, string, error) {
	tags, err := dag.ComputeTags(concreteName, cliVars)
	if err != nil {
		return nil, "", fmt.Errorf("compute tags: %w", err)
	}
	configPath, mergedVars, err := prepareLayerRender(dag, concreteName, cliVars)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.LoadConfigWithVars(configPath, mergedVars)
	if err != nil {
		return nil, "", err
	}
	return cfg, tags[concreteName], nil
}

// registrySourceFrom builds the OCI source reference from a layer's first
// registry publish block — the canonical artifact store an OCI->S3
// materialization pulls from. A layer may publish to several registries; the
// pull needs exactly one, and the first block is the one build treats as
// primary.
func registrySourceFrom(cfg *config.Config, contentTag string) (promote.RegistrySource, error) {
	regPub, err := promote.FindPublish(cfg.Publish, "registry")
	if err != nil {
		return promote.RegistrySource{}, fmt.Errorf("resolve source: %w", err)
	}
	return registrySourceFromBlock(regPub, cfg.Meta.Name, contentTag), nil
}

// registrySourceFromBlock builds the OCI source reference for one specific
// registry publish block. A retag is a copy within a single repository, so each
// registry destination promotes from its own registry — the content tag is
// already there, pushed by the build that wrote that block.
func registrySourceFromBlock(block config.Publish, name, contentTag string) promote.RegistrySource {
	tlsVerify := true
	if block.TLSVerify != nil {
		tlsVerify = *block.TLSVerify
	}
	return promote.RegistrySource{
		URL:       block.URL,
		Name:      name,
		Tag:       contentTag,
		TLSVerify: tlsVerify,
	}
}

// promoteLayer promotes one logical layer — every arch it builds for, or just
// --arch when set — to every publish block matching every requested --to type.
// A layer with two registry blocks and two s3 blocks promoted with
// `--to registry --to s3` writes four destinations.
//
// All destinations are resolved up front so a config error fails before
// anything is written; promotions then run sequentially and best-effort, so an
// endpoint that is down does not withhold the release from the healthy ones.
// Every failure is collected and returned together.
//
// A requested type the layer declares no block for is skipped, not an error —
// that is what makes `--to registry --to s3` usable across a manifest where only
// some layers materialize to s3. Returns errNoTarget only when *no* requested
// type matched anything: skipped during a whole-manifest promote, an error when
// the layer was named explicitly.
func promoteLayer(ctx context.Context, dag *manifest.DAG, cliVars map[string]interface{}, logicalName string, bulk bool, log *slog.Logger) error {
	concretes, err := concreteLayersFor(dag, logicalName)
	if err != nil {
		return err
	}
	if len(concretes) == 0 {
		if bulk {
			return errNoTarget
		}
		return fmt.Errorf("layer %q does not build for arch %q", logicalName, archName)
	}

	type target struct {
		concrete string
		arch     string
		typ      string
		src      promote.RegistrySource
		cfg      *config.Config
		block    config.Publish
	}

	var targets []target
	seen := make(map[string]string, len(concretes)) // registry dest ref -> arch that claimed it
	for _, concrete := range concretes {
		cfg, contentTag, err := renderLayer(dag, cliVars, concrete)
		if err != nil {
			return err
		}
		layer, err := dag.Get(concrete)
		if err != nil {
			return err
		}
		for _, typ := range toTypes {
			blocks := promote.FindPublishAll(cfg.Publish, typ)
			if len(blocks) == 0 {
				// Not configured for this layer. A missing publisher is a skip;
				// only a configured-but-broken one is a failure.
				log.Debug("skipping target: no publish block", "layer", concrete, "target", typ)
				continue
			}
			for _, block := range blocks {
				// A registry retag copies within the destination registry, so
				// each one sources from itself. An s3 materialization has to
				// pull from somewhere: the layer's canonical registry.
				var src promote.RegistrySource
				if typ == "registry" {
					src = registrySourceFromBlock(block, cfg.Meta.Name, contentTag)
				} else {
					src, err = registrySourceFrom(cfg, contentTag)
					if err != nil {
						return err
					}
				}
				// A registry retag writes <repo>:<release>; two arches landing
				// on the same ref would silently clobber each other. S3 keys
				// embed the arch, so they can't collide.
				if typ == "registry" {
					dst := src.RefWithTag(releaseTag)
					if prev, ok := seen[dst]; ok {
						return fmt.Errorf(
							"arches %q and %q both retag to %s; the release tag can't distinguish them — "+
								"put the arch in meta.name (e.g. <image>-{{ .arch }}) so each arch has its own repo",
							prev, layer.Arch, dst)
					}
					seen[dst] = layer.Arch
				}
				targets = append(targets, target{
					concrete: concrete, arch: layer.Arch, typ: typ,
					src: src, cfg: cfg, block: block,
				})
			}
		}
	}
	if len(targets) == 0 {
		return errNoTarget
	}

	var failures []error
	for _, t := range targets {
		switch t.typ {
		case "registry":
			log.Info("promote resolved",
				"mode", "registry",
				"layer", t.concrete,
				"content_tag", t.src.Tag,
				"source", t.src.Ref(),
				"dest", t.src.RefWithTag(releaseTag),
				"force", forcePromote,
				"dry_run", dryRun,
			)
			if dryRun {
				continue
			}
			if err := promote.RetagRegistry(ctx, t.src, releaseTag, forcePromote); err != nil {
				log.Error("promote failed", "layer", t.concrete, "arch", t.arch,
					"dest", t.src.RefWithTag(releaseTag), "error", err)
				failures = append(failures, fmt.Errorf("arch %s -> %s: %w",
					t.arch, t.src.RefWithTag(releaseTag), err))
			}
		case "s3":
			log.Info("promote resolved",
				"mode", "s3",
				"layer", t.concrete,
				"content_tag", t.src.Tag,
				"source", t.src.Ref(),
				"release", releaseTag,
				"bucket", t.block.Bucket,
				"prefix", t.block.Prefix,
				"arch", t.arch,
				"force", forcePromote,
				"dry_run", dryRun,
			)
			if dryRun {
				continue
			}
			s3Dest := fmt.Sprintf("%s/%s/%s", t.block.URL, t.block.Bucket, t.block.Prefix)
			dst, err := newS3Publisher(t.block, t.arch)
			if err != nil {
				// A configured-but-invalid publisher is a failure, not a skip,
				// but it must not withhold the remaining destinations.
				log.Error("promote failed", "layer", t.concrete, "arch", t.arch, "dest", s3Dest, "error", err)
				failures = append(failures, fmt.Errorf("arch %s -> %s: %w", t.arch, s3Dest, err))
				continue
			}
			if err := promote.MaterializeToS3(ctx, t.src, dst, t.cfg.Meta.Name, releaseTag, forcePromote); err != nil {
				log.Error("promote failed", "layer", t.concrete, "arch", t.arch, "dest", s3Dest, "error", err)
				failures = append(failures, fmt.Errorf("arch %s -> %s: %w", t.arch, s3Dest, err))
			}
		}
	}
	if len(failures) > 0 {
		log.Error("promote finished with failures",
			"layer", logicalName, "attempted", len(targets), "failed", len(failures))
	}
	return errors.Join(failures...)
}

// setupLogger installs the default slog handler and configures the logrus logger
// that buildah and the container libraries use.
//
// The logrus level is deliberately NOT tied to --log-level: `--log-level debug`
// means "tell me more about my build", not "dump every bind mount and blob-cache
// lookup buildah performs". The container-runtime firehose is opt-in via
// --container-debug.
func setupLogger(level, format string) error {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", level, err)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	// Logs go to stderr so that subcommands which print user-facing data
	// (today: `render`, which writes the rendered YAML to stdout) stay
	// cleanly redirectable. Mixing log output and program output on the
	// same stream forces every consumer to either set --log-level=error
	// or strip log lines out of the result, neither of which scales.
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	case "textblock":
		handler = container.NewTextBlockHandler(os.Stderr, opts)
	default:
		return fmt.Errorf("invalid log format %q", format)
	}

	slog.SetDefault(slog.New(handler))
	container.SetLogFormat(format)

	// This level also propagates to buildah's chroot reexec child via LOGLEVEL,
	// so the one knob controls both the parent and child firehoses.
	switch {
	case containerDebug:
		logrus.SetLevel(logrus.DebugLevel)
	case lvl >= slog.LevelError:
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.WarnLevel)
	}
	logrus.SetOutput(os.Stderr)

	return nil
}

// init sets up the CLI command structure and flags.
// This runs before main() and configures all cobra commands and their flags.
func init() {
	// Persistent flags apply to all subcommands (root and children)
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "textblock", "log format (json, text, textblock)")
	rootCmd.PersistentFlags().BoolVar(&containerDebug, "container-debug", false, "enable debug output from the container runtime libraries (buildah, containers/storage); very verbose")

	// Build-specific flags
	buildCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	buildCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file")
	buildCmd.Flags().StringVar(&layerName, "layer", "", "logical layer name to build (requires --manifest)")
	buildCmd.Flags().StringVar(&archName, "arch", "", "target architecture (multi-arch manifests only; defaults to host arch)")
	buildCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	buildCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	buildCmd.Flags().BoolVar(&skipIfExists, "skip-if-exists", false, "skip the build when all publishers report the image already exists")
	buildCmd.Flags().BoolVar(&pruneParent, "prune-parent", false, "remove the base image from local storage after the build, if this run pulled it")

	// Validate-specific flags. Mirrors the build/render shape so users can
	// dry-run a manifest layer's rendered config — picking validate over
	// render when they only care about pass/fail.
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	validateCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file (use with --layer)")
	validateCmd.Flags().StringVar(&layerName, "layer", "", "logical layer name in the manifest (requires --manifest)")
	validateCmd.Flags().StringVar(&archName, "arch", "", "target architecture (multi-arch manifests only; defaults to host arch)")
	validateCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	validateCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")

	// Render-specific flags. Mirrors build so users have one flag pattern
	// across subcommands: either --config standalone, or --manifest +
	// --layer for full manifest context with computed tags.
	renderCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	renderCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file (use with --layer)")
	renderCmd.Flags().StringVar(&layerName, "layer", "", "logical layer name in the manifest (requires --manifest)")
	renderCmd.Flags().StringVar(&archName, "arch", "", "target architecture (multi-arch manifests only; defaults to host arch)")
	renderCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	renderCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	renderCmd.Flags().StringVarP(&renderOutput, "output", "o", "", "output file (default: stdout)")

	// Promote-specific flags. Manifest-driven like build (the content tag is
	// recomputed from the manifest), plus the release tag and source/target
	// selectors.
	promoteCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file (required)")
	promoteCmd.Flags().StringVar(&layerName, "layer", "", "logical layer name to promote (default: every layer declaring a block for --to)")
	promoteCmd.Flags().StringVar(&archName, "arch", "", "target architecture (multi-arch manifests only; defaults to host arch)")
	promoteCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	promoteCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	promoteCmd.Flags().StringVar(&releaseTag, "release", "", "release tag to publish under, e.g. release-0.0.1 (required)")
	// StringArray, not StringSlice: a declared default would be *appended* to
	// rather than replaced by the flag, so `--to s3` would silently mean
	// "registry and s3". The registry default is applied in resolveTargetTypes.
	promoteCmd.Flags().StringArrayVar(&toTypes, "to", nil, "promotion target: registry (retag) or s3 (materialize); repeatable (default registry)")
	promoteCmd.Flags().BoolVar(&forcePromote, "force", false, "overwrite an existing release (registry tag or s3 objects) instead of failing")
	promoteCmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and print actions without contacting the target")

	// Register all subcommands under the root command
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(renderCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(promoteCmd)
}

// buildContext returns the root context for a build, cancelled on SIGINT or
// SIGTERM so that deferred cleanup (container delete, unmount, store
// shutdown) still runs when a CI system cancels the job. Without this the
// process died mid-build and left mounted containers — and their helper
// processes — behind on the runner, which eventually exhausted the runner's
// process limit (pthread_create EAGAIN crashes in later builds).
//
// The first signal cancels the context and lets the build wind down. A
// second signal force-exits: cleanup is best-effort, and a CI runner about
// to SIGKILL us anyway shouldn't have to wait on a wedged unmount.
func buildContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		slog.Warn("received signal, cancelling build and cleaning up (send again to force exit)", "signal", sig)
		cancel()
		if _, ok := <-sigCh; ok {
			slog.Warn("received second signal, exiting immediately")
			os.Exit(1)
		}
	}()
	stop := func() {
		signal.Stop(sigCh)
		close(sigCh)
		cancel()
	}
	return ctx, stop
}

// runBuild resolves the config — either a standalone --config or a --manifest
// layer — and hands it to the builder.
func runBuild(cmd *cobra.Command, args []string) error {
	ctx, stop := buildContext()
	defer stop()

	// Configure logging first so we can log everything else
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	// Validate mutually-exclusive flag combinations. Either a single config
	// file is provided, or a manifest + layer pair driving a manifest-based
	// build — never both, and never neither.
	if err := validateManifestFlags(); err != nil {
		return err
	}
	if manifestPath == "" && cfgPath == "" {
		return fmt.Errorf("either --config or --manifest is required")
	}

	// Always load vars (possibly empty). Templating is supported in both
	// single-config and manifest modes.
	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	// Manifest mode: delegate to buildLayer so the per-layer flow stays in
	// one place (also shared with runRender's manifest branch).
	if manifestPath != "" {
		dag, err := loadDAG(manifestPath)
		if err != nil {
			return err
		}
		concreteName, err := resolveManifestLayer(dag)
		if err != nil {
			return err
		}
		layer, err := dag.Get(concreteName)
		if err != nil {
			return fmt.Errorf("get layer: %w", err)
		}
		return buildLayer(ctx, dag, layer, cliVars, skipIfExists)
	}

	// Single-config mode: no DAG, no tag injection, just render and build.
	cfg, err := config.LoadConfigWithVars(cfgPath, cliVars)
	if err != nil {
		return err
	}

	b, err := newBackend(cfg.Layer.Manager)
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	// Single-config build has no manifest arch; the S3 key layout omits the
	// arch segment.
	p, err := newPublishers(cfg.Publish, "")
	if err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	bldr := builder.New(cfg, cfgPath, b, p)
	bldr.SetSkipIfExists(skipIfExists)
	bldr.SetPruneParent(pruneParent)
	return bldr.Build(ctx)
}

// loadDAG is a thin wrapper that loads a manifest and constructs its DAG,
// surfacing both error stages with consistent prefixes.
func loadDAG(path string) (*manifest.DAG, error) {
	m, err := manifest.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	dag, err := manifest.NewDAG(m)
	if err != nil {
		return nil, fmt.Errorf("build dag: %w", err)
	}
	return dag, nil
}

// buildLayer runs the full per-layer pipeline for a single manifest layer:
// load layer-specific vars, inject computed tags, render+validate config,
// construct backend and publishers, and run the build. Used by runBuild's
// manifest branch — kept as a helper so the prelude can stay in lockstep
// with prepareLayerRender (which runRender's manifest branch reuses).
func buildLayer(
	ctx context.Context,
	dag *manifest.DAG,
	layer *manifest.Layer,
	cliVars map[string]interface{},
	skipIfExists bool,
) error {
	configPath, mergedVars, err := prepareLayerRender(dag, layer.Name, cliVars)
	if err != nil {
		return err
	}

	cfg, err := config.LoadConfigWithVars(configPath, mergedVars)
	if err != nil {
		return err
	}

	b, err := newBackend(cfg.Layer.Manager)
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	// layer.Arch drives the S3 key layout (<prefix><tag>/<arch>/...); it is ""
	// for single-arch manifests, which omits the arch segment.
	p, err := newPublishers(cfg.Publish, layer.Arch)
	if err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	bldr := builder.New(cfg, configPath, b, p)
	bldr.SetSkipIfExists(skipIfExists)
	bldr.SetPruneParent(pruneParent)
	return bldr.Build(ctx)
}

// prepareLayerRender resolves the inputs needed to render a manifest layer's
// template: the config path to feed RenderConfig / LoadConfigWithVars, and
// the merged variable map containing the layer's own var files, CLI vars,
// and computed build vars (this layer's tag plus direct-parent tags).
//
// Used by buildLayer's prelude and by runRender in manifest mode, so the
// rendered output you preview matches exactly what build will see.
func prepareLayerRender(
	dag *manifest.DAG,
	layerName string,
	cliVars map[string]interface{},
) (string, map[string]interface{}, error) {
	layer, err := dag.Get(layerName)
	if err != nil {
		return "", nil, fmt.Errorf("get layer: %w", err)
	}

	mergedVars, err := dag.RenderVars(layerName, cliVars)
	if err != nil {
		return "", nil, fmt.Errorf("compute render vars: %w", err)
	}

	return layer.Config, mergedVars, nil
}

// runValidate loads and validates a config without building it.
//
// It supports the same input modes as build (standalone --config, or --manifest
// + --layer) and resolves them the same way, so manifest mode gives the answer
// build would for that layer rather than a looser one.
func runValidate(cmd *cobra.Command, args []string) error {
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	if err := validateManifestFlags(); err != nil {
		return err
	}
	if manifestPath == "" && cfgPath == "" {
		return fmt.Errorf("either --config or --manifest is required")
	}

	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	// Resolve the config path + merged vars exactly the same way build does
	// for the chosen mode, so validate's answer matches what build would see.
	var (
		validateConfigPath string
		mergedVars         map[string]interface{}
	)
	if manifestPath != "" {
		dag, err := loadDAG(manifestPath)
		if err != nil {
			return err
		}
		concreteName, err := resolveManifestLayer(dag)
		if err != nil {
			return err
		}
		validateConfigPath, mergedVars, err = prepareLayerRender(dag, concreteName, cliVars)
		if err != nil {
			return err
		}
	} else {
		validateConfigPath = cfgPath
		mergedVars = cliVars
	}

	// No "invalid config" prefix here: ParseAndValidate already applies one,
	// and wrapping again produced "invalid config: invalid config: ...".
	cfg, err := config.LoadConfigWithVars(validateConfigPath, mergedVars)
	if err != nil {
		return err
	}

	// Verify the backend is supported
	if _, err := newBackend(cfg.Layer.Manager); err != nil {
		return fmt.Errorf("invalid backend: %w", err)
	}

	slog.With("component", "cli").Info("config is valid", "path", validateConfigPath)
	return nil
}

// runRender renders a config file template and writes the result to stdout
// (or to --output). It mirrors build's input model so the previewed YAML is
// exactly what a build would consume:
//
//   - Standalone: --config foo.yaml [--var-file vf] [--var k=v]
//     Renders foo.yaml with only the user-supplied vars. Templates that
//     reference manifest-injected vars like {{ .tag }} or {{ .parent_tag }}
//     must either avoid them or have the user supply them via --var.
//
//   - Manifest:   --manifest m.yaml --layer x [--var-file vf] [--var k=v]
//     Looks up layer x in the manifest, loads the layer's var files,
//     computes the build vars (this layer's hash tag + ancestor tags),
//     and renders the layer's referenced template. Useful for "what
//     will build actually run?" inspection.
//
// --config and --manifest are mutually exclusive; --layer requires
// --manifest. Same shape as build so users don't have to learn a second
// flag pattern.
func runRender(cmd *cobra.Command, args []string) error {
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	if err := validateManifestFlags(); err != nil {
		return err
	}
	if manifestPath == "" && cfgPath == "" {
		return fmt.Errorf("either --config or --manifest is required")
	}

	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	// Decide which template to render and which vars to apply.
	var (
		renderConfigPath string
		mergedVars       map[string]interface{}
	)
	if manifestPath != "" {
		dag, err := loadDAG(manifestPath)
		if err != nil {
			return err
		}
		concreteName, err := resolveManifestLayer(dag)
		if err != nil {
			return err
		}
		renderConfigPath, mergedVars, err = prepareLayerRender(dag, concreteName, cliVars)
		if err != nil {
			return err
		}
	} else {
		renderConfigPath = cfgPath
		mergedVars = cliVars
	}

	rendered, err := config.RenderConfig(renderConfigPath, mergedVars)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}

	if renderOutput != "" {
		if err := os.WriteFile(renderOutput, []byte(rendered), 0644); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		slog.With("component", "cli").Info("rendered config written", "path", renderOutput)
	} else {
		fmt.Print(rendered)
	}

	return nil
}

// main initialises buildah's reexec hooks and the rootless user namespace, then
// runs the CLI.
//
// The reexec calls must come first and must be able to return early: buildah and
// containers-storage re-execute this same binary for privileged operations, and
// those child invocations have to exit through Init rather than falling into the
// cobra command tree.
func main() {
	if reexec.Init() {
		return
	}
	if buildah.InitReexec() {
		return
	}

	unshare.MaybeReexecUsingUserNamespace(false)

	if err := rootCmd.Execute(); err != nil {
		// rootCmd sets SilenceErrors, so printing here is what surfaces it.
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
