package main

import (
	"flag"
	"log"

	"github.com/travisbcotton/image-build/internal/backend/dnf"
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
)

func main() {
	cfgPath := flag.String("config", "./test.yaml", "path to YAML config (or '-' for stdin)")
	flag.Parse()

	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	b := builder.New(cfg, dnf.New())
	if err := b.Build(); err != nil {
		log.Fatalf("build: %v", err)
	}
}
