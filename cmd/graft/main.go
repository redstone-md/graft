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
	const w = 64 // inner width between │ symbols

	port := cfg.Server.Port

	fmt.Println()
	printLine("╔", "═", "╗", w)
	printLinePadded("║", fmt.Sprintf("  GRAFT %s", version), "║", w)
	printLine("╠", "═", "╣", w)
	printLinePadded("║", fmt.Sprintf("  http://localhost:%s", port), "║", w)
	printLine("╠", "═", "╣", w)

	// Models
	printLinePadded("║", "  Models:", "║", w)
	for name, m := range cfg.Models {
		line := fmt.Sprintf("    %-10s %s/%s", name, m.Provider, m.Model)
		if m.ContextWindow > 0 {
			line += fmt.Sprintf("  [%s]", formatTokens(m.ContextWindow))
		}
		printLinePadded("║", line, "║", w)
	}

	printLine("╠", "═", "╣", w)

	// Profiles
	printLinePadded("║", "  Profiles:", "║", w)
	for name, fp := range cfg.Profiles {
		panel := strings.Join(fp.Panel, " + ")
		line := fmt.Sprintf("    %-8s %s → %s → %s", name, panel, fp.Judge, fp.Final)
		printLinePadded("║", line, "║", w)
	}

	printLine("╠", "═", "╣", w)
	printLinePadded("║", "  Routes:", "║", w)
	printLinePadded("║", "    POST /v1/chat/completions", "║", w)
	printLinePadded("║", "    GET  /v1/models", "║", w)
	printLinePadded("║", "    GET  /health", "║", w)
	printLine("╚", "═", "╝", w)
	fmt.Println()
}

// printLine draws a box line: left + repeated char + right, total inner width w.
func printLine(left, ch, right string, w int) {
	fmt.Print(left)
	for i := 0; i < w; i++ {
		fmt.Print(ch)
	}
	fmt.Println(right)
}

// printLinePadded draws: │  content padded to w-4  │
func printLinePadded(border, content, end string, w int) {
	inner := w - 4 // 2 spaces padding on each side
	if inner < 0 {
		inner = 0
	}
	padded := content
	if len(padded) > inner {
		padded = padded[:inner-1] + "…"
	}
	for len(padded) < inner {
		padded += " "
	}
	fmt.Printf("%s  %s  %s\n", border, padded, end)
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
