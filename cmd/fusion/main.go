package main

import (
	"flag"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/engine"
	"github.com/redstone-md/graft/internal/handler"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	log.Printf("Fusion Orchestrator starting on :%s", cfg.Server.Port)
	for name, fp := range cfg.Fusion {
		log.Printf("  profile %q: panel=%v judge=%s final=%s", name, fp.Panel, fp.Judge, fp.Final)
	}

	eng := engine.NewEngine(cfg)
	h := handler.NewHandler(cfg, eng)

	r := gin.Default()
	h.RegisterRoutes(r)

	if err := r.Run(":" + cfg.Server.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
