package reconciler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestCycleReconcilesEveryContainer(t *testing.T) {
	api := normalFakeAPI()
	pod := testPod("workload-b", testIDB)
	api.list.Items = append(api.list.Items, pod)
	api.pods["workload-b"] = pod
	cgroups := &fakeCgroups{}
	engine := NewEngine(testConfig(), api, cgroups, nil)
	changed, err := engine.ReconcileOnce(context.Background())
	if err != nil || !changed || len(cgroups.writes) != 2 || api.podCalls != 2 || cgroups.verifyCalls != 1 {
		t.Fatalf("changed=%t error=%v writes=%d GETs=%d verifications=%d", changed, err, len(cgroups.writes), api.podCalls, cgroups.verifyCalls)
	}
}

func TestCycleContinuesAfterIndividualFailures(t *testing.T) {
	for _, where := range []string{"selection", "inspect", "freshness", "write"} {
		t.Run(where, func(t *testing.T) {
			api := normalFakeAPI()
			pod := testPod("workload-b", testIDB)
			api.list.Items = append(api.list.Items, pod)
			api.pods["workload-b"] = pod
			cgroups := &fakeCgroups{}
			switch where {
			case "selection":
				api.list.Items[0].Metadata.UID = ""
			case "inspect":
				cgroups.inspectErrors = map[string]error{testIDA: errors.New("cgroup disappeared")}
			case "freshness":
				api.podErr["workload-a"] = errors.New("pod disappeared")
			case "write":
				cgroups.errors = map[string]error{testIDA: errors.New("cgroup disappeared")}
			}
			changed, err := NewEngine(testConfig(), api, cgroups, nil).ReconcileOnce(context.Background())
			if err == nil || !changed || len(cgroups.writes) == 0 || cgroups.writes[len(cgroups.writes)-1].ContainerID != testIDB {
				t.Fatalf("changed=%t error=%v writes=%+v", changed, err, cgroups.writes)
			}
			if where == "inspect" && (len(cgroups.inspected) != 2 || api.podCalls != 1 || api.nodeCalls != 2) {
				t.Fatalf("inspections=%d podGETs=%d nodeGETs=%d", len(cgroups.inspected), api.podCalls, api.nodeCalls)
			}
		})
	}
}

func TestStableContainersSkipFreshAPIReadsAndWrites(t *testing.T) {
	api := normalFakeAPI()
	api.list.Items = append(api.list.Items, testPod("workload-b", testIDB))
	cgroups := &fakeCgroups{unchanged: map[string]bool{testIDA: true, testIDB: true}}
	changed, err := NewEngine(testConfig(), api, cgroups, nil).ReconcileOnce(context.Background())
	if err != nil || changed || len(cgroups.writes) != 0 || len(cgroups.inspected) != 2 || api.nodeCalls != 1 || api.podCalls != 0 || len(api.listNamespaces) != 1 {
		t.Fatalf("changed=%t error=%v writes=%d inspections=%d nodeGETs=%d podGETs=%d lists=%d", changed, err, len(cgroups.writes), len(cgroups.inspected), api.nodeCalls, api.podCalls, len(api.listNamespaces))
	}
}

func TestCycleDoesNotWriteAfterGlobalFailures(t *testing.T) {
	for _, where := range []string{"root", "node-api", "node-label", "list-api", "incomplete-list"} {
		t.Run(where, func(t *testing.T) {
			api, cgroups := normalFakeAPI(), &fakeCgroups{}
			switch where {
			case "root":
				cgroups.verifyErr = errors.New("not cgroup v2")
			case "node-api":
				api.nodeErr = errors.New("forbidden")
			case "node-label":
				api.node.Metadata.Labels["kubernetes.io/os"] = "windows"
			case "list-api":
				api.listErr = errors.New("unavailable")
			case "incomplete-list":
				api.list.Metadata.Continue = "next-page"
			}
			changed, err := NewEngine(testConfig(), api, cgroups, nil).ReconcileOnce(context.Background())
			if err == nil || changed || len(cgroups.writes) != 0 {
				t.Fatalf("changed=%t error=%v writes=%d", changed, err, len(cgroups.writes))
			}
		})
	}
}

func TestEmptyListIsSuccessfulCycle(t *testing.T) {
	api := normalFakeAPI()
	api.list.Items = []Pod{}
	changed, err := NewEngine(testConfig(), api, &fakeCgroups{}, nil).ReconcileOnce(context.Background())
	if err != nil || changed {
		t.Fatalf("changed=%t error=%v", changed, err)
	}
}

