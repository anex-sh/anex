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

You can control machine selection by adding annotations to your Pod specification. All annotations use the prefix `gpu-provider.glami.cz/`.

#### Machine Quality

|  | |      |                                                 |
|------------|------------------------|------|-------------------------------------------------|
| `verified-only` | Only select verified machines                          | `bool` | `gpu-provider.glami.cz/verified-only: "true"`   |
| `datacenter-only` | Only select datacenter machines (no consumer hardware) | `bool` | `gpu-provider.glami.cz/datacenter-only: "true"` |

#### Allowed Regions

|  |  | |  |
|------------|-------------|-------|---------|
| `region` | Specify allowed regions (comma-separated) | `europe`, `north-america`, `asia-pacific`, `africa`, `south-america`, `oceania` | `gpu-provider.glami.cz/region: "europe,north-america"` |

#### GPU Names and SM

|  |  |  |
|------------|-------------|---------|
| `gpu-names` | Comma-separated list of allowed GPU names | `gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"` |
| `compute-cap` | Comma-separated list of allowed CUDA compute capabilities | `gpu-provider.glami.cz/compute-cap: "8.6,8.9"` |

#### GPU Filters

All numeric filters support three variants:
- Exact value: `<field-name>`
- Minimum value: `<field-name>-min`
- Maximum value: `<field-name>-max`

**Note:** If an exact value is specified, min/max values for that field are ignored.

|  |  |  |  |
|------------|-------------|------|---------|
| `gpu-count` | Number of GPUs | count | `gpu-provider.glami.cz/gpu-count: "2"` |
| `vram` | VRAM per GPU | MB | `gpu-provider.glami.cz/vram: "24576"` |
| `vram-total` | Total VRAM across all GPUs | MB | `gpu-provider.glami.cz/vram-total: "49152"` |
| `vram-bandwidth` | GPU memory bandwidth | GB/s | `gpu-provider.glami.cz/vram-bandwidth: "900.0"` |
| `tflops` | Total TFLOPS | TFLOPS | `gpu-provider.glami.cz/tflops: "82.0"` |
| `cuda` | CUDA version | version | `gpu-provider.glami.cz/cuda: "12.1"` |
| `cpu` | Number of CPU cores | cores | `gpu-provider.glami.cz/cpu: "8"` |
| `ram` | System RAM | MB | `gpu-provider.glami.cz/ram: "32768"` |
| `price` | Exact price per hour | USD/hour | `gpu-provider.glami.cz/price: "0.50"` |
| `upload-speed` | Upload speed | Mbps | `gpu-provider.glami.cz/upload-speed: "1000"` |
| `download-speed` | Download speed | Mbps | `gpu-provider.glami.cz/download-speed: "1000"` |

##### VastAI-Specific Filters

|  |  |  |  |
|------------|-------------|------|---------|
| `vastai-dlperf` | VastAI DLPerf benchmark score | score | `gpu-provider.glami.cz/vastai-dlperf: "100.0"` |
| `vastai-dlperf-min` | Minimum DLPerf score | score | `gpu-provider.glami.cz/vastai-dlperf-min: "50.0"` |
| `vastai-dlperf-max` | Maximum DLPerf score | score | `gpu-provider.glami.cz/vastai-dlperf-max: "150.0"` |

### Example Pod Specification

Here's a complete example showing how to use these annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-gpu-workload
  annotations:
    # Only verified datacenter machines in Europe
    gpu-provider.glami.cz/verified-only: "true"
    gpu-provider.glami.cz/datacenter-only: "true"
    gpu-provider.glami.cz/region: "europe"
    
    # GPU requirements: 2x RTX 4090 or RTX 3090
    gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"
    gpu-provider.glami.cz/gpu-count: "2"
    gpu-provider.glami.cz/vram-min: "20480"  # At least 20GB per GPU
    
    # CPU and RAM requirements
    gpu-provider.glami.cz/cpu-min: "8"
    gpu-provider.glami.cz/ram-min: "32768"  # At least 32GB RAM
    
    # Price constraint
    gpu-provider.glami.cz/price-max: "1.50"  # Maximum $1.50 per hour
    
    # Network requirements
    gpu-provider.glami.cz/download-speed-min: "1000"  # At least 1Gbps
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
