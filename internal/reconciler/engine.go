package reconciler

import (
	"context"
	"errors"
	"fmt"
)

type LogFunc func(string, ...any)

type Engine struct {
	cfg     Config
	api     KubernetesAPI
	cgroups CgroupAccess
	logf    LogFunc
}

func NewEngine(cfg Config, api KubernetesAPI, cgroups CgroupAccess, logf LogFunc) *Engine {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Engine{cfg: cfg, api: api, cgroups: cgroups, logf: logf}
}

// ReconcileOnce visits every selected container, including after a different
// container or namespace fails. An invalid node or cgroup root stops the cycle.
func (e *Engine) ReconcileOnce(ctx context.Context) (bool, error) {
	if err := e.cgroups.Verify(); err != nil {
		return false, fmt.Errorf("verify cgroup root: %w", err)
	}
	node, err := e.api.GetNode(ctx, e.cfg.NodeName)
	if err != nil {
		return false, fmt.Errorf("get node: %w", err)
	}
	if err := validateNode(node, e.cfg); err != nil {
		return false, err
	}
	var failures []error
	wrote := false
	namespaces := e.cfg.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""} // The cluster-wide pod endpoint.
	}
	for _, namespace := range namespaces {
		pods, err := e.api.ListPods(ctx, e.cfg.NodeName, e.cfg.PodSelector.String(), namespace)
		if err != nil {
			failures = append(failures, fmt.Errorf("list pods namespace=%q: %w", namespace, err))
			continue
		}
		if pods.Kind != "PodList" || pods.Items == nil || pods.Metadata.Continue != "" {
			failures = append(failures, fmt.Errorf("pod list namespace=%q is malformed or incomplete", namespace))
			continue
		}
		for _, pod := range pods.Items {
			if namespace != "" && pod.Metadata.Namespace != namespace {
				failures = append(failures, fmt.Errorf("pod list namespace=%q contains a pod from another namespace", namespace))
				continue
			}
			candidates, err := selectPod(pod, e.cfg)
			if err != nil {
				failures = append(failures, fmt.Errorf("select %s/%s: %w", pod.Metadata.Namespace, pod.Metadata.Name, err))
			}
			for _, candidate := range candidates {
				if err := ctx.Err(); err != nil {
					return wrote, err
				}
				needsChange, err := e.cgroups.NeedsChange(candidate)
				if err != nil {
					failures = append(failures, fmt.Errorf("inspect %s/%s container=%s: %w", candidate.Namespace, candidate.PodName, candidate.ContainerName, err))
					continue
				}
				if !needsChange {
					continue
				}
				freshNode, err := e.api.GetNode(ctx, e.cfg.NodeName)
				if err != nil {
					return wrote, errors.Join(append(failures, fmt.Errorf("re-read node: %w", err))...)
				}
				if err := validateNode(freshNode, e.cfg); err != nil {
					return wrote, errors.Join(append(failures, err)...)
				}
				if freshNode.Metadata.UID != node.Metadata.UID {
					return wrote, errors.Join(append(failures, fmt.Errorf("node UID changed during reconciliation"))...)
				}
				if err := RevalidateCandidate(ctx, e.api, e.cfg, candidate); err != nil {
					failures = append(failures, fmt.Errorf("%s/%s container=%s: %w", candidate.Namespace, candidate.PodName, candidate.ContainerName, err))
					continue
				}
				if err := ctx.Err(); err != nil {
					return wrote, err
				}
				changed, err := e.cgroups.SetOOMGroup(candidate)
				if err != nil {
					failures = append(failures, fmt.Errorf("%s/%s container=%s: %w", candidate.Namespace, candidate.PodName, candidate.ContainerName, err))
					continue
				}
				if changed {
					wrote = true
					e.logf("set memory.oom.group=%c node=%s namespace=%s pod=%s podUID=%s container=%s containerID=%s", e.cfg.OOMGroup, e.cfg.NodeName, candidate.Namespace, candidate.PodName, candidate.PodUID, candidate.ContainerName, candidate.ContainerID)
				}
			}
		}
	}
	return wrote, errors.Join(failures...)
}