func TestCycleListsOnlyConfiguredNamespacesAndContinuesAfterFailure(t *testing.T) {
	for _, failure := range []string{"api", "malformed list"} {
		t.Run(failure, func(t *testing.T) {
			cfg := testConfig()
			cfg.Namespaces = []string{"unavailable", "workloads"}
			api := normalFakeAPI()
			api.listPods = func(_ context.Context, node, selector, namespace string) (PodList, error) {
				if node != cfg.NodeName || selector != cfg.PodSelector.String() {
					t.Errorf("node=%q selector=%q", node, selector)
				}
				if namespace == "unavailable" {
					if failure == "api" {
						return PodList{}, errors.New("forbidden")
					}
					return PodList{}, nil
				}
				return api.list, nil
			}
			cgroups := &fakeCgroups{}
			changed, err := NewEngine(cfg, api, cgroups, nil).ReconcileOnce(context.Background())
			if err == nil || !changed || len(cgroups.writes) != 1 || !slices.Equal(api.listNamespaces, cfg.Namespaces) {
				t.Fatalf("changed=%t error=%v writes=%+v namespaces=%q", changed, err, cgroups.writes, api.listNamespaces)
			}
		})
	}
}

func TestCycleRejectsCrossNamespaceListResponse(t *testing.T) {
	cfg := testConfig()
	cfg.Namespaces = []string{"tools", "workloads"}
	api := normalFakeAPI()
	api.listPods = func(_ context.Context, _, _, namespace string) (PodList, error) {
		if namespace == "tools" {
			return api.list, nil // The returned pod belongs to workloads, not tools.
		}
		return PodList{Kind: "PodList", Items: []Pod{}}, nil
	}
	cgroups := &fakeCgroups{}
	changed, err := NewEngine(cfg, api, cgroups, nil).ReconcileOnce(context.Background())
	if err == nil || changed || len(cgroups.writes) != 0 || api.podCalls != 0 {
		t.Fatalf("changed=%t error=%v writes=%+v", changed, err, cgroups.writes)
	}
}

func TestCycleRechecksNodeBeforeEveryWrite(t *testing.T) {
	for _, failure := range []string{"labels", "uid", "deleting", "api"} {
		t.Run(failure, func(t *testing.T) {
			cfg := testConfig()
			api := normalFakeAPI()
			api.getNode = func(context.Context, string) (Node, error) {
				node := testNode()
				if api.nodeCalls == 1 {
					return node, nil
				}
				switch failure {
				case "labels":
					node.Metadata.Labels = map[string]string{}
				case "uid":
					node.Metadata.UID = "replacement-node"
				case "deleting":
					value := "now"
					node.Metadata.DeletionTimestamp = &value
				case "api":
					return Node{}, errors.New("unavailable")
				}
				return node, nil
			}
			cgroups := &fakeCgroups{}
			changed, err := NewEngine(cfg, api, cgroups, nil).ReconcileOnce(context.Background())
			if err == nil || changed || len(cgroups.writes) != 0 || api.nodeCalls != 2 {
				t.Fatalf("changed=%t error=%v writes=%+v nodeGETs=%d", changed, err, cgroups.writes, api.nodeCalls)
			}
		})
	}
}

func TestCycleStopsAfterNodeLosesSelectionBetweenContainers(t *testing.T) {
	api := normalFakeAPI()
	pod := testPod("workload-b", testIDB)
	api.list.Items = append(api.list.Items, pod)
	api.pods[pod.Metadata.Name] = pod
	api.getNode = func(context.Context, string) (Node, error) {
		node := testNode()
		if api.nodeCalls == 3 {
			node.Metadata.Labels = map[string]string{}
		}
		return node, nil
	}
	cgroups := &fakeCgroups{}
	changed, err := NewEngine(testConfig(), api, cgroups, nil).ReconcileOnce(context.Background())
	if err == nil || !changed || len(cgroups.writes) != 1 || cgroups.writes[0].ContainerID != testIDA {
		t.Fatalf("changed=%t error=%v writes=%+v", changed, err, cgroups.writes)
	}
}

func TestCycleLogsPolicyAndTargetIdentity(t *testing.T) {
	cfg := testConfig()
	cfg.OOMGroup = '1'
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	_, err := NewEngine(cfg, normalFakeAPI(), &fakeCgroups{}, logf).ReconcileOnce(context.Background())
	if err != nil || len(logs) != 1 {
		t.Fatalf("error=%v logs=%q", err, logs)
	}
	for _, fragment := range []string{"memory.oom.group=1", "node=worker-1", "namespace=workloads", "pod=workload-a", "podUID=01234567-89ab-cdef-0123-456789abcdef", "container=workload", "containerID=" + testIDA} {
		if !strings.Contains(logs[0], fragment) {
			t.Errorf("log missing %q: %q", fragment, logs[0])
		}
	}
}
