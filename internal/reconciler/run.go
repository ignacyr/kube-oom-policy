package reconciler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

func Run(ctx context.Context, cfg Config, engine *Engine, logf LogFunc) error {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		return fmt.Errorf("listen for health checks: %w", err)
	}
	return run(ctx, cfg, engine, logf, listener)
}

func run(ctx context.Context, cfg Config, engine *Engine, logf LogFunc, listener net.Listener) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var ready atomic.Bool
	server := &http.Server{
		Handler: healthHandler(&ready), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	defer server.Close()
	logf("started node=%s nodeSelector=%q podSelector=%q namespaces=%q oomGroup=%c poll=%s", cfg.NodeName, cfg.NodeSelector.String(), cfg.PodSelector.String(), cfg.Namespaces, cfg.OOMGroup, cfg.PollInterval)
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := engine.ReconcileOnce(ctx)
		ready.Store(err == nil)
		if err != nil && ctx.Err() == nil {
			logf("reconciliation failed; retrying: %v", err)
		}
		timer := time.NewTimer(cfg.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case err := <-serverErrors:
			timer.Stop()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("health server: %w", err)
		case <-timer.C:
		}
	}
}

func healthHandler(ready *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "reconciliation has not succeeded", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux
}
