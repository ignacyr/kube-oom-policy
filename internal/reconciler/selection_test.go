package reconciler

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/labels"
)

func TestSelectionHonorsSelectorsRunningStateAndExclusions(t *testing.T) {
	cfg := testConfig()
	cfg.ContainerNames = map[string]struct{}{"workload": {}, "tools": {}, "istio-proxy": {}}
	pod := testPod("workload-a", testIDA)
	pod.Kind = ""                // API list items need not repeat their kind.
	pod.Status.Phase = "Pending" // A sibling may still be waiting.
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses,
		testContainer("istio-proxy", testIDB),
		ContainerStatus{Name: "tools", ContainerID: "containerd://" + testIDB},
	)
	got, err := selectPod(pod, cfg)
	if err != nil || len(got) != 1 || got[0].ContainerName != "workload" || got[0].QoSClass != "Burstable" {
		t.Fatalf("selection=%+v error=%v", got, err)
	}
}

func TestInitAndEphemeralContainersAreNotSelected(t *testing.T) {
	pod := testPod("workload-a", testIDA)
	data, _ := json.Marshal(pod)
	var object map[string]any
	_ = json.Unmarshal(data, &object)
	status := object["status"].(map[string]any)
	status["initContainerStatuses"] = []ContainerStatus{testContainer("init", testIDB)}
	status["ephemeralContainerStatuses"] = []ContainerStatus{testContainer("debug", testIDB)}
	data, _ = json.Marshal(object)
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	got, err := selectPod(pod, testConfig())
	if err != nil || len(got) != 1 || got[0].ContainerName != "workload" {
		t.Fatalf("selection=%+v error=%v", got, err)
	}
}

func TestFreshnessRejectsChangesThatAffectTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Pod)
	}{
		{"uid", func(p *Pod) { p.Metadata.UID = "replacement" }},
		{"namespace", func(p *Pod) { p.Metadata.Namespace = "different" }},
		{"name", func(p *Pod) { p.Metadata.Name = "different" }},
		{"node", func(p *Pod) { p.Spec.NodeName = "other-node" }},
		{"labels", func(p *Pod) { p.Metadata.Labels = map[string]string{} }},
		{"deleting", func(p *Pod) { value := "now"; p.Metadata.DeletionTimestamp = &value }},
		{"completed", func(p *Pod) { p.Status.Phase = "Succeeded" }},
		{"stopped", func(p *Pod) { p.Status.ContainerStatuses[0].State.Running = nil }},
		{"restarted", func(p *Pod) { p.Status.ContainerStatuses[0].ContainerID = "containerd://" + testIDB }},
		{"qos", func(p *Pod) { p.Status.QoSClass = "Guaranteed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := normalFakeAPI()
			candidates, err := selectPod(api.list.Items[0], testConfig())
			if err != nil {
				t.Fatal(err)
			}
			fresh := testPod("workload-a", testIDA)
			tc.change(&fresh)
			api.pods["workload-a"] = fresh
			if err := RevalidateCandidate(context.Background(), api, testConfig(), candidates[0]); err == nil {
				t.Fatal("stale target accepted")
			}
		})
	}
}

func TestFreshnessIgnoresResourceVersionAndUnrelatedStatus(t *testing.T) {
	api := normalFakeAPI()
	candidates, _ := selectPod(api.list.Items[0], testConfig())
	fresh := testPod("workload-a", testIDA)
	if err := json.Unmarshal([]byte(`{"metadata":{"resourceVersion":"99999"},"status":{"conditions":[{"type":"Ready","status":"False"}]}}`), &fresh); err != nil {
		t.Fatal(err)
	}
	api.pods["workload-a"] = fresh
	if err := RevalidateCandidate(context.Background(), api, testConfig(), candidates[0]); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidContainerDoesNotHideValidSibling(t *testing.T) {
	pod := testPod("workload-a", testIDA)
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, testContainer("broken", "not-a-container-id"))
	selected, err := selectPod(pod, testConfig())
	if err == nil || len(selected) != 1 || selected[0].ContainerID != testIDA {
		t.Fatalf("selection=%+v error=%v", selected, err)
	}
}

func TestNodeRequiresLinuxAndConfiguredSelector(t *testing.T) {
	cfg := testConfig()
	cfg.NodeSelector = labels.SelectorFromSet(labels.Set{"pool": "workloads"})
	for _, tc := range []struct {
		labels  map[string]string
		allowed bool
	}{
		{map[string]string{"kubernetes.io/os": "linux", "pool": "workloads"}, true},
		{map[string]string{"kubernetes.io/os": "linux", "pool": "system"}, false},
		{map[string]string{"kubernetes.io/os": "windows", "pool": "workloads"}, false},
		{map[string]string{"kubernetes.io/os": "linux"}, false},
	} {
		node := testNode()
		node.Metadata.Labels = tc.labels
		if err := validateNode(node, cfg); (err == nil) != tc.allowed {
			t.Fatalf("labels=%v error=%v", tc.labels, err)
		}
	}
}

func TestSelectionIntersectsNamespacePodNameAndLabels(t *testing.T) {
	cfg := testConfig()
	cfg.Namespaces = []string{"workloads"}
	cfg.PodNames = map[string]struct{}{"workload-a": {}}
	for _, tc := range []struct {
		name    string
		change  func(*Pod)
		allowed bool
	}{
		{"all match", func(*Pod) {}, true},
		{"namespace", func(p *Pod) { p.Metadata.Namespace = "other" }, false},
		{"pod name prefix", func(p *Pod) { p.Metadata.Name = "workload-a-1" }, false},
		{"pod name", func(p *Pod) { p.Metadata.Name = "workload-b" }, false},
		{"labels", func(p *Pod) { p.Metadata.Labels = map[string]string{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := testPod("workload-a", testIDA)
			tc.change(&pod)
			selected, err := selectPod(pod, cfg)
			if err != nil || (len(selected) == 1) != tc.allowed {
				t.Fatalf("selected=%+v error=%v", selected, err)
			}
		})
	}
}

func TestSelectionSkipsAgentOwnPod(t *testing.T) {
	pod := testPod("policy-agent", testIDA)
	cfg := testConfig()
	cfg.PodUID = pod.Metadata.UID
	selected, err := selectPod(pod, cfg)
	if err != nil || len(selected) != 0 {
		t.Fatalf("selected=%+v error=%v", selected, err)
	}
}

func TestFreshnessRechecksConfiguredNamespacesAndPodNames(t *testing.T) {
	api := normalFakeAPI()
	cfg := testConfig()
	candidates, err := selectPod(api.list.Items[0], cfg)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("selected=%+v error=%v", candidates, err)
	}
	for _, filter := range []string{"namespace", "pod name"} {
		t.Run(filter, func(t *testing.T) {
			cfg := testConfig()
			if filter == "namespace" {
				cfg.Namespaces = []string{"other"}
			} else {
				cfg.PodNames = map[string]struct{}{"other": {}}
			}
			if err := RevalidateCandidate(context.Background(), api, cfg, candidates[0]); err == nil {
				t.Fatal("target outside the configured scope was accepted")
			}
		})
	}
}
