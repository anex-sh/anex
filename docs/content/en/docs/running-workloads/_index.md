---
title: "Running GPU Workloads"
linkTitle: "Running GPU Workloads"
weight: 4
description: >
  Guide to deploying workload on a virtual nodes
---

## Networking

Since VastAI does not allow us to run Wireguard in kernel space the VPN is done by Wireproxy in user space. This means all traffic to the cluster needs to go either over HTTP proxy or through tunnel.

Proxy is available at `localhost:3128`

TCP Tunnels can be set by setting ENV variables in format `GW_TUNNEL_<port-number>` = `private address to tunnel to`. Set these variables in pod's definition.



## Machine Selection

You can control machine selection by adding annotations to your Pod specification. All annotations use the prefix `anex.sh/`.

#### Machine Quality

|  | |      |                                                 |
|------------|------------------------|------|-------------------------------------------------|
| `verified-only` | Only select verified machines                          | `bool` | `anex.sh/verified-only: "true"`   |
| `datacenter-only` | Only select datacenter machines (no consumer hardware) | `bool` | `anex.sh/datacenter-only: "true"` |

#### Allowed Regions

|  |  | |  |
|------------|-------------|-------|---------|
| `region` | Specify allowed regions (comma-separated) | `europe`, `north-america`, `asia-pacific`, `africa`, `south-america`, `oceania` | `anex.sh/region: "europe,north-america"` |

#### GPU Names and SM

|  |  |  |
|------------|-------------|---------|
| `gpu-names` | Comma-separated list of allowed GPU names | `anex.sh/gpu-names: "RTX 4090,RTX 3090"` |
| `compute-cap` | Comma-separated list of allowed CUDA compute capabilities | `anex.sh/compute-cap: "8.6,8.9"` |

#### GPU Filters

All numeric filters support three variants:
- Exact value: `<field-name>`
- Minimum value: `<field-name>-min`
- Maximum value: `<field-name>-max`

**Note:** If an exact value is specified, min/max values for that field are ignored.

|  |  |  |  |
|------------|-------------|------|---------|
| `gpu-count` | Number of GPUs | count | `anex.sh/gpu-count: "2"` |
| `vram` | VRAM per GPU | MB | `anex.sh/vram: "24576"` |
| `vram-total` | Total VRAM across all GPUs | MB | `anex.sh/vram-total: "49152"` |
| `vram-bandwidth` | GPU memory bandwidth | GB/s | `anex.sh/vram-bandwidth: "900.0"` |
| `tflops` | Total TFLOPS | TFLOPS | `anex.sh/tflops: "82.0"` |
| `cuda` | CUDA version | version | `anex.sh/cuda: "12.1"` |
| `cpu` | Number of CPU cores | cores | `anex.sh/cpu: "8"` |
| `ram` | System RAM | MB | `anex.sh/ram: "32768"` |
| `price` | Exact price per hour | USD/hour | `anex.sh/price: "0.50"` |
| `upload-speed` | Upload speed | Mbps | `anex.sh/upload-speed: "1000"` |
| `download-speed` | Download speed | Mbps | `anex.sh/download-speed: "1000"` |

##### VastAI-Specific Filters

|  |  |  |  |
|------------|-------------|------|---------|
| `vastai-dlperf` | VastAI DLPerf benchmark score | score | `anex.sh/vastai-dlperf: "100.0"` |
| `vastai-dlperf-min` | Minimum DLPerf score | score | `anex.sh/vastai-dlperf-min: "50.0"` |
| `vastai-dlperf-max` | Maximum DLPerf score | score | `anex.sh/vastai-dlperf-max: "150.0"` |

### Example Pod Specification

Here's a complete example showing how to use these annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-gpu-workload
  annotations:
    # Only verified datacenter machines in Europe
    anex.sh/verified-only: "true"
    anex.sh/datacenter-only: "true"
    anex.sh/region: "europe"
    
    # GPU requirements: 2x RTX 4090 or RTX 3090
    anex.sh/gpu-names: "RTX 4090,RTX 3090"
    anex.sh/gpu-count: "2"
    anex.sh/vram-min: "20480"  # At least 20GB per GPU
    
    # CPU and RAM requirements
    anex.sh/cpu-min: "8"
    anex.sh/ram-min: "32768"  # At least 32GB RAM
    
    # Price constraint
    anex.sh/price-max: "1.50"  # Maximum $1.50 per hour
    
    # Network requirements
    anex.sh/download-speed-min: "1000"  # At least 1Gbps
spec:
  containers:
  - name: training-container
    image: pytorch/pytorch:latest
    command: ["python", "train.py"]
```

### Filter Behavior

- **Exact vs Range**: When you specify an exact value (e.g., `gpu-count: "2"`), the corresponding min/max filters are ignored
- **Multiple Filters**: All specified filters must be satisfied (AND logic)
- **List Filters**: For list filters like `gpu-names`, any value in the list is acceptable (OR logic)
- **Default Ordering**: Machines are ordered by price (ascending) by default
- **Machine Bans**: Machines that fail during startup are temporarily banned and excluded from future selections
