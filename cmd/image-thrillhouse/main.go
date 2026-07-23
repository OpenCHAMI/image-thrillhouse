// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package main is the entry point for the image-thrillhouse CLI tool.
// It provides commands for building container images using various package managers
// and publishing them to different destinations.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"go.podman.io/buildah"
	"go.podman.io/storage/pkg/reexec"
	"go.podman.io/storage/pkg/unshare"

	"github.com/travisbcotton/image-thrillhouse/internal/backend"
	"github.com/travisbcotton/image-thrillhouse/internal/backend/apt"
	"github.com/travisbcotton/image-thrillhouse/internal/backend/dnf"
	"github.com/travisbcotton/image-thrillhouse/internal/backend/mmdebstrap"
	"github.com/travisbcotton/image-thrillhouse/internal/backend/zypper"
	"github.com/travisbcotton/image-thrillhouse/internal/builder"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/manifest"
	"github.com/travisbcotton/image-thrillhouse/internal/promote"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher/local"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher/registry"
	s3pub "github.com/travisbcotton/image-thrillhouse/internal/publisher/s3"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher/squashfs"
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

	// promote-specific flags
	releaseTag   string // Human-readable tag to publish under (e.g. release-0.0.1)
	forcePromote bool   // Overwrite an existing release artifact instead of failing
	dryRun       bool   // Resolve and print actions without contacting the registry
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
	Short: "Retag a tested image under a release tag",
	Long: `Promote an already-built, already-tested image to a human-readable
release tag within its registry, without rebuilding it.

The layer's content-addressed image is copied to the release tag (the blobs
already exist, so only a new tag is written). For a multi-arch layer, omitting
--arch assembles an OCI image index over all arches under one release tag;
passing --arch retags a single arch (e.g. release-0.0.1-aarch64).

The content tag is recomputed from the manifest, so promote must run from the
same checkout that built the image.`,
	RunE: runPromote,
}

// newBackend creates the appropriate package manager backend based on the configuration.
// Each backend knows how to install packages using its specific package manager.
//
// Supported backends:
//   - dnf: Red Hat, Rocky, AlmaLinux, Fedora (supports scratch and parent builds)
//   - zypper: openSUSE, SLES (supports scratch and parent builds)
//   - apt: Debian, Ubuntu (only parent builds, use mmdebstrap for scratch)
//   - mmdebstrap: Debian, Ubuntu (only scratch builds using debootstrap)
//
// Returns an error if the package manager name is not recognized or if options are invalid.
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

// newPublishers creates a list of publishers based on the configuration.
// Publishers determine where the built image will be stored or uploaded.
//
// If no publishers are specified in the config, defaults to local publisher
// which commits the image to the local container storage.
//
// Supported publisher types:
//   - local: Commit to local podman/buildah storage
//   - squashfs: Create a SquashFS filesystem image (requires path)
//   - registry: Push to OCI container registry (requires url)
//   - s3: Upload to S3-compatible storage (requires url, bucket, access (env provided)
//     and secret (env provided))
//
// Returns an error if a publisher type is not supported or missing required fields.
func newPublishers(publishes []config.Publish) ([]publisher.Publisher, error) {
	// Default to local publisher if none specified
	if len(publishes) == 0 {
		return []publisher.Publisher{local.New()}, nil
	}

	var publishers []publisher.Publisher
	for _, p := range publishes {
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
			if p.URL == "" {
				return nil, fmt.Errorf("s3 publisher requires url")
			}
			if p.Bucket == "" {
				return nil, fmt.Errorf("s3 publisher requires bucket")
			}
			// Get S3 credentials from environment variables
			accessKey := os.Getenv("S3_ACCESS")
			secretKey := os.Getenv("S3_SECRET")
			if accessKey == "" || secretKey == "" {
				return nil, fmt.Errorf("s3 publisher requires S3_ACCESS and S3_SECRET environment variables")
			}
			publishers = append(publishers, s3pub.New(p.URL, p.Bucket, p.Prefix, accessKey, secretKey))
		default:
			return nil, fmt.Errorf("unsupported publisher type: %s", p.Type)
		}
	}
	return publishers, nil
}

