---
title: "Getting Started"
linkTitle: "Getting Started"
weight: 2
description: >
  Quick start guide to run your first GPU workload with Anex.
---

## Minikube

For testing Anex with minimal impact on your existing infrastructure, Minikube is the easiest option.
Anex's gateway needs a publicly reachable endpoint for remote machines to connect to, so running on a laptop is usually not feasible — but running Minikube on an EC2 (or any public-IP) instance works fine.

The gateway is exposed via a NodePort. Map the ports when starting Minikube:

```bash
minikube start --driver=docker --listen-address=0.0.0.0 \
  --ports=31000:31000/udp,31001:31001/tcp
```

If using AWS EC2, allow UDP `31000` and TCP `31001` in the Security Group. The TCP port is the wstunnel endpoint used by RunPod machines (Vast.AI uses UDP).

Configure the gateway class to `node-port` and set `domain` to the machine's public IP in `values.yaml`:

```yaml
deployment:
  gateway:
    class: "node-port"
    domain: "3.44.111.222"
    nodePortUDP: 31000
    nodePortTCP: 31001
```

The rest of the setup is identical to AWS.

## AWS

### Prerequisites

Before you begin, ensure you have:
- [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/) — Anex needs to expose a UDP endpoint for the gateway
- (optional) [ExternalDNS](https://github.com/kubernetes-sigs/external-dns) — for stable DNS names so reinstalls don't require re-handshaking peers

### Installation

1. Create an [API key](https://cloud.vast.ai/manage-keys/) in your Vast.AI account.

2. Create minimal `quickstart.values.yaml`:

    ```yaml
    deployment:
      gateway:
        class: "aws-eks"

    appConfig:
      cluster:
        # clusterUUID must be unique across clusters under the same Vast.AI account
        clusterUUID: "mycluster"

      cloudProvider:
        # one of: vastai, runpod, mock
        active: "vastai"
        vastAI:
          apiKey: ""    # API key from step 1

      virtualNode:
        # Node name must be unique in your Kubernetes cluster
        nodeName: "virtual-node"
        # Labels picked up by pod nodeSelector / affinity
        labels:
          node-provider: vastai
    ```

3. Install the Anex Helm chart:

    ```bash
    helm upgrade --install anex \
      --namespace anex --create-namespace \
      -f quickstart.values.yaml \
      oci://public.ecr.aws/m4v1f8q5/gpu-provider/helm \
      --version 1.0.0
    ```

4. Once the provider is up, you'll see a new node in your cluster.

### Deploying GPU Workloads

A minimal pod that schedules onto the virtual node — the default scheduler places it there, and the virtual kubelet provisions a machine in Vast.AI to run the container.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: dummy
  annotations:
    # Use annotations to specify machine requirements
    anex.sh/region: "europe"
    anex.sh/gpu-names: "RTX 4090,RTX 3090"
    anex.sh/price-max: "0.5"
spec:
  containers:
    - name: dummy
      image: ubuntu:22.04
      # Command must be explicitly set; image default is not used
      command: ['sleep', '3600']
  nodeSelector:
    node-provider: vastai
  # Default taints on the virtual node
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

See [Running GPU Workloads](/docs/running-workloads/) for full details on machine selection, and [Examples](/docs/examples/) for the `VirtualService` CRD that exposes virtual pods through a Service.
