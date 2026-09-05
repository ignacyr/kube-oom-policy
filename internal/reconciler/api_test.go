package reconciler

import (
	"context"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewInClusterClientUsesProjectedCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/nodes/redirect":
			http.Redirect(response, request, "/redirect-target", http.StatusFound)
			return
		case "/redirect-target":
			t.Error("API client followed a redirect")
			return
		case "/api/v1/nodes/forbidden":
			response.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(response, "private-error-body")
			return
		}
		if request.URL.Path != "/api/v1/nodes/worker-1" || request.Header.Get("Authorization") != "Bearer projected-token" {
			t.Errorf("unexpected request path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(response, `{"kind":"Node","metadata":{"name":"worker-1"}}`)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	serviceAccountDir := t.TempDir()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(serviceAccountDir, "ca.crt"), certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceAccountDir, "token"), []byte("projected-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.ServiceAccountDir = serviceAccountDir
	cfg.KubernetesHost = host
	cfg.KubernetesPort = port
	client, err := NewInClusterClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetNode(context.Background(), "worker-1"); err != nil {
		t.Fatalf("TLS request with projected CA failed: %v", err)
	}
	if _, err := client.GetNode(context.Background(), "redirect"); err == nil {
		t.Fatal("redirect was accepted")
	}
	if _, err := client.GetNode(context.Background(), "forbidden"); err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "private-error-body") {
		t.Fatalf("unexpected API error: %v", err)
	}
}

func TestInClusterClientUsesExactSelectorsAndReloadsToken(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("first-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		wantToken := "Bearer first-token"
		wantPath := "/api/v1/pods"
		if requests == 2 {
			wantToken = "Bearer second-token"
			wantPath = "/api/v1/namespaces/workloads/pods"
		}
		if request.URL.Path != wantPath {
			t.Errorf("path=%q, want %q", request.URL.Path, wantPath)
		}
		if got := request.Header.Get("Authorization"); got != wantToken {
			t.Errorf("Authorization=%q, want %q", got, wantToken)
		}
		if got := request.URL.Query().Get("fieldSelector"); got != "spec.nodeName=worker-1" {
			t.Errorf("fieldSelector=%q", got)
		}
		if got := request.URL.Query().Get("labelSelector"); got != DefaultPodSelector+",team in (platform,development)" {
			t.Errorf("labelSelector=%q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"kind":"PodList","metadata":{"resourceVersion":"1"},"items":[]}`)
	}))
	defer server.Close()
	client := &InClusterClient{baseURL: server.URL, tokenPath: tokenPath, client: &http.Client{Timeout: time.Second}}
	if _, err := client.ListPods(context.Background(), "worker-1", DefaultPodSelector+",team in (platform,development)", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("second-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListPods(context.Background(), "worker-1", DefaultPodSelector+",team in (platform,development)", "workloads"); err != nil {
		t.Fatal(err)
	}
}

func TestInClusterClientRejectsMalformedTokenAndTrailingJSON(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, `{"kind":"Node"} {"second":true}`)
	}))
	defer server.Close()
	client := &InClusterClient{baseURL: server.URL, tokenPath: tokenPath, client: &http.Client{Timeout: time.Second}}
	if err := os.WriteFile(tokenPath, []byte("token\nheader-injection"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetNode(context.Background(), "node"); err == nil {
		t.Fatal("malformed token was accepted")
	}
	if err := os.WriteFile(tokenPath, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetNode(context.Background(), "node"); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

func TestInClusterClientRejectsNonObjectRunningState(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, `{"kind":"Pod","status":{"containerStatuses":[{"name":"workload","state":{"running":false}}]}}`)
	}))
	defer server.Close()
	client := &InClusterClient{baseURL: server.URL, tokenPath: tokenPath, client: &http.Client{Timeout: time.Second}}
	if _, err := client.GetPod(context.Background(), "workload-users", "workload"); err == nil {
		t.Fatal("non-object state.running was accepted")
	}
}
