package reconciler

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthAndReadiness(t *testing.T) {
	var ready atomic.Bool
	handler := healthHandler(&ready)
	for _, tc := range []struct {
		path   string
		ready  bool
		status int
	}{
		{"/healthz", false, http.StatusOK}, {"/readyz", false, http.StatusServiceUnavailable},
		{"/readyz", true, http.StatusOK}, {"/readyz", false, http.StatusServiceUnavailable},
	} {
		ready.Store(tc.ready)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s ready=%t returned %d", tc.path, tc.ready, response.Code)
		}
	}
}

func TestRunReconcilesImmediatelyAndStopsOnCancellation(t *testing.T) {
	cfg := testConfig()
	cfg.PollInterval = time.Hour
	api := normalFakeAPI()
	entered := make(chan struct{})
	release := make(chan struct{})
	api.getPod = func(ctx context.Context, _, _ string) (Pod, error) {
		close(entered)
		select {
		case <-release:
			return api.pods["workload-a"], nil
		case <-ctx.Done():
			return Pod{}, ctx.Err()
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, NewEngine(cfg, api, &fakeCgroups{}, nil), nil, listener) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial cycle did not start immediately")
	}
	client := &http.Client{Timeout: time.Second}
	check := func() int {
		t.Helper()
		response, err := client.Get("http://" + listener.Addr().String() + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	if status := check(); status != http.StatusServiceUnavailable {
		t.Fatalf("premature readiness: %d", status)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for check() != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("successful cycle did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop the loop")
	}
}
