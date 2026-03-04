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
