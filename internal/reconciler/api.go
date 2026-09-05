package reconciler

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxAPIResponseBytes = 32 << 20

type KubernetesAPI interface {
	GetNode(context.Context, string) (Node, error)
	ListPods(context.Context, string, string, string) (PodList, error)
	GetPod(context.Context, string, string) (Pod, error)
}

type InClusterClient struct {
	baseURL   string
	tokenPath string
	client    *http.Client
}

func NewInClusterClient(cfg Config) (*InClusterClient, error) {
	caPath := filepath.Join(cfg.ServiceAccountDir, "ca.crt")
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read service-account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("service-account CA contains no certificates")
	}
	hostPort := net.JoinHostPort(cfg.KubernetesHost, cfg.KubernetesPort)
	transport := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout: 5 * time.Second,
		DisableCompression:  true,
	}
	return &InClusterClient{
		baseURL:   "https://" + hostPort,
		tokenPath: filepath.Join(cfg.ServiceAccountDir, "token"),
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("redirects are refused")
			},
		},
	}, nil
}

func (c *InClusterClient) GetNode(ctx context.Context, name string) (Node, error) {
	var result Node
	err := c.get(ctx, "/api/v1/nodes/"+url.PathEscape(name), nil, &result)
	return result, err
}

func (c *InClusterClient) ListPods(ctx context.Context, node, selector, namespace string) (PodList, error) {
	query := url.Values{}
	query.Set("fieldSelector", "spec.nodeName="+node)
	query.Set("labelSelector", selector)
	resource := "/api/v1/pods"
	if namespace != "" {
		resource = "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
	}
	var result PodList
	err := c.get(ctx, resource, query, &result)
	return result, err
}

func (c *InClusterClient) GetPod(ctx context.Context, namespace, name string) (Pod, error) {
	var result Pod
	resource := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name)
	err := c.get(ctx, resource, nil, &result)
	return result, err
}

func (c *InClusterClient) get(ctx context.Context, resource string, query url.Values, output any) error {
	tokenBytes, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return fmt.Errorf("read projected service-account token: %w", err)
	}
	token := string(tokenBytes)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("projected service-account token is empty or malformed")
	}
	requestURL := c.baseURL + resource
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("construct Kubernetes request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("Kubernetes GET failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Kubernetes GET returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Kubernetes response: %w", err)
	}
	if len(body) > maxAPIResponseBytes {
		return fmt.Errorf("Kubernetes response exceeds %d bytes", maxAPIResponseBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode Kubernetes response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("Kubernetes response contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing Kubernetes response: %w", err)
	}
	return nil
}
