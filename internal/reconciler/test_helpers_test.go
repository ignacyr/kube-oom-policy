package reconciler

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/labels"
)

const (
	testIDA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testIDB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testConfig() Config {
	return Config{
		NodeName:     "worker-1",
		PodSelector:  labels.SelectorFromSet(labels.Set{"oom-policy": "managed"}),
		NodeSelector: labels.SelectorFromSet(labels.Set{"kubernetes.io/os": "linux"}),
		CgroupRoot:   DefaultCgroupRoot, ServiceAccountDir: DefaultSADir,
		KubernetesHost: "10.0.0.1", KubernetesPort: "443",
		PollInterval:           5 * time.Second,
		ExcludedContainerNames: map[string]struct{}{"istio-proxy": {}, "linkerd-proxy": {}},
		OOMGroup:               '0',
	}
}

func testNode() Node {
	return Node{Kind: "Node", Metadata: objectMeta{
		Name: "worker-1", UID: "node-uid",
		Labels: map[string]string{"kubernetes.io/os": "linux"},
	}}
}

func testPod(name, id string) Pod {
	pod := Pod{Kind: "Pod", Metadata: objectMeta{
		Name: name, Namespace: "workloads", UID: "01234567-89ab-cdef-0123-456789abcdef",
		Labels: map[string]string{"oom-policy": "managed"},
	}}
	pod.Spec.NodeName = "worker-1"
	pod.Status.Phase = "Running"
	pod.Status.QoSClass = "Burstable"
	pod.Status.ContainerStatuses = []ContainerStatus{testContainer("workload", id)}
	return pod
}

func testContainer(name, id string) ContainerStatus {
	return ContainerStatus{Name: name, ContainerID: "containerd://" + id, State: containerState{Running: &struct{}{}}}
}

type fakeAPI struct {
	node           Node
	list           PodList
	pods           map[string]Pod
	nodeErr        error
	listErr        error
	podErr         map[string]error
	podCalls       int
	nodeCalls      int
	listNamespaces []string
	getNode        func(context.Context, string) (Node, error)
	listPods       func(context.Context, string, string, string) (PodList, error)
	getPod         func(context.Context, string, string) (Pod, error)
}

func normalFakeAPI() *fakeAPI {
	pod := testPod("workload-a", testIDA)
	return &fakeAPI{
		node: testNode(), list: PodList{Kind: "PodList", Items: []Pod{pod}},
		pods: map[string]Pod{"workload-a": pod}, podErr: map[string]error{},
	}
}

func (f *fakeAPI) GetNode(ctx context.Context, name string) (Node, error) {
	f.nodeCalls++
	if f.getNode != nil {
		return f.getNode(ctx, name)
	}
	return f.node, f.nodeErr
}
func (f *fakeAPI) ListPods(ctx context.Context, node, selector, namespace string) (PodList, error) {
	f.listNamespaces = append(f.listNamespaces, namespace)
	if f.listPods != nil {
		return f.listPods(ctx, node, selector, namespace)
	}
	return f.list, f.listErr
}
func (f *fakeAPI) GetPod(ctx context.Context, namespace, name string) (Pod, error) {
	f.podCalls++
	if f.getPod != nil {
		return f.getPod(ctx, namespace, name)
	}
	if err := f.podErr[name]; err != nil {
		return Pod{}, err
	}
	pod, ok := f.pods[name]
	if !ok {
		return Pod{}, errors.New("pod not found")
	}
	return pod, nil
}

type fakeCgroups struct {
	verifyErr     error
	inspectErrors map[string]error
	unchanged     map[string]bool
	inspected     []Candidate
	errors        map[string]error
	writes        []Candidate
	verifyCalls   int
}

func (f *fakeCgroups) Verify() error { f.verifyCalls++; return f.verifyErr }
func (f *fakeCgroups) NeedsChange(candidate Candidate) (bool, error) {
	f.inspected = append(f.inspected, candidate)
	if err := f.inspectErrors[candidate.ContainerID]; err != nil {
		return false, err
	}
	return !f.unchanged[candidate.ContainerID], nil
}
func (f *fakeCgroups) SetOOMGroup(candidate Candidate) (bool, error) {
	f.writes = append(f.writes, candidate)
	if err := f.errors[candidate.ContainerID]; err != nil {
		return false, err
	}
	return true, nil
}
