// Package main is the entry point for the image-build CLI tool.
// It provides commands for building container images using various package managers
// and publishing them to different destinations.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/containers/buildah"
	"github.com/spf13/cobra"
	"go.podman.io/storage/pkg/reexec"
	"go.podman.io/storage/pkg/unshare"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/backend/apt"
	"github.com/travisbcotton/image-build/internal/backend/dnf"
	"github.com/travisbcotton/image-build/internal/backend/mmdebstrap"
	"github.com/travisbcotton/image-build/internal/backend/zypper"
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/manifest"
	"github.com/travisbcotton/image-build/internal/publisher"
	"github.com/travisbcotton/image-build/internal/publisher/local"
	"github.com/travisbcotton/image-build/internal/publisher/registry"
	s3pub "github.com/travisbcotton/image-build/internal/publisher/s3"
	"github.com/travisbcotton/image-build/internal/publisher/squashfs"
)

// Global CLI flags that are shared across all subcommands
var (
	cfgPath      string   // Path to the YAML configuration file
	logLevel     string   // Logging level: debug, info, warn, error
	logFormat    string   // Logging format: json or text
	varFile      string   // Path to a variables file (yaml or json) used for templating
	vars         []string // Variable overrides in key=value format
	renderOutput string   // Output path for the render command (default: stdout)
	manifestPath string   // Path to a manifest file describing a DAG of layers
	layerName    string   // Layer name (within the manifest) to build
	skipIfExists bool     // Skip build when every configured publisher reports the image already exists
)

// rootCmd is the base command that is run when no subcommands are provided.
// It serves as the entry point for the CLI and holds all subcommands.
var rootCmd = &cobra.Command{
	Use:          "image-build",
	Short:        "Build OS images for multiple distros",
	SilenceUsage: true, // Don't show usage on errors during execution
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

// buildAllCmd builds every layer in a manifest in topological order.
//
// With just --manifest, the whole manifest is built. With --manifest and
// --layer, only the named layer and its transitive ancestors are built
// (i.e. its subtree of the DAG), so you can target a leaf without dragging
// in unrelated branches.
//
// Combined with --skip-if-exists, already-published parents short-circuit
// the per-layer Exists check, so re-running the same command is a fast
// no-op when nothing has changed.
//
// On the first layer that fails, the run aborts immediately (fail-fast).
var buildAllCmd = &cobra.Command{
	Use:   "build-all",
	Short: "Build every layer of a manifest in topological order",
	Long: `Build every layer of a manifest in dependency order.

Pass --manifest to build the entire manifest. Pass --manifest with --layer
to build only the named layer and its transitive ancestors. Combine with
--skip-if-exists to skip layers whose images are already published.

The run stops at the first failing layer.`,
	RunE: runBuildAll,
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

// versionCmd prints the version information for the image-build tool.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("image-build v0.1.0")
	},
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
//         and secret (env provided))
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

// setupLogger configures the global logger with the specified level and format.
//
// Parameters:
//   - level: Log level as string (debug, info, warn, error)
//   - format: Output format (json, text)
//
// The logger is set as the default slog logger and will be used by all packages.
// JSON format is recommended for production and parsing, while text is more human-readable.
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
		default:
			return fmt.Errorf("invalid log format %q", format)
	}

	slog.SetDefault(slog.New(handler))
	return nil
}

