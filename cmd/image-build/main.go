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
	"github.com/travisbcotton/image-build/internal/publisher"
	"github.com/travisbcotton/image-build/internal/publisher/local"
	"github.com/travisbcotton/image-build/internal/publisher/registry"
	s3pub "github.com/travisbcotton/image-build/internal/publisher/s3"
	"github.com/travisbcotton/image-build/internal/publisher/squashfs"
)

// Global command-line flags that are shared across all subcommands
var (
	cfgPath   string // Path to the YAML configuration file
	logLevel  string // Logging level: debug, info, warn, error
	logFormat string // Logging format: json or text
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
	Long: `Build a container image using the specified configuration file.

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
// Returns an error if the package manager name is not recognized.
func newBackend(manager config.Manager) (backend.Backend, error) {
	switch manager.Name {
	case "dnf":
		return dnf.New(manager.Options), nil
	case "mmdebstrap":
		return mmdebstrap.New(manager.Options), nil
	case "apt":
		return apt.New(manager.Options), nil
	case "zypper":
		return zypper.New(manager.Options), nil
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager.Name)
	}
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
//   - s3: Upload to S3-compatible storage (requires url, bucket)
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
			if p.Bucket == "" {
				return nil, fmt.Errorf("s3 publisher requires bucket")
			}
			publishers = append(publishers, s3pub.New(p.Endpoint, p.Bucket, p.Prefix, p.Format))
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

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
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

	// Build-specific flags (only available to build and validate commands)
	buildCmd.Flags().StringVarP(&cfgPath, "config", "c", "./test.yaml", "path to YAML config")
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "./test.yaml", "path to YAML config")

	// Register all subcommands under the root command
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(validateCmd)
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

	// Load the YAML configuration file
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	// Create the package manager backend (dnf, zypper, apt, etc.)
	b, err := newBackend(cfg.Layer.Manager)
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	// Create publishers (local, squashfs, registry, s3, etc.)
	p, err := newPublishers(cfg.Publish)
	if err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	// Create the builder and execute the build
	bldr := builder.New(ctx, cfg, b, p)
	return bldr.Build(ctx)
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

	// Load and validate the configuration
	cfg, err := config.LoadConfig(cfgPath)
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
