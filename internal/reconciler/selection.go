package reconciler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
)

var containerIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateNode(node Node, cfg Config) error {
	if node.Kind != "Node" || node.Metadata.Name != cfg.NodeName || node.Metadata.UID == "" || node.Metadata.DeletionTimestamp != nil {
		return fmt.Errorf("node identity is missing, changed, or deleting")
	}
	if node.Metadata.Labels["kubernetes.io/os"] != "linux" {
		return fmt.Errorf("node is not Linux")
	}
	if cfg.NodeSelector == nil || !cfg.NodeSelector.Matches(labels.Set(node.Metadata.Labels)) {
		return fmt.Errorf("node does not match NODE_SELECTOR")
	}
	return nil
}

// Only ordinary, currently running containers are eligible. Init and ephemeral
// containers have separate status fields and are deliberately not decoded.
func selectPod(pod Pod, cfg Config) ([]Candidate, error) {
	if (pod.Kind != "" && pod.Kind != "Pod") || !validNamespace(pod.Metadata.Namespace) ||
		!validDNSSubdomain(pod.Metadata.Name) || pod.Metadata.UID == "" {
		return nil, fmt.Errorf("pod identity is malformed or incomplete")
	}
	if pod.Metadata.UID == cfg.PodUID ||
		(len(cfg.Namespaces) > 0 && !slices.Contains(cfg.Namespaces, pod.Metadata.Namespace)) {
		return nil, nil
	}
	if _, included := cfg.PodNames[pod.Metadata.Name]; len(cfg.PodNames) > 0 && !included {
		return nil, nil
	}
	if pod.Metadata.DeletionTimestamp != nil || pod.Spec.NodeName != cfg.NodeName ||
		cfg.PodSelector == nil || !cfg.PodSelector.Matches(labels.Set(pod.Metadata.Labels)) ||
		(pod.Status.Phase != "Running" && pod.Status.Phase != "Pending") {
		return nil, nil
	}
	var result []Candidate
	var failures []error
	names, ids := map[string]int{}, map[string]int{}
	for _, status := range pod.Status.ContainerStatuses {
		names[status.Name]++
		ids[status.ContainerID]++
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Running == nil {
			continue
		}
		if _, included := cfg.ContainerNames[status.Name]; len(cfg.ContainerNames) > 0 && !included {
			continue
		}
		if _, excluded := cfg.ExcludedContainerNames[status.Name]; excluded {
			continue
		}
		id, ok := strings.CutPrefix(status.ContainerID, "containerd://")
		if !validContainerName(status.Name) || !ok || !containerIDRE.MatchString(id) || names[status.Name] != 1 || ids[status.ContainerID] != 1 {
			failures = append(failures, fmt.Errorf("container %q has an invalid or ambiguous containerd identity", status.Name))
			continue
		}
		result = append(result, Candidate{
			Namespace: pod.Metadata.Namespace, PodName: pod.Metadata.Name, PodUID: pod.Metadata.UID,
			ContainerName: status.Name, ContainerID: id, QoSClass: pod.Status.QoSClass,
		})
	}
	return result, errors.Join(failures...)
}

func RevalidateCandidate(ctx context.Context, api KubernetesAPI, cfg Config, candidate Candidate) error {
	pod, err := api.GetPod(ctx, candidate.Namespace, candidate.PodName)
	if err != nil {
		return fmt.Errorf("re-read pod: %w", err)
	}
	selected, _ := selectPod(pod, cfg)
	for _, fresh := range selected {
		if fresh == candidate {
			return nil
		}
	}
	return fmt.Errorf("pod or container no longer matches the selected identity")
}
