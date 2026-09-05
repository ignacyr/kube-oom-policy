# Security

kube-oom-policy is an administrator-installed node agent. Its writable host
cgroup mount requires explicit approval under your cluster's security policy.
This document describes access and limitations; it is not an independent audit,
compliance certification, or assurance of organizational approval.

## Required access

| Access | Purpose |
| --- | --- |
| Root UID with all Linux capabilities dropped | Write root-owned cgroup controls |
| Writable `/sys/fs/cgroup` hostPath | Apply `memory.oom.group` |
| Nodes: `get` | Recheck the local node and its labels |
| Pods: `get`, `list` in configured namespaces | Select and revalidate containers |
| Kubernetes API over verified TLS | Read node and workload identity |
| TCP 8080 on the Pod IP | Health and readiness checks only |

The container has a read-only root filesystem, uses RuntimeDefault seccomp,
and disables privilege escalation. It has no runtime socket, host PID/network
namespace, Kubernetes write permission, shell, or application control endpoint.
The application sends no telemetry and needs no external service connection.

## Trust boundary

The application writes only a selected container's `memory.oom.group`. It
rechecks Pod UID, running container ID and selection before opening the cgroup.
It rejects unsupported filesystems, unsafe paths, symlinks, mount crossings,
ambiguous identities and unexpected control values, then verifies each write.

**The writable host mount exposes other cgroup controls too.** These checks
constrain the application's normal behavior; they are not a host security
boundary. A compromised agent could change other controls and disrupt workloads
outside its selectors on that node. Dropping capabilities does not remove this
access.

Node/Pod labels, Pod names and container filters are selection controls, not
authorization. Someone able to set matching Pod labels can opt their workloads
into the policy within the configured scope. Administrators must control the
policy configuration and any labels intended to represent approval.

Namespace-scoped RBAC restricts API access, not the host mount. Pod `get`/`list`
permissions expose full Pod specifications, including literal environment
values, even though the application decodes only the fields it needs. They do
not grant access to Secrets, Pod logs or Pod exec. Node `get` permission is
cluster-scoped and is not restricted by the node selector.

## Deployment

Use a dedicated namespace controlled by cluster administrators. Restrict who
can modify its workloads, service account and Helm configuration. Narrow the
node and namespace scope to the workloads that need the policy.

Build the image from reviewed source, scan it using your organization's tools,
push it to your registry and deploy by digest. The repository does not require
or assume a publicly published project image.

The writable hostPath does not satisfy the Kubernetes Baseline or Restricted
Pod Security Standards. Use an explicit administrator-approved exception for
this workload; do not broaden the container's privileges when admission fails.
The health port needs no Service or public exposure. Cluster network controls
must allow API requests and kubelet health probes.

## Operational limits

`memory.oom.group=0` permits individual-process OOM killing. It does not guarantee
that only one process dies or that PID 1 survives. Partial process failure can
leave an application unhealthy. Setting `1` enables grouped OOM killing for the
container cgroup. Ancestor cgroup policy, node OOM and kubelet eviction can still
terminate workloads. Memory limits are not changed.

There is a startup/reconciliation window before a new container is configured.
Removing selection or uninstalling stops future writes; existing values persist
until container recreation or another writer changes them. Explicitly apply the
intended value before removal if required. Two installations selecting the same
container with different values will compete; avoid overlapping policies.

## Reporting

Report vulnerabilities privately through
[GitHub's vulnerability reporting form](https://github.com/ignacyr/kube-oom-policy/security/advisories/new).
Do not post vulnerabilities or secrets in public issues.

## References

- [Linux cgroup v2 memory controller](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#memory)
- [Kubernetes hostPath risks](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)
- [Kubernetes Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
- [Kubernetes RBAC good practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)
