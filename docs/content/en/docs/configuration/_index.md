---
title: "Configuration"
linkTitle: "Configuration"
weight: 3
description: >
  Helm chart values and provider configuration
---

The Anex Helm chart takes a single `values.yaml`. All settings under `appConfig` map 1:1 to the virtual kubelet provider config (YAML on disk inside the pod). Every nested key can also be overridden by an environment variable formed from the path in `SCREAMING_SNAKE_CASE` joined by underscores — e.g. `cloudProvider.vastAI.apiKey` → `CLOUDPROVIDER_VASTAI_APIKEY`.

### Deployment

```yaml
deployment:
  # The provider pod can run on any node; restrict if useful for stability
  affinity: {}
  nodeSelector: {}
  tolerations: []

  # ServiceAccount annotations — e.g. AWS IAM role for ECR pull
  serviceAccount:
    annotations: {}

  # mTLS for the virtual kubelet API (optional)
  tls:
    cert: ""
    key: ""
    caCert: ""

  gateway:
    class: ""           # enum: [aws-eks | node-port]
    domain: ""          # public hostname or IP of the gateway
    nodePortUDP: 31000  # Wireguard (Vast.AI) — used with class: node-port
    nodePortTCP: 31001  # wstunnel (RunPod) — used with class: node-port
    config:
      # Pre-existing Secret holding gateway Wireguard config under key
      # config.yaml. If empty, gateway-init generates fresh keys at startup
      # into an emptyDir.
      secretName: ""

  containers:
    virtualKubelet:
      enabled: true
      image:
        repository: "public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet"
        tag: ""           # defaults to chart appVersion
        pullPolicy: Always
      resources: {}

    gateway:
      image:
        repository: "public.ecr.aws/m4v1f8q5/gpu-provider/gateway"
        tag: ""
        pullPolicy: Always
      resources: {}

    gatewayController: {}
```

### Application Configuration

```yaml
appConfig:
  logLevel: "info"

  # Multiple clusters may share a cloud provider account.
  # clusterUUID separates them so garbage collection works correctly.
  cluster:
    clusterUUID: ""

  # CDN URLs for binaries downloaded onto Vast.AI machines (agent, wireproxy,
  # promtail). Leave empty to use the Anex default CDN — in that case
  # anexAuthToken must be set.
  cdn:
    agentURL: ""
    wireproxyURL: ""
    promtailURL: ""
    anexAuthToken: ""

  cloudProvider:
    # Required. One of: vastai, runpod, mock
    active: "vastai"

    vastAI:
      apiKey: ""

    runPod:
      apiKey: ""
      # URLs for binaries downloaded onto RunPod containers
      initURL: ""
      agentURL: ""
      wireproxyURL: ""
      wstunnelURL: ""
      promtailURL: ""

  virtualNode:
    nodeName: "virtual-node"
    # Hard pod limit reported by the virtual node
    podLimit: "10"
    # Resources advertised to the scheduler
    cpu: "1000"
    memory: "1000Gi"
    # Extra labels and taints. Default taints are always applied.
    labels: {}
    taints: []

  provisioning:
    # Maximum provisioning attempts; most steps are retried until timeout
    maxRetries: 10

    # Total budget for one provisioning attempt
    startupTimeout: "10m"

    # Container agent reports status periodically; if it fails to report
    # within this window the pod is restarted and the machine destroyed.
    statusReportTimeout: "2m"

    # If a machine fails provisioning or fails to start within startupTimeout,
    # ban it for `timeout`. Vast.AI only — RunPod has no machine bans.
    machineBansStore:
      localFile:
        enable: true
        timeout: "1d"

  gateway:
    # Path inside the pod where Wireguard config is mounted
    configPath: /gateway/config.yaml

  # Push container logs to a Loki gateway. The only way to export logs from
  # virtual pods on managed Kubernetes.
  promtail:
    enable: false
    clients: []
    # clients:
    #   - url: "https://loki-gateway.example.com/loki/api/v1/push"
    #     basicAuth:
    #       username: ""
    #       password: ""

  # mTLS paths inside the pod (set automatically when deployment.tls is used)
  tls:
    certPath: ""
    keyPath: ""
    caCertPath: ""
```
