package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/engine"
	"github.com/redstone-md/graft/internal/handler"
)

// Set via -ldflags at build time:
//
//	go build -ldflags "-X main.version=v1.0.0 -X main.commit=abc123 -X main.date=2026-06-14"
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	if *showVersion {
		fmt.Printf("graft %s (commit: %s, built: %s)\n", version, commit, date)
		return
	}

	// Gin mode: release for tagged builds, debug for local dev.
	if version == "dev" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	log.Printf("Graft %s starting on :%s", version, cfg.Server.Port)
	for name, fp := range cfg.Profiles {
		log.Printf("  profile %q: panel=%v judge=%s final=%s", name, fp.Panel, fp.Judge, fp.Final)
	}

	eng := engine.NewEngine(cfg)
	h := handler.NewHandler(cfg, eng)

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)

	if err := r.Run(":" + cfg.Server.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
