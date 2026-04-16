package main

import (
	"flag"
	"log"

	"github.com/travisbcotton/image-build/internal/config"
)

func main()  {
	cfgPath := flag.String("config", "./test.yaml", "path to YAML config (or '-' for stdin)")
	flag.Parse()

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf(err)
	}
	fmt.Printf("%+v\n", cfg)
}