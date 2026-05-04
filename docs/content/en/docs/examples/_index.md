---
title: "Examples"
linkTitle: "Examples"
weight: 5
description: >
  GPU Provider deployment examples
---

### Virtual Service

Standard Kubernetes Services do not work with virtual pods. The `VirtualService` must be deployed. 
It automatically creates a mirror `Service` with the same name and port, but routes traffic over Gateway, and therefore can reach the pods.

There are a few rough edges around the VirtualService integration at the moment:
- Pods intended to be targeted must be **annotated** with `virtual: "true"`
- The Service also needs to include the `gateway:` part as shown in the example (currently hardcoded)
- Only `ClusterIP` type is supported

Use `kubectl port-forward service/fancy-page <local-port>:80` to query

Coming soon:
- Support for the full Kubernetes Service API, including `LoadBalancer` and `NodePort` types


```yaml
apiVersion: anex.sh/v1alpha1
kind: VirtualService
metadata:
  name: fancy-page
spec:
  # gateway key is hard-coded default; for now it must be explicitly set 
  gateway:
    selector:
      custom-gateway: "true"
  service:
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
        anex.sh/verified-only: "true"
        # mark the pod as virtual to be a valid Virtual Service target
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
    <head>
        <title>Welcome to VastAI</title>
        <meta charset="utf-8" />
    </head>
    <body>
    <h1>Welcome to VastAI</h1>
    <p>The system is running.</p>
    </body>
    </html>
```
