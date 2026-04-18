package main

import (
	"context"
	"flag"
	"log"

	"github.com/travisbcotton/image-build/internal/backend/dnf"
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
)

func main() {

	ctx := context.Background()

	cfgPath := flag.String("config", "./test.yaml", "path to YAML config (or '-' for stdin)")
	flag.Parse()

	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	b := builder.New(ctx, cfg, dnf.New())
	if err := b.Build(ctx); err != nil {
		log.Fatalf("build: %v", err)
	}
}
