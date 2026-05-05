---
title: "Running GPU Workloads"
linkTitle: "Running GPU Workloads"
weight: 4
description: >
  Deploying workloads on the Anex virtual node
---

## Networking

Vast.AI does not allow Wireguard in kernel space, so the VPN runs in user space via Wireproxy. RunPod blocks UDP entirely, so Wireguard is tunneled over WebSocket (wstunnel). In both cases, traffic from the remote container reaches the cluster either through an HTTP proxy or via TCP tunnels.

- **HTTP proxy** is available inside the container at `localhost:3128`.
- **TCP tunnels** are configured via env vars on the pod: `GW_TUNNEL_<port>` = `<destination address>`. Anex pushes those into the agent and wireproxy reroutes the local port to the destination over the VPN.

## Machine Selection

Annotations on the pod control machine selection. Shared filters use the `anex.sh/` prefix; provider-specific filters use `vastai.anex.sh/` and `runpod.anex.sh/`.

#### Allowed Regions

| Annotation | Description | Allowed values |
|---|---|---|
| `anex.sh/region` | Comma-separated list of allowed regions | `europe`, `north-america`, `asia-pacific`, `africa`, `south-america`, `oceania` |

#### GPU Identification

| Annotation | Description | Example |
|---|---|---|
| `anex.sh/gpu-names` | Comma-separated list of allowed GPU names | `anex.sh/gpu-names: "RTX 4090,RTX 3090"` |
| `anex.sh/compute-cap` | Comma-separated list of allowed CUDA compute capabilities | `anex.sh/compute-cap: "8.6,8.9"` |

#### Numeric Filters

All numeric filters support three variants:
- Exact value: `<field>`
- Minimum: `<field>-min`
- Maximum: `<field>-max`

If an exact value is set, the corresponding `-min`/`-max` are ignored.

| Annotation | Description | Unit |
|---|---|---|
| `anex.sh/gpu-count` | Number of GPUs | count |
| `anex.sh/vram` | VRAM per GPU | MB |
| `anex.sh/vram-total` | Total VRAM across all GPUs | MB |
| `anex.sh/vram-bandwidth` | GPU memory bandwidth | GB/s |
| `anex.sh/tflops` | Total TFLOPS | TFLOPS |
| `anex.sh/cuda` | CUDA version | version |
| `anex.sh/cpu` | Number of CPU cores | cores |
| `anex.sh/ram` | System RAM | MB |
| `anex.sh/price` | Price per hour (whole machine) | USD/hour |
| `anex.sh/upload-speed` | Upload speed | Mbps |
| `anex.sh/download-speed` | Download speed | Mbps |
| `anex.sh/disk-space-gb` | Container disk to allocate | GB |
| `anex.sh/disk-bw` | Disk bandwidth | MB/s |

#### Vast.AI-Specific

| Annotation | Description | Type |
|---|---|---|
| `vastai.anex.sh/verified-only` | Only select Vast.AI-verified machines (default `true`) | bool |
| `vastai.anex.sh/datacenter-only` | Only select datacenter machines (no consumer hardware) | bool |
| `vastai.anex.sh/dlperf` | DLPerf benchmark score (also `-min` / `-max`) | float |

#### RunPod-Specific

| Annotation | Description | Values |
|---|---|---|
| `runpod.anex.sh/cloud-type` | RunPod cloud tier | `SECURE` or `COMMUNITY` |
| `runpod.anex.sh/datacenter-ids` | Comma-separated, priority-ordered datacenter IDs | e.g. `EU-RO-1,US-CA-2` |
| `runpod.anex.sh/keep-gpu-type-priority` | Preserve GPU type priority order during selection | bool |

### Example Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-gpu-workload
  annotations:
    # Region + GPU
    anex.sh/region: "europe"
    anex.sh/gpu-names: "RTX 4090,RTX 3090"
    anex.sh/gpu-count: "2"
    anex.sh/vram-min: "20480"     # ≥ 20GB per GPU

    # CPU and RAM
    anex.sh/cpu-min: "8"
    anex.sh/ram-min: "32768"      # ≥ 32GB RAM

    # Price
    anex.sh/price-max: "1.50"

    # Network
    anex.sh/download-speed-min: "1000"

    # Vast.AI: only verified datacenter hardware
    vastai.anex.sh/verified-only: "true"
    vastai.anex.sh/datacenter-only: "true"
spec:
  containers:
    - name: training
      image: pytorch/pytorch:latest
      command: ["python", "train.py"]
  nodeSelector:
    node-provider: vastai
  tolerations:
    - key: "virtual-kubelet.io/provider"
      operator: "Equal"
      value: "vastai"
      effect: "NoSchedule"
    - key: "ignore-taint.cluster-autoscaler.kubernetes.io/manual-ignore"
      operator: "Equal"
      value: "true"
      effect: "NoSchedule"
```

### Filter Behaviour

- **Exact vs range** — when an exact value is set, the matching `-min`/`-max` filters are ignored.
- **AND across filters** — every annotation that is set must be satisfied.
- **OR within a list** — `gpu-names`, `compute-cap`, `region`, etc. accept any value from the list.
- **Default ordering** — candidates are sorted by price ascending.
- **Bans (Vast.AI)** — machines that fail provisioning are banned for the configured timeout and skipped on subsequent retries. RunPod does not implement bans.
