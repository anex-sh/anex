---
title: "Getting Started"
linkTitle: "Getting Started"
weight: 2
description: >
  Quick start guide to run your first GPU workload with GPU Provider.
---

## Minikube

In case you want to test GPU Provider with minimal impact on your existing infrastructure, we recommend using Minikube.
The GPU provider needs to expose a public endpoint for remote machines to connect to, therefore, running it locally is usually not feasible.
But it's completely fine to run Minikube on EC2 or other cloud instances.

We can expose the gateway using a NodePort. To make this publicly accessible, the easiest way is to map ports on Minikube start. 

If using AWS EC2 don't forget to allow UDP port 31000 in the Security Groups.

```bash
minikube start --driver=docker --listen-address=0.0.0.0 --ports=31000:31000/udp
```

Then configure Gateway class to a `node-port` type and set the domain to the machine's public IP in your helm `values.yaml`

```yaml
deployment:
  gateway:
    class: "node-port"
    domain: "3.44.111.222"
    nodePort: 31000
```

The rest of the setup is the same as for AWS provider.

## AWS

### Prerequisites

Before you begin, ensure you have:
- [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/): GPU Provider needs to expose an UDP endpoint for the Gateway
- (optional) [ExternalDNS](https://github.com/kubernetes-sigs/external-dns): To set stable DNS names for the Gateway and speed up uptime after reinstall



### Installation

1. Create [API Key](https://cloud.vast.ai/manage-keys/) in your VastAI account 
2. Create minimal configuration `quickstart.values.yaml` for the Helm deployment

    ```yaml
    deployment:
      gateway:
        class: "aws-eks"

    appConfig:
      cluster:
        # cluster UUID must unique in your VastAI account
        clusterUUID: "mycluster"
    
      cloudProvider:
        vastAI:
          # Api key from previous step
          apiKey: ""
    
      virtualNode:
        # Node name must be unique in your Kubernetes cluster
        nodeName: "virtual-node"
        
        # Any labels you might want to use for pod's nodeSelector or affinity 
        labels:
          node-provider: vastai
    ```

3. Install GPU Provider helm chart

    ```bash
    helm upgrade --install gpu-provider \
      --namespace gpu-provider --create-namespace \
      -f quickstart.values.yaml \
      oci://public.ecr.aws/m4v1f8q5/gpu-provider/helm \
      --version 0.4.1
    ```

4. When provider is up and running you will see a new node in your cluster



### Deploying GPU Workloads

Below you can see a basic pod definition to be scheduled on the virtual node. Pod will be placed on the node by your acting scheduler. 
From there it's lifecycle is handled by the virtual kubelet. Machine running pod's container will be provisioned in the VastAI cloud.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: dummy
  annotations:
    # Use annotations to specify machine requirements
    gpu-provider.glami.cz/region: "europe"
    gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"
    gpu-provider.glami.cz/price-max: "0.5"
spec:
  containers:
    - name: dummy
      image: ubuntu:22.04
      # Command needs to be explicitly set; image default will not work for now
      command: ['sleep', '3600']
  nodeSelector:
    node-provider: vastai
  # Virtual node has following taints by default
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

See [Running workload](/docs/examples/) for more details on machine selection and networking.

