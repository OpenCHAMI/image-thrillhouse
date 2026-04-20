package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/containers/buildah"
	"go.podman.io/storage/pkg/reexec"
	"go.podman.io/storage/pkg/unshare"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/backend/dnf"
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/publisher"
	"github.com/travisbcotton/image-build/internal/publisher/local"
	"github.com/travisbcotton/image-build/internal/publisher/squashfs"
)

func newBackend(manager string) (backend.Backend, error) {
	switch manager {
	case "dnf":
		return dnf.New(), nil
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
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

func main() {
	if reexec.Init() {
		return
	}

	if buildah.InitReexec() {
		return
	}

	unshare.MaybeReexecUsingUserNamespace(false)

	fmt.Printf("uid: %d, euid: %d\n", os.Getuid(), os.Geteuid())
	fmt.Printf("inside user ns: %v\n", os.Getenv("_CONTAINERS_USERNS_CONFIGURED"))

	ctx := context.Background()

	cfgPath := flag.String("config", "./test.yaml", "path to YAML config (or '-' for stdin)")
	logLevel := flag.String("log-level", "info", "log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "json", "log format (json, text)")
	flag.Parse()

	if err := setupLogger(*logLevel, *logFormat); err != nil {
		log.Fatalf("%v", err)
	}

	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	b, err := newBackend(cfg.Layer.Manager.Name)
	if err != nil {
		log.Fatalf("backend: %v", err)
	}

	p, err := newPublishers(cfg.Publish)
	if err != nil {
		log.Fatalf("publishers: %v", err)
	}

	builder := builder.New(ctx, cfg, b, p)
	if err := builder.Build(ctx); err != nil {
		log.Fatalf("build: %v", err)
	}
}
