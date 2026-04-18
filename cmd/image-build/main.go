package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/containers/buildah"
	"go.podman.io/storage/pkg/reexec"
	"go.podman.io/storage/pkg/unshare"

	"github.com/travisbcotton/image-build/internal/backend/dnf"
	"github.com/travisbcotton/image-build/internal/builder"
	"github.com/travisbcotton/image-build/internal/config"
)

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
