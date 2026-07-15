# kt — Kubernetes Toolkit

A fast CLI for inspecting Kubernetes clusters, with color-coded output and Karpenter-aware node pool information.

## Installation

```sh
go install kt
```

Or build from source:

```sh
go build -o kt .
```

## Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--context` | current context | kubectl context to use |
| `--color` | `always` | Color output: `always`, `auto`, `none` |

## Commands

### `kt pods`

Lists unhealthy pods across the cluster. By default only pods **not** in a healthy state are shown.

```text
NAMESPACE  NAME       READY  ARCH   NODEPOOL      STATUS            RESTARTS  AGE
prod       api-xk9q   0/1    amd64  default       CrashLoopBackOff  5 (2m ago)  1h
prod       worker-2   0/1    arm64  default-arm64  OOMKilled         1 (5m ago)  30m
```

#### Flags

| Flag | Short | Description |
| --- | --- | --- |
| `--all` | `-a` | Show all pods, not just unhealthy ones |
| `--namespace` | `-n` | Limit to a specific namespace (default: all namespaces) |

---

### `kt nodes`

Lists all nodes with capacity information and a Karpenter nodepool summary.

```text
NAME                                         ARCH   NODEPOOL       INSTANCE      CPUS  MEMORY  PODS  AGE   OS IMAGE
ip-10-0-1-100.us-west-2.compute.internal     arm64  default-arm64  m7g.2xlarge      8   30Gi   110  2d    Bottlerocket OS 1.19.0
ip-10-0-1-200.us-west-2.compute.internal     amd64  default        m7i.xlarge       4   15Gi   110  5d    Amazon Linux 2

Nodepools
NAME           NODECLASS   NODES  CPUS  MEMORY  PODS  READY  AGE
default        default         5    20   75Gi   550  True   11d
default-arm64  default         1     8   30Gi   110  True   31h
```

The Nodepools section requires Karpenter (`karpenter.sh/v1`) and is silently skipped if not installed. Node counts, CPU, memory, and pod capacity in the summary are aggregated from the live node list.

#### Flags

| Flag | Short | Description |
| --- | --- | --- |
| `--arch` | `-A` | Filter rows by exact architecture |
| `--autoscaler` | `-a` | Filter rows by exact autoscaler (`karpenter`, `managed`, `autoscaler`) |
| `--grep` | `-g` | Filter rows by Perl-compatible regexp (matched against the full rendered row) |
| `--instance` | `-i` | Filter rows by exact instance type |
| `--nodepool` | `-p` | Filter rows by exact nodepool name |
| `--watch` | `-w` | Refresh interval in seconds (0 = run once) |

---

### `kt node <name>`

Shows detailed information about a single node followed by all pods running on it and recent events.

**Node info:**

```text
Name:                    ip-10-0-1-100.us-west-2.compute.internal
Hostname:                ip-10-0-1-100.us-west-2.compute.internal
InternalIP:              10.0.1.100
Operating System:        linux
Architecture:            arm64
OS Image:                Bottlerocket OS 1.19.0
Kernel Version:          5.15.167
Container Runtime:       containerd://1.7.27
Kubelet Version:         v1.32.1-eks-5d632d8
Taints:                  dedicated=gpu:NoSchedule
```

**Pods** (same columns as `kt pods --all`, scoped to this node):

```text
NAMESPACE  POD              KIND        READY  ARCH   NODEPOOL       INSTANCE     STATUS   RESTARTS  AGE
prod       api-xk9q         Deployment  1/1    arm64  default-arm64  m7g.2xlarge  Running  0         2d
prod       worker-2         StatefulSet 1/1    arm64  default-arm64  m7g.2xlarge  Running  0         5h
```

**Events** (sorted oldest to newest, warnings in red):

```text
TYPE     REASON           FROM     LAST SEEN  MESSAGE
Normal   NodeReady        kubelet  5d         Node ip-10-0-1-100... status is now: NodeReady
Warning  NodeNotReady     kubelet  2m         Node ip-10-0-1-100... status is now: NodeNotReady
```

Use `--watch/-w <seconds>` to auto-refresh at the given interval.

---

### `kt nodepools`

Lists all Karpenter nodepools with their configuration and live resource totals aggregated from the current node list. Requires Karpenter (`karpenter.sh/v1`); exits silently if not installed.

```text
NODEPOOL       NODECLASS  ARCH   OS     CAPACITY-TYPE  INSTANCE-TYPE  INSTANCE-CATEGORY  INSTANCE-GENERATION  INSTANCE-CPU  NO-SCHEDULE    NODES  CPUS  MEMORY  PODS  READY  AGE
default        default    amd64  linux  on-demand      m7i.xlarge                                                               default       5    20    75Gi   550  True   11d
default-arm64  default    arm64  linux  on-demand                     c,m,r              >5                   16,32,64  default-arm64       1     8    30Gi   110  True   31h
```

Node counts, CPU, memory, and pod capacity are aggregated from the live node list. The `INSTANCE-CATEGORY`, `INSTANCE-GENERATION`, and `INSTANCE-CPU` columns reflect the `karpenter.k8s.aws` requirement constraints defined on the nodepool (empty when not specified). The `NO-SCHEDULE` column lists taint values for any `NoSchedule` taints defined on the nodepool.

---

### `kt images`

Lists the unique container images used by one or more pods and the CPU architectures each image supports. Architecture is resolved by fetching the image manifest from the registry (using credentials from `~/.docker/config.json`). Multi-arch images show all supported platforms; single-arch images show the one platform from the image config.

```text
IMAGE                                       ARCHITECTURES
gcr.io/my-project/api:v2.3.1                amd64, arm64
gcr.io/my-project/sidecar:v1.0.0            amd64
nginx:1.27                                  amd64, arm64, arm
```

#### Selecting pods

| Syntax | Behaviour |
| --- | --- |
| `kt images <pod-name>` | Exact pod name match; falls back to prefix match |
| `kt images svc/<service>` | Resolves the service's pod selector, then lists images for all matching pods |
| `kt images -l key=value` | Label selector; repeat `-l` to AND multiple selectors together |
| `kt images -a` | All pods in the namespace (or cluster-wide if no `-n` is given) |
| `kt images -N <node>` | All pods running on the given node (exact name or prefix) |
| `kt images -p <nodepool>` | All pods running on nodes of the given Karpenter nodepool |

`-N` and `-p` can also be combined with any of the other selectors to narrow them down, e.g. `kt images -l app=api -p graviton`.

#### Options

| Flag | Short | Description |
| --- | --- | --- |
| `--namespace` | `-n` | Limit to a specific namespace (default: all namespaces) |
| `--label` | `-l` | Label selector; can be repeated (ANDed together) |
| `--all` | `-a` | List images for all pods |
| `--node` | `-N` | Only pods running on this node (exact name or prefix) |
| `--nodepool` | `-p` | Only pods running on nodes of this Karpenter nodepool |
