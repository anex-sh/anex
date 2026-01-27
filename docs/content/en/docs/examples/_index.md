---
title: "Running GPU Workloads"
linkTitle: "Running GPU Workloads"
weight: 4
description: >
  Guide to deploying workload on a virtual nodes
---

## VastAI

Currently the only supported cloud provider. More are to come.

### Networking

Since VastAI does not allow us to run Wireguard in kernel space the VPN is done by Wireproxy in user space. This means all traffic to the cluster needs to go either over HTTP proxy or through tunnel.

Proxy is available at `localhost:3128`

TCP Tunnels can be set by setting ENV variables in format `GW_TUNNEL_<port-number>` = `private address to tunnel to`. Set these variables in pod's definition.



### Machine Selection

You can control machine selection by adding annotations to your Pod specification. All annotations use the prefix `gpu-provider.glami.cz/`.

#### Boolean Filters

| Annotation | Description | Example |
|------------|-------------|---------|
| `verified-only` | Only select verified machines | `gpu-provider.glami.cz/verified-only: "true"` |
| `datacenter-only` | Only select datacenter machines (no consumer hardware) | `gpu-provider.glami.cz/datacenter-only: "true"` |

#### Region Filters

| Annotation | Description | Valid Values | Example |
|------------|-------------|--------------|---------|
| `region` | Specify allowed regions (comma-separated) | `europe`, `north-america`, `asia-pacific`, `africa`, `south-america`, `oceania` | `gpu-provider.glami.cz/region: "europe,north-america"` |

#### List Filters

| Annotation | Description | Example |
|------------|-------------|---------|
| `gpu-names` | Comma-separated list of allowed GPU names | `gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"` |
| `compute-cap` | Comma-separated list of allowed CUDA compute capabilities | `gpu-provider.glami.cz/compute-cap: "8.6,8.9"` |

#### GPU Filters

All numeric filters support three variants:
- Exact value: `<field-name>`
- Minimum value: `<field-name>-min`
- Maximum value: `<field-name>-max`

**Note:** If an exact value is specified, min/max values for that field are ignored.

| Annotation | Description | Unit | Example |
|------------|-------------|------|---------|
| `gpu-count` | Number of GPUs | count | `gpu-provider.glami.cz/gpu-count: "2"` |
| `gpu-count-min` | Minimum number of GPUs | count | `gpu-provider.glami.cz/gpu-count-min: "1"` |
| `gpu-count-max` | Maximum number of GPUs | count | `gpu-provider.glami.cz/gpu-count-max: "4"` |
| `vram` | VRAM per GPU | MB | `gpu-provider.glami.cz/vram: "24576"` |
| `vram-min` | Minimum VRAM per GPU | MB | `gpu-provider.glami.cz/vram-min: "16384"` |
| `vram-max` | Maximum VRAM per GPU | MB | `gpu-provider.glami.cz/vram-max: "49152"` |
| `vram-total` | Total VRAM across all GPUs | MB | `gpu-provider.glami.cz/vram-total: "49152"` |
| `vram-total-min` | Minimum total VRAM | MB | `gpu-provider.glami.cz/vram-total-min: "32768"` |
| `vram-total-max` | Maximum total VRAM | MB | `gpu-provider.glami.cz/vram-total-max: "98304"` |
| `vram-bandwidth` | GPU memory bandwidth | GB/s | `gpu-provider.glami.cz/vram-bandwidth: "900.0"` |
| `vram-bandwidth-min` | Minimum memory bandwidth | GB/s | `gpu-provider.glami.cz/vram-bandwidth-min: "500.0"` |
| `vram-bandwidth-max` | Maximum memory bandwidth | GB/s | `gpu-provider.glami.cz/vram-bandwidth-max: "1000.0"` |
| `tflops` | Total TFLOPS | TFLOPS | `gpu-provider.glami.cz/tflops: "82.0"` |
| `tflops-min` | Minimum TFLOPS | TFLOPS | `gpu-provider.glami.cz/tflops-min: "50.0"` |
| `tflops-max` | Maximum TFLOPS | TFLOPS | `gpu-provider.glami.cz/tflops-max: "100.0"` |
| `cuda` | CUDA version | version | `gpu-provider.glami.cz/cuda: "12.1"` |
| `cuda-min` | Minimum CUDA version | version | `gpu-provider.glami.cz/cuda-min: "11.8"` |
| `cuda-max` | Maximum CUDA version | version | `gpu-provider.glami.cz/cuda-max: "12.4"` |

#### CPU and RAM Filters

| Annotation | Description | Unit | Example |
|------------|-------------|------|---------|
| `cpu` | Number of CPU cores | cores | `gpu-provider.glami.cz/cpu: "8"` |
| `cpu-min` | Minimum CPU cores | cores | `gpu-provider.glami.cz/cpu-min: "4"` |
| `cpu-max` | Maximum CPU cores | cores | `gpu-provider.glami.cz/cpu-max: "16"` |
| `ram` | System RAM | MB | `gpu-provider.glami.cz/ram: "32768"` |
| `ram-min` | Minimum system RAM | MB | `gpu-provider.glami.cz/ram-min: "16384"` |
| `ram-max` | Maximum system RAM | MB | `gpu-provider.glami.cz/ram-max: "65536"` |

#### Price Filters

| Annotation | Description | Unit | Example |
|------------|-------------|------|---------|
| `price` | Exact price per hour | USD/hour | `gpu-provider.glami.cz/price: "0.50"` |
| `price-min` | Minimum price per hour | USD/hour | `gpu-provider.glami.cz/price-min: "0.10"` |
| `price-max` | Maximum price per hour | USD/hour | `gpu-provider.glami.cz/price-max: "1.00"` |

#### Network Speed Filters

| Annotation | Description | Unit | Example |
|------------|-------------|------|---------|
| `upload-speed` | Upload speed | Mbps | `gpu-provider.glami.cz/upload-speed: "1000"` |
| `upload-speed-min` | Minimum upload speed | Mbps | `gpu-provider.glami.cz/upload-speed-min: "500"` |
| `upload-speed-max` | Maximum upload speed | Mbps | `gpu-provider.glami.cz/upload-speed-max: "10000"` |
| `download-speed` | Download speed | Mbps | `gpu-provider.glami.cz/download-speed: "1000"` |
| `download-speed-min` | Minimum download speed | Mbps | `gpu-provider.glami.cz/download-speed-min: "500"` |
| `download-speed-max` | Maximum download speed | Mbps | `gpu-provider.glami.cz/download-speed-max: "10000"` |

#### VastAI-Specific Filters

| Annotation | Description | Unit | Example |
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
