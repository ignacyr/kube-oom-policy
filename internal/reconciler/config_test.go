package reconciler

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/labels"
)

func setTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NODE_NAME", "worker-1")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT_HTTPS", "443")
	t.Setenv("POD_SELECTOR", DefaultPodSelector)
	t.Setenv("NODE_SELECTOR", DefaultNodeSelector)
	t.Setenv("POD_UID", "")
	t.Setenv("NAMESPACES", "")
	t.Setenv("POD_NAMES", "")
	t.Setenv("OOM_GROUP", "0")
	t.Setenv("CONTAINER_NAMES", "")
	t.Setenv("EXCLUDED_CONTAINER_NAMES", "istio-proxy,linkerd-proxy")
	t.Setenv("POLL_INTERVAL", "5s")
}

func TestConfigParsesStandardSelectorsAndContainerLists(t *testing.T) {
	setTestEnv(t)
	t.Setenv("POD_SELECTOR", "workload,team in (platform,development),!disabled")
	t.Setenv("CONTAINER_NAMES", "workload, tools")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PodSelector.Matches(labels.Set{"workload": "", "team": "platform"}) {
		t.Fatal("standard selector or empty-value existence label did not match")
	}
	if cfg.PodSelector.Matches(labels.Set{"workload": "", "team": "platform", "disabled": ""}) {
		t.Fatal("negated selector matched")
	}
	if len(cfg.ContainerNames) != 2 || cfg.PollInterval != 5*time.Second || cfg.CgroupRoot != "/host-cgroup" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if _, ok := cfg.ExcludedContainerNames["istio-proxy"]; !ok {
		t.Fatal("missing sidecar exclusion")
	}
}

func TestConfigRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"POD_SELECTOR", ""}, {"POD_SELECTOR", "workload in ("},
		{"NODE_SELECTOR", ""}, {"NODE_NAME", "../node"},
		{"NAMESPACES", "team.a"}, {"NAMESPACES", "tools,"}, {"NAMESPACES", "../tools"},
		{"POD_NAMES", "workload-*"}, {"POD_NAMES", "workload-a,"}, {"POD_NAMES", "UPPER"},
		{"OOM_GROUP", ""}, {"OOM_GROUP", "2"}, {"OOM_GROUP", " 0"}, {"OOM_GROUP", "01"},
		{"POLL_INTERVAL", "0s"}, {"POLL_INTERVAL", "2h"}, {"POLL_INTERVAL", "5"},
		{"CONTAINER_NAMES", "workload,"}, {"EXCLUDED_CONTAINER_NAMES", "INVALID"},
		{"KUBERNETES_SERVICE_HOST", "host/path"}, {"KUBERNETES_SERVICE_PORT_HTTPS", "0"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			setTestEnv(t)
			t.Setenv(tc.name, tc.value)
			if _, err := ConfigFromEnv(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestExclusionsCanBeCleared(t *testing.T) {
	setTestEnv(t)
	t.Setenv("EXCLUDED_CONTAINER_NAMES", "")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExcludedContainerNames) != 0 {
		t.Fatal("exclusions were not cleared")
	}
}

func TestConfigParsesExactNamesAndBothOOMPolicies(t *testing.T) {
	for _, value := range []string{"0", "1"} {
		t.Run(value, func(t *testing.T) {
			setTestEnv(t)
			t.Setenv("NAMESPACES", "tools, workloads,tools")
			t.Setenv("POD_NAMES", "editor-0, worker.v2,editor-0")
			t.Setenv("OOM_GROUP", value)
			t.Setenv("POD_UID", "agent-uid")
			cfg, err := ConfigFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Namespaces) != 2 || cfg.Namespaces[0] != "tools" || cfg.Namespaces[1] != "workloads" {
				t.Fatalf("namespaces=%q", cfg.Namespaces)
			}
			if _, found := cfg.PodNames["worker.v2"]; !found || len(cfg.PodNames) != 2 || cfg.OOMGroup != value[0] || cfg.PodUID != "agent-uid" {
				t.Fatalf("unexpected config: %+v", cfg)
			}
		})
	}
}
