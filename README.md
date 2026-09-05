# kube-oom-policy

[![CI](https://github.com/ignacyr/kube-oom-policy/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/ignacyr/kube-oom-policy/actions/workflows/ci.yml)

**Control OOM grouping for selected Kubernetes containers.**

One Go binary, one image, one Helm chart. Choose your nodes and workloads;
the DaemonSet reconciles their cgroup v2 `memory.oom.group` setting.
No cloud-provider or application-operator dependency. No kubelet changes or restarts.

| Helm value | Container behavior at its memory limit |
| --- | --- |
| `oomGroup: false` (default) | Write `0`: allow individual-process OOM killing. |
| `oomGroup: true` | Write `1`: treat the cgroup as a group for OOM killing. |

Useful for multi-process containers such as browser IDEs, where losing a child
process can be preferable to losing the whole session. Disabling group kills
does **not** guarantee that PID 1 survives or that only one process dies.

## Requirements

Linux nodes with **cgroup v2, containerd, and `openat2` support**, plus permission
to install a DaemonSet with a writable host cgroup mount. Systemd and cgroupfs
layouts are supported. CRI-O, cgroup v1, Windows and sandbox runtimes are not supported.

This is an administrator-installed node agent. Read the short
[security model](SECURITY.md) before deploying it.

## Build and install

Build an image in a registry your cluster can pull from:

```sh
git clone https://github.com/ignacyr/kube-oom-policy.git
cd kube-oom-policy
IMAGE=registry.example.com/platform/kube-oom-policy:0.1.0
docker build -t "$IMAGE" .
docker push "$IMAGE"
```

Save your selection as `policy.yaml`. Replace the example namespace and pool labels
with your cluster's values; target namespaces must already exist. There is no
special node-pool API.

```yaml
oomGroup: false
namespaces: [workloads] # Limits both API requests and Pod RBAC to these namespaces.
podSelector: "oom-policy=managed"
podNames: [] # Optional exact Pod names; combined with the selector.
containerNames: [] # Empty selects all running regular containers.
excludedContainerNames: [istio-proxy, linkerd-proxy]
nodeSelector:
  kubernetes.io/os: linux
nodeSelectorExpressions:
  - key: nodepool
    operator: In
    values: [interactive, batch]
pollInterval: 5s
```

Label the target workload's **Pod template** with `oom-policy: managed` so new
Pods inherit the selection. To select an existing Pod:

```sh
kubectl label pod -n workloads my-pod oom-policy=managed
helm upgrade --install oom-policy ./charts/kube-oom-policy \
  --namespace oom-policy-system --create-namespace \
  --set image.repository=registry.example.com/platform/kube-oom-policy \
  --set image.tag=0.1.0 -f policy.yaml
```

The installation namespace must permit the hostPath and root UID. The chart does
not relax your cluster's admission policy. Use `image.digest` and
`imagePullSecrets` for your registry's deployment policy. Images are built by
the operator; this repository does not yet provide published release images.

## Selection and operation

All filters intersect; container exclusions win. Empty `namespaces` selects
across namespaces and grants cluster-wide Pod read access. Empty `podNames` and
`containerNames` add no name restriction. The Pod selector must be nonempty;
the default `oom-policy=managed` requires opt-in. Default node selection is all
Linux nodes. Add pool labels or expressions to narrow it.

Node expressions support `In`, `NotIn`, `Exists` and `DoesNotExist`. They are
used for both scheduling and runtime revalidation. Init and ephemeral containers,
the agent's own Pod, deleting Pods and stopped containers are skipped.

```text
Helm values → one agent per selected node
            → read selected Pods from the Kubernetes API
            → recheck node, Pod UID and running container ID
            → write and verify only that container's memory.oom.group
```

Every cycle handles all eligible containers; a disappearing container does not
block the rest. New Pods, container restarts and new nodes are picked up
automatically. There is a startup/polling delay. Pod/QoS ancestor controls,
memory limits and eviction settings are not changed.

```sh
kubectl logs -n oom-policy-system daemonset/oom-policy
kubectl exec -n workloads my-pod -c my-container -- cat /sys/fs/cgroup/memory.oom.group
helm uninstall oom-policy --namespace oom-policy-system
```

Uninstalling or removing selection stops future writes; existing values persist.
To change them, apply the intended `oomGroup` value while targets are still
selected, or recreate the containers. Avoid overlapping releases with conflicting values.

## Development and verification

```sh
go test -race ./...
go vet ./...
helm lint ./charts/kube-oom-policy
```

CI runs tests, dependency-vulnerability and secret scans, builds the image, and
uploads its SPDX SBOM with the packaged chart. It has read-only repository access
and does not publish images. See [CI runs](https://github.com/ignacyr/kube-oom-policy/actions).

Live checks on Docker Desktop Kubernetes 1.36.1 with containerd 2.3.1 and cgroup v2
verified workload selection, namespace RBAC and reapplication after a container
restart. Three real child-process OOMs left PID 1 alive without OOM-induced
container restarts. Both amd64 and arm64 images build; ARM runtime behavior and
other cluster distributions have not been verified.

Contributions should keep the tool focused on this one control. Include a
regression test and update the values or security notes when behavior changes.
Report vulnerabilities as described in [SECURITY.md](SECURITY.md).
