---
title: "Examples"
linkTitle: "Examples"
weight: 5
description: >
  Anex deployment examples
---

### VirtualService

A standard Kubernetes `Service` cannot route traffic to virtual pods because they run on remote machines outside the cluster network. Anex ships a `VirtualService` CRD (`anex.sh/v1alpha1`, short name `vsvc`) that creates a mirror `Service` of the same name and port, but routes traffic through the gateway to the remote pod.

Constraints:
- Pods to be targeted must carry the annotation `virtual: "true"`
- The `gateway.selector` must match the gateway pod's labels — by default the chart sets `gpu-provider-gateway: "true"`
- TCP only; `targetPort` must be an integer (named ports are not supported)
- `Type` may be `ClusterIP` (default) or `NodePort`

Use `kubectl port-forward service/<name> <local-port>:<service-port>` for ad-hoc access, or place an Ingress in front of the generated Service.

```yaml
apiVersion: anex.sh/v1alpha1
kind: VirtualService
metadata:
  name: fancy-page
spec:
  gateway:
    selector:
      gpu-provider-gateway: "true"
  service:
    # type: ClusterIP   # default; NodePort also supported
    selector:
      app: nginx
    ports:
      - name: http
        port: 80
        targetPort: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
      annotations:
        anex.sh/region: "europe"
        anex.sh/gpu-names: "RTX 4090"
        anex.sh/price-max: "0.3"
        # marks the pod as a valid VirtualService target
        virtual: "true"
    spec:
      containers:
        - name: nginx
          image: nginx:stable
          command: [ "nginx" ]
          args: [ "-g", "daemon off;" ]
          ports:
            - containerPort: 80
          volumeMounts:
            - name: index-html
              mountPath: /usr/share/nginx/html/index.html
              subPath: index.html
      volumes:
        - name: index-html
          configMap:
            name: nginx-index
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
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: nginx-index
data:
  index.html: |
    <!DOCTYPE html>
    <html>
    <head><title>Anex</title><meta charset="utf-8" /></head>
    <body><h1>Hello from a rented GPU</h1></body>
    </html>
```

For end-to-end examples (llama.cpp on Minikube and AWS with TLS Ingress + bearer auth), see the [project README](https://github.com/anex-sh/anex#deployment-examples).