// init sets up the CLI command structure and flags.
// This runs before main() and configures all cobra commands and their flags.
func init() {
	// Persistent flags apply to all subcommands (root and children)
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "json", "log format (json, text)")

	// Build-specific flags
	buildCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	buildCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file")
	buildCmd.Flags().StringVar(&layerName, "layer", "", "layer name to build (requires --manifest)")
	buildCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	buildCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	buildCmd.Flags().BoolVar(&skipIfExists, "skip-if-exists", false, "skip the build when all publishers report the image already exists")

	// build-all-specific flags
	buildAllCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file (required)")
	buildAllCmd.Flags().StringVar(&layerName, "layer", "", "build only this layer and its transitive ancestors (default: build all)")
	buildAllCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	buildAllCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	buildAllCmd.Flags().BoolVar(&skipIfExists, "skip-if-exists", false, "skip layers whose publishers report the image already exists")

	// Validate-specific flags
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	validateCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	validateCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")

	// Render-specific flags. Mirrors build/build-all so users have one
	// flag pattern across subcommands: either --config standalone, or
	// --manifest + --layer for full manifest context with computed tags.
	renderCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config")
	renderCmd.Flags().StringVar(&manifestPath, "manifest", "", "path to manifest file (use with --layer)")
	renderCmd.Flags().StringVar(&layerName, "layer", "", "layer name in the manifest (requires --manifest)")
	renderCmd.Flags().StringVar(&varFile, "var-file", "", "path to variables file (yaml or json)")
	renderCmd.Flags().StringArrayVar(&vars, "var", nil, "variable override in key=value format")
	renderCmd.Flags().StringVarP(&renderOutput, "output", "o", "", "output file (default: stdout)")

	// Register all subcommands under the root command
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(buildAllCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(renderCmd)
	rootCmd.AddCommand(versionCmd)
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
	ctx := context.Background()

	// Configure logging first so we can log everything else
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	// Validate mutually-exclusive flag combinations. Either a single config
	// file is provided, or a manifest + layer pair driving a manifest-based
	// build — never both.
	if manifestPath != "" && cfgPath != "" {
		return fmt.Errorf("--config and --manifest are mutually exclusive")
	}
	if manifestPath != "" && layerName == "" {
		return fmt.Errorf("--layer is required when using --manifest")
	}
	if layerName != "" && manifestPath == "" {
		return fmt.Errorf("--manifest is required when using --layer")
	}

	// Always load vars (possibly empty). Templating is supported in both
	// single-config and manifest modes.
	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	// Manifest mode: delegate to the shared per-layer builder so build and
	// build-all share exactly the same per-layer behaviour.
	if manifestPath != "" {
		dag, err := loadDAG(manifestPath)
		if err != nil {
			return err
		}
		layer, err := dag.Get(layerName)
		if err != nil {
			return fmt.Errorf("get layer: %w", err)
		}
		return buildLayer(ctx, dag, layer, cliVars, cliGlobalVarFiles(), skipIfExists)
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

	bldr := builder.New(ctx, cfg, b, p)
	bldr.SetSkipIfExists(skipIfExists)
	return bldr.Build(ctx)
}

// runBuildAll iterates a manifest in topological order and builds each layer
// with the same per-layer code path runBuild uses in manifest mode. When
// --layer is provided, the iteration is narrowed to that layer's transitive
// ancestor subtree (the layer plus everything it depends on).
//
// Fails fast on the first layer error.
func runBuildAll(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}
	if manifestPath == "" {
		return fmt.Errorf("--manifest is required for build-all")
	}

	cliVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	dag, err := loadDAG(manifestPath)
	if err != nil {
		return err
	}

	order, err := buildOrder(dag, layerName)
	if err != nil {
		return err
	}

	globalVarFiles := cliGlobalVarFiles()
	log := slog.With("component", "build-all")
	log.Info("building layers in order",
		"count", len(order),
		"order", layerNames(order),
		"skip_if_exists", skipIfExists)

	for _, layer := range order {
		if err := buildLayer(ctx, dag, layer, cliVars, globalVarFiles, skipIfExists); err != nil {
			return fmt.Errorf("layer %s: %w", layer.Name, err)
		}
	}
	return nil
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

// buildOrder returns the layers to build, in dependency order. When target is
// empty, every layer in the DAG is returned; otherwise only target and its
// transitive ancestors are returned, still in dependency order (root first).
func buildOrder(dag *manifest.DAG, target string) ([]*manifest.Layer, error) {
	if target == "" {
		return dag.TopologicalSort()
	}
	ancestors, err := dag.Ancestors(target)
	if err != nil {
		return nil, fmt.Errorf("ancestors of %s: %w", target, err)
	}
	leaf, err := dag.Get(target)
	if err != nil {
		return nil, fmt.Errorf("get layer %s: %w", target, err)
	}
	return append(ancestors, leaf), nil
}

// layerNames is a small helper for logging — pulls just the names from a
// slice of layers so structured logs stay readable.
func layerNames(layers []*manifest.Layer) []string {
	out := make([]string, len(layers))
	for i, l := range layers {
		out[i] = l.Name
	}
	return out
}

// cliGlobalVarFiles returns the CLI-supplied --var-file as a slice (or empty
// when the flag wasn't given), in the shape ComputeBuildVars expects.
func cliGlobalVarFiles() []string {
	if varFile == "" {
		return nil
	}
	return []string{varFile}
}

// buildLayer runs the full per-layer pipeline for a single manifest layer:
// load layer-specific vars, inject computed tags, render+validate config,
// construct backend and publishers, and run the build. Used by both runBuild
// (manifest mode) and runBuildAll, so behaviour stays in lockstep.
func buildLayer(
	ctx context.Context,
	dag *manifest.DAG,
	layer *manifest.Layer,
	cliVars map[string]interface{},
	globalVarFiles []string,
	skipIfExists bool,
) error {
	configPath, mergedVars, err := prepareLayerRender(dag, layer.Name, cliVars, globalVarFiles)
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

	bldr := builder.New(ctx, cfg, b, p)
	bldr.SetSkipIfExists(skipIfExists)
	return bldr.Build(ctx)
}

// prepareLayerRender resolves the inputs needed to render a manifest layer's
// template: the config path to feed RenderConfig / LoadConfigWithVars, and
// the merged variable map containing CLI vars, the layer's own var files,
// and computed build vars (this layer's tag plus parent/ancestor tags).
//
// Used by buildLayer's prelude and by runRender in manifest mode, so the
// rendered output you preview matches exactly what build will see.
func prepareLayerRender(
	dag *manifest.DAG,
	layerName string,
	cliVars map[string]interface{},
	globalVarFiles []string,
) (string, map[string]interface{}, error) {
	layer, err := dag.Get(layerName)
	if err != nil {
		return "", nil, fmt.Errorf("get layer: %w", err)
	}

	// Layer-specific var files have lower priority than CLI vars.
	layerVars, err := config.LoadVars(layer.VarFiles, nil)
	if err != nil {
		return "", nil, fmt.Errorf("load layer vars: %w", err)
	}
	mergedVars := config.MergeVars(layerVars, cliVars)

	// Inject computed tags (this layer + transitive ancestors) so templates
	// can reference parent images by their deterministic tags.
	buildVars, err := manifest.ComputeBuildVars(dag, layerName, globalVarFiles)
	if err != nil {
		return "", nil, fmt.Errorf("compute build vars: %w", err)
	}
	mergedVars = config.MergeVars(mergedVars, buildVars)

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
func runValidate(cmd *cobra.Command, args []string) error {
	// Setup logging
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	// Load any provided vars (possibly empty) and render+validate the config.
	mergedVars, err := config.LoadVars([]string{varFile}, vars)
	if err != nil {
		return fmt.Errorf("load vars: %w", err)
	}

	cfg, err := config.LoadConfigWithVars(cfgPath, mergedVars)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Verify the backend is supported
	if _, err := newBackend(cfg.Layer.Manager); err != nil {
		return fmt.Errorf("invalid backend: %w", err)
	}

	// Log success message
	slog.Info("config is valid", "path", cfgPath)
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
// --manifest. Same shape as build/build-all so users don't have to learn a
// second flag pattern.
func runRender(cmd *cobra.Command, args []string) error {
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	if manifestPath != "" && cfgPath != "" {
		return fmt.Errorf("--config and --manifest are mutually exclusive")
	}
	if manifestPath != "" && layerName == "" {
		return fmt.Errorf("--layer is required when using --manifest")
	}
	if layerName != "" && manifestPath == "" {
		return fmt.Errorf("--manifest is required when using --layer")
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
		renderConfigPath, mergedVars, err = prepareLayerRender(
			dag, layerName, cliVars, cliGlobalVarFiles(),
		)
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
		slog.Info("rendered config written", "path", renderOutput)
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
		log.Fatal(err)
	}
}
