package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"strings"

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

	eng := engine.NewEngine(cfg)
	h := handler.NewHandler(cfg, eng)

	// Suppress Gin's [GIN-debug] route registration noise.
	gin.DefaultWriter = io.Discard

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)

	printBanner(cfg)

	if err := r.Run(":" + cfg.Server.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func printBanner(cfg *config.Config) {
	fmt.Println()
	fmt.Println("  __________  ___    ____________")
	fmt.Println("  / ____/ __ \\/   |  / ____/_  __/")
	fmt.Println("  / / __/ /_/ / /| | / /_    / /   ")
	fmt.Println("  / /_/ / _, _/ ___ |/ __/   / /    ")
	fmt.Println("  \\____/_/ |_/_/  |_/_/     /_/  ")
	fmt.Printf("  github.com/redstone-md/graft  %s\n", version)
	fmt.Println()

	// Models
	fmt.Println("  Models:")
	for name, m := range cfg.Models {
		ctx := ""
		if m.ContextWindow > 0 {
			ctx = fmt.Sprintf("  [%s]", formatTokens(m.ContextWindow))
		}
		fmt.Printf("    %-12s %s/%s%s\n", name, m.Provider, m.Model, ctx)
	}
	fmt.Println()

	// Profiles
	fmt.Println("  Profiles:")
	for name, fp := range cfg.Profiles {
		panel := strings.Join(fp.Panel, " + ")
		fmt.Printf("    %-10s %s → %s → %s\n", name, panel, fp.Judge, fp.Final)
	}
	fmt.Println()

	// Routes
	fmt.Println("  Listening on http://localhost:" + cfg.Server.Port)
	fmt.Println()
}

func formatTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%dM", n/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dK", n/1000)
	}
	return fmt.Sprintf("%d", n)
}
