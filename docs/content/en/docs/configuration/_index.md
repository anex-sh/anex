---
title: "Configuration"
linkTitle: "Configuration"
weight: 3
description: >
  Configuration guide
---

This document describes configuration options available for the Helm chart and the virtual kubelet. Both can be obviously set from shared values.yaml file.

### Deployment

```yaml
deployment:
  # virtual kubelet pod can run on any node in the cluster; limit the restarts and rescheduling for better performance
  affinity: {}
  nodeSelector: {}
  tolerations: []

  # VK Service Account annotation allows you to reference AWS IAM role and gain permission to generate an access token for ECR
  serviceAccount:
    annotations: {}

  gateway:
    # enum: [aws-eks | node-port]
    class: ""
    # hostname or IP address of the gateway
    domain: ""
    # effective only with 'node-port' class
    nodePort: 31000

  containers:
    virtualKubelet:
      image:
        repository: "391135486350.dkr.ecr.eu-central-1.amazonaws.com/gpu-provider"
        tag: latest
        pullPolicy: Always

      resources:
        requests:
          cpu: "0.5"
          memory: "512Mi"
        limits:
          cpu: "0.5"
          memory: "512Mi"

    gateway:
      image:
        repository: "public.ecr.aws/m4v1f8q5/gpu-provider/gateway"
        tag: latest
        pullPolicy: Always

      resources:
        requests:
          cpu: "0.5"
          memory: "256Mi"
        limits:
          cpu: "0.5"
          memory: "256Mi"
```

### Virtual Kubelet Configuration

```yaml
appConfig:
  logLevel: "info"

  # multiple clusters can run under same cloud provider account;
  # correctly differentiate them by clusterUUID so garbage collection works properly
  cluster:
    clusterUUID: ""

  cloudProvider:
    vastAI:
      apiKey: ""

  virtualNode:
    # virtual node is created under with following name 
    nodeName: "virtual-node"
    # current hard limit is 64 pods per node
    podLimit: "10"
    # virtual node will report the following resources and scheduler will consider them when scheduling pods 
    cpu: "1000"
    memory: "1000Gi"
    labels: {}
    # additional taints for the node; see default taints below
    taints: []

  provisioning:
    # maximum provisioning attempts; most provisioning steps are retried until timeout;  
    maxRetries: 10
    
    # timeout for complete provisioning
    startupTimeout: "10m"
    
    # container agent reports status of a remote containers; should it fail to report within this timeout; 
    # the pod will be restarted and the running machine destroyed
    statusReportTimeout: "2m"
    
    # should a machine fail during provisioning or fail to start within before startupTimeout
    # it is banned from further attempts for specified period 
    machineBansStore:
      localFile:
        enable: true
        timeout: "1d"


  # push container logs to the Loki gateway; right now this is the only way to export logs on managed Kubernetes 
  promtail:
    enable: false
    clients: []
    # clients: []
    #   - url: "https://loki-gateway.example.com/loki/api/v1/push"
    #     basicAuth:
    #       username: ""
    #       password: ""
```
