package reconciler

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	DefaultCgroupRoot   = "/host-cgroup"
	DefaultSADir        = "/var/run/secrets/kubernetes.io/serviceaccount"
	DefaultPodSelector  = "oom-policy=managed"
	DefaultNodeSelector = "kubernetes.io/os=linux"
)

type Config struct {
	NodeName               string
	PodUID                 string
	PodSelector            labels.Selector
	NodeSelector           labels.Selector
	Namespaces             []string
	PodNames               map[string]struct{}
	ContainerNames         map[string]struct{}
	ExcludedContainerNames map[string]struct{}
	OOMGroup               byte
	PollInterval           time.Duration
	CgroupRoot             string
	ServiceAccountDir      string
	KubernetesHost         string
	KubernetesPort         string
}

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		NodeName:          os.Getenv("NODE_NAME"),
		PodUID:            os.Getenv("POD_UID"),
		CgroupRoot:        DefaultCgroupRoot,
		ServiceAccountDir: DefaultSADir,
		KubernetesHost:    os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesPort:    envOr("KUBERNETES_SERVICE_PORT_HTTPS", "443"),
	}
	var err error
	if cfg.PodSelector, err = parseSelector("POD_SELECTOR", envOr("POD_SELECTOR", DefaultPodSelector)); err != nil {
		return Config{}, err
	}
	if cfg.NodeSelector, err = parseSelector("NODE_SELECTOR", envOr("NODE_SELECTOR", DefaultNodeSelector)); err != nil {
		return Config{}, err
	}
	if cfg.Namespaces, err = parseNameList(os.Getenv("NAMESPACES"), validNamespace); err != nil {
		return Config{}, fmt.Errorf("NAMESPACES: %w", err)
	}
	if cfg.PodNames, err = parseNameSet(os.Getenv("POD_NAMES"), validDNSSubdomain); err != nil {
		return Config{}, fmt.Errorf("POD_NAMES: %w", err)
	}
	if cfg.ContainerNames, err = parseContainerSet(os.Getenv("CONTAINER_NAMES")); err != nil {
		return Config{}, fmt.Errorf("CONTAINER_NAMES: %w", err)
	}
	if cfg.ExcludedContainerNames, err = parseContainerSet(envOr("EXCLUDED_CONTAINER_NAMES", "istio-proxy,linkerd-proxy")); err != nil {
		return Config{}, fmt.Errorf("EXCLUDED_CONTAINER_NAMES: %w", err)
	}
	switch value := envOr("OOM_GROUP", "0"); value {
	case "0", "1":
		cfg.OOMGroup = value[0]
	default:
		return Config{}, fmt.Errorf("OOM_GROUP must be exactly 0 or 1")
	}
	if cfg.PollInterval, err = time.ParseDuration(envOr("POLL_INTERVAL", "5s")); err != nil || cfg.PollInterval < time.Second || cfg.PollInterval > time.Hour {
		return Config{}, fmt.Errorf("POLL_INTERVAL must be a duration between 1s and 1h")
	}
	if !validDNSSubdomain(cfg.NodeName) {
		return Config{}, fmt.Errorf("NODE_NAME is missing or invalid")
	}
	if net.ParseIP(cfg.KubernetesHost) == nil && !validDNSSubdomain(cfg.KubernetesHost) {
		return Config{}, fmt.Errorf("KUBERNETES_SERVICE_HOST is missing or invalid")
	}
	port, err := strconv.ParseUint(cfg.KubernetesPort, 10, 16)
	if err != nil || port == 0 {
		return Config{}, fmt.Errorf("KUBERNETES_SERVICE_PORT_HTTPS is invalid")
	}
	return cfg, nil
}

func parseSelector(name, value string) (labels.Selector, error) {
	selector, err := labels.Parse(value)
	if err != nil || selector == nil || selector.Empty() {
		return nil, fmt.Errorf("%s must be a nonempty Kubernetes label selector: %q", name, value)
	}
	return selector, nil
}

func parseContainerSet(value string) (map[string]struct{}, error) {
	return parseNameSet(value, validContainerName)
}

func parseNameSet(value string, valid func(string) bool) (map[string]struct{}, error) {
	names, err := parseNameList(value, valid)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result, nil
}

func parseNameList(value string, valid func(string) bool) ([]string, error) {
	var result []string
	seen := make(map[string]struct{})
	if strings.TrimSpace(value) == "" {
		return result, nil
	}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if !valid(name) {
			return nil, fmt.Errorf("invalid name %q", name)
		}
		if _, exists := seen[name]; !exists {
			result = append(result, name)
			seen[name] = struct{}{}
		}
	}
	return result, nil
}

func validNamespace(value string) bool {
	return len(validation.IsDNS1123Label(value)) == 0
}

func validContainerName(value string) bool {
	return len(validation.IsDNS1123Label(value)) == 0
}

func validDNSSubdomain(value string) bool {
	return len(validation.IsDNS1123Subdomain(value)) == 0
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