// runPromote implements the promote command: OCI -> OCI retag within a registry.
//
// It recomputes the layer's content tag from the manifest and resolves the
// registry source from the layer's own publish block, then:
//   - multi-arch layer, no --arch: assemble an OCI image index over all arches
//     under one release tag.
//   - otherwise: copy the single content-tagged image to the release tag.
func runPromote(cmd *cobra.Command, args []string) error {
	ctx, stop := buildContext()
	defer stop()

	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	// Manifest-driven: the content tag to promote is recomputed from the
	// manifest, so a single --config has nothing to compute against.
	if manifestPath == "" || layerName == "" {
		return fmt.Errorf("--manifest and --layer are required")
	}
	if releaseTag == "" {
		return fmt.Errorf("--release is required")
	}

	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	dag, err := loadDAG(manifestPath)
	if err != nil {
		return err
	}

	log := slog.With("component", "cli")

	// Multi-arch with no --arch means "one release tag for all arches": assemble
	// an OCI image index. An explicit --arch (or a single-arch manifest) takes
	// the per-arch retag path.
	if dag.IsMultiArch() && archName == "" {
		return runPromoteIndex(ctx, dag, cliVars, log)
	}

	concreteName, err := resolveManifestLayer(dag)
	if err != nil {
		return err
	}
	src, err := resolveRegistrySource(dag, cliVars, concreteName)
	if err != nil {
		return err
	}

	log.Info("promote resolved",
		"layer", concreteName,
		"content_tag", src.Tag,
		"source", src.Ref(),
		"dest", src.RefWithTag(releaseTag),
		"force", forcePromote,
	)
	if dryRun {
		log.Info("dry-run: skipping retag")
		return nil
	}
	return promote.RetagRegistry(ctx, src, releaseTag, forcePromote)
}

// resolveRegistrySource recomputes the content tag for concreteName, renders its
// config, and returns the registry source the promotion reads from. The source
// is always the layer's registry publish block — the canonical artifact store.
func resolveRegistrySource(dag *manifest.DAG, cliVars map[string]interface{}, concreteName string) (promote.RegistrySource, error) {
	tags, err := dag.ComputeTags(concreteName, cliVars)
	if err != nil {
		return promote.RegistrySource{}, fmt.Errorf("compute tags: %w", err)
	}
	configPath, mergedVars, err := prepareLayerRender(dag, concreteName, cliVars)
	if err != nil {
		return promote.RegistrySource{}, err
	}
	cfg, err := config.LoadConfigWithVars(configPath, mergedVars)
	if err != nil {
		return promote.RegistrySource{}, err
	}
	regPub, err := promote.FindPublish(cfg.Publish, "registry")
	if err != nil {
		return promote.RegistrySource{}, fmt.Errorf("resolve source: %w", err)
	}
	tlsVerify := true
	if regPub.TLSVerify != nil {
		tlsVerify = *regPub.TLSVerify
	}
	return promote.RegistrySource{
		URL:       regPub.URL,
		Name:      cfg.Meta.Name,
		Tag:       tags[concreteName],
		TLSVerify: tlsVerify,
	}, nil
}

// runPromoteIndex assembles a single release tag over every arch of a
// multi-arch layer as an OCI image index. Each arch's single-arch image must
// already live in one shared repository (url/name); the index references them
// by digest, which is only valid within a repo, so a per-arch repo split is a
// hard error pointing the user at --arch (per-arch tags) instead.
func runPromoteIndex(ctx context.Context, dag *manifest.DAG, cliVars map[string]interface{}, log *slog.Logger) error {
	arches := dag.ArchesFor(layerName)
	if len(arches) == 0 {
		return fmt.Errorf("layer %q does not build for multiple arches", layerName)
	}

	var (
		url, name string
		tlsVerify bool
		members   []promote.IndexMember
	)
	for i, arch := range arches {
		concrete := layerName + "-" + arch
		src, err := resolveRegistrySource(dag, cliVars, concrete)
		if err != nil {
			return fmt.Errorf("arch %s: %w", arch, err)
		}
		if i == 0 {
			url, name, tlsVerify = src.URL, src.Name, src.TLSVerify
		} else if src.URL != url || src.Name != name {
			return fmt.Errorf(
				"image index requires all arches in one repository, but arch %q resolves to %s/%s while %q resolves to %s/%s; "+
					"use a shared registry url/name, or promote per-arch with --arch",
				arch, src.URL, src.Name, arches[0], url, name)
		}
		members = append(members, promote.IndexMember{Arch: arch, Tag: src.Tag})
	}

	log.Info("promote resolved",
		"mode", "index",
		"layer", layerName,
		"repo", fmt.Sprintf("%s/%s", url, name),
		"release", releaseTag,
		"arches", arches,
		"force", forcePromote,
	)
	if dryRun {
		log.Info("dry-run: skipping index assembly")
		return nil
	}
	return promote.RetagIndex(ctx, url, name, releaseTag, members, tlsVerify, forcePromote)
}

