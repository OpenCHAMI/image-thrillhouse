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
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/publisher"
	"github.com/travisbcotton/image-build/internal/publisher/local"
	"github.com/travisbcotton/image-build/internal/publisher/squashfs"
)

var (
	cfgPath   string
	logLevel  string
	logFormat string
)

var rootCmd = &cobra.Command{
	Use:          "image-build",
	Short:        "Build OS images for multiple distros",
	SilenceUsage: true,
}

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build an image layer from a config file",
	RunE:  runBuild,
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a config file without building",
	RunE:  runValidate,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("image-build v0.1.0")
	},
}

func newBackend(manager config.Manager) (backend.Backend, error) {
	switch manager.Name {
	case "dnf":
		return dnf.New(manager.Options), nil
	case "mmdebstrap":
		return mmdebstrap.New(manager.Options), nil
	case "apt":
		return apt.New(manager.Options), nil
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager.Name)
	}
}

func newPublishers(publishes []config.Publish) ([]publisher.Publisher, error) {
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
		default:
			return nil, fmt.Errorf("unsupported publisher type: %s", p.Type)
		}
	}
	return publishers, nil
}

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

func init() {
	// persistent flags apply to all subcommands
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "json", "log format (json, text)")

	// build-specific flags
	buildCmd.Flags().StringVarP(&cfgPath, "config", "c", "./test.yaml", "path to YAML config")
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "./test.yaml", "path to YAML config")

	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(versionCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(cfgPath)
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
	return bldr.Build(ctx)
}

func runValidate(cmd *cobra.Command, args []string) error {
	if err := setupLogger(logLevel, logFormat); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	if _, err := newBackend(cfg.Layer.Manager); err != nil {
		return fmt.Errorf("invalid backend: %w", err)
	}

	slog.Info("config is valid", "path", cfgPath)
	return nil
}

func main() {
	if reexec.Init() {
		return
	}

	if buildah.InitReexec() {
		return
	}

	unshare.MaybeReexecUsingUserNamespace(false)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
