package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/ignacyr/kube-oom-policy/internal/reconciler"
)

func main() {
	cfg, err := reconciler.ConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid configuration; refusing to start: %v", err)
	}
	api, err := reconciler.NewInClusterClient(cfg)
	if err != nil {
		log.Fatalf("cannot initialize the Kubernetes API client; refusing to start: %v", err)
	}
	cgroups := reconciler.NewHostCgroups(cfg)
	engine := reconciler.NewEngine(cfg, api, cgroups, log.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := reconciler.Run(ctx, cfg, engine, log.Printf); err != nil {
		log.Fatalf("reconciler stopped: %v", err)
	}
}