// setupLogger configures the global logger with the specified level and format.
//
// Parameters:
//   - level: Log level as string (debug, info, warn, error)
//   - format: Output format (json, text, textblock)
//
// The logger is set as the default slog logger and will be used by all packages.
// JSON format is recommended for production and parsing, while text is more human-readable,
// and textblock formats all output in human-readable blocks.
//
// This function also configures the logrus logger used by buildah and other
// container libraries. Their logs are pinned at WARN regardless of the app
// log level: --log-level debug means "tell me more about my build", not
// "dump every bind mount and blob-cache lookup buildah performs". Users who
// actually need the container-runtime firehose (typically developers
// debugging storage/isolation issues) opt in explicitly with
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

	// Configure logrus (used by buildah and container libraries).
	// Deliberately NOT tied to the app log level: at WARN the libraries
	// still surface real problems, but their internal chatter (bind
	// mounts, overlay mount_data, blob-cache lookups, OCI manifest dumps)
	// stays out of --log-level debug output. Note the logrus level also
	// propagates to buildah's chroot reexec child via LOGLEVEL, so this
	// single knob controls both the parent and child firehoses.
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
	promoteCmd.Flags().StringVar(&layerName, "layer", "", "logical layer name to promote (required)")
	promoteCmd.Flags().StringVar(&archName, "arch", "", "target architecture (multi-arch manifests only; defaults to host arch)")
	promoteCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	promoteCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	promoteCmd.Flags().StringVar(&releaseTag, "release", "", "release tag to publish under, e.g. release-0.0.1 (required)")
	promoteCmd.Flags().BoolVar(&forcePromote, "force", false, "overwrite an existing release tag instead of failing")
	promoteCmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and print actions without contacting the registry")

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

// runBuild is the main execution function for the build command.
// It orchestrates the entire image building process:
//  1. Setup logging
//  2. Load and validate configuration
//  3. Create appropriate backend (package manager)
//  4. Create publishers (destinations for the image)
//  5. Execute the build
//  6. Publish to configured destinations
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

	p, err := newPublishers(cfg.Publish)
	if err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	bldr := builder.New(cfg, cfgPath, b, p)
	bldr.SetSkipIfExists(skipIfExists)
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

	p, err := newPublishers(cfg.Publish)
	if err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	bldr := builder.New(cfg, configPath, b, p)
	bldr.SetSkipIfExists(skipIfExists)
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

// runValidate validates a configuration file without building the image.
// This is useful for:
//   - CI/CD pipelines to catch errors early
//   - Quick syntax checking during development
//   - Ensuring backend/publisher types are supported
//
// It checks:
//   - YAML syntax and structure
//   - Required fields are present
//   - Package manager backend is supported
//   - Publisher configuration is valid
//
// Supports the same input modes as build/render: standalone --config OR
// --manifest + --layer. Manifest mode loads the layer-specific var files and
// injects computed tags before rendering, so "validate" gives the same answer
// build would for that layer.
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

	cfg, err := config.LoadConfigWithVars(validateConfigPath, mergedVars)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
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

// main is the application entry point.
// It handles buildah/podman reexec initialization and user namespace setup,
// then executes the cobra CLI.
//
// The reexec and unshare calls are necessary for:
//   - Buildah's internal operations that need to re-execute the binary
//   - Rootless container operations using user namespaces
//
// These must be called before any other operations.
func main() {
	// Handle buildah storage reexec - this allows the storage library
	// to re-execute itself for certain privileged operations
	if reexec.Init() {
		return
	}

	// Handle buildah reexec - this allows buildah to re-execute itself
	// for container operations that need different privileges
	if buildah.InitReexec() {
		return
	}

	// Setup user namespace for rootless operation
	// This allows running containers without root privileges
	unshare.MaybeReexecUsingUserNamespace(false)

	// Execute the CLI and handle any errors
	if err := rootCmd.Execute(); err != nil {
		// Print the error to stderr in a simple format
		// We've already set SilenceErrors: true on rootCmd so Cobra won't print it
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
