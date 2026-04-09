# Anex

Anex lets you run GPU workloads on cheap cloud GPU providers (like [VastAI](https://vast.ai/)) while managing them through your existing Kubernetes cluster. You write standard Kubernetes manifests, and Anex takes care of renting machines, setting up networking, and making your remote GPU pods feel like they're part of your cluster.

> **⚠️ Experimental Version**: This is an experimental version of Anex. We are aiming to publish a stable release by the end of April 2026. For now, you can use our Docker images and binaries to test the project.

---

## Quick Setup

Get started quickly using the pre-built images from our public registry:

### 1. Create values.quickstart.yaml

```yaml
deployment:
  gateway:
    class: "node-port"
    domain: "your-minikube-ip"  # Use your machine's public IP
    nodePort: 31000             # start minikube with --ports=31000:31000/udp

appConfig:
  cluster:
    clusterUUID: "your-cluster-uuid"  # Must be unique in your VastAI account

  cloudProvider:
    vastAI:
      apiKey: "your-vastai-api-key"   # Get from https://cloud.vast.ai/account/

  virtualNode:
    nodeName: "virtual-node-name"     # Must be unique in your cluster
    labels:
      node-provider: vastai
```

### 2. Deploy Anex

```bash
helm upgrade --install anex --namespace anex --create-namespace \
  -f values.quickstart.yaml \
  oci://public.ecr.aws/m4v1f8q5/gpu-provider/helm \
  --version 0.4.5
```

### 3. Create llama-cpp.yaml

```yaml
# VirtualService: makes the remote pod reachable as a Kubernetes service
apiVersion: anex.sh/v1alpha1
kind: VirtualService
metadata:
  name: llama
spec:
  gateway:
    selector:
      custom-gateway: "true"
  service:
    selector:
      app: llama
    ports:
      - name: http
        port: 80
        targetPort: 8090
---
# The llama.cpp server pod — runs on a rented GPU
apiVersion: v1
kind: Pod
metadata:
  name: llama
  labels:
    app: llama
  annotations:
    gpu-provider.glami.cz/region: "europe"
    gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"
    gpu-provider.glami.cz/price-max: "0.5"
    gpu-provider.glami.cz/verified-only: "true"
    gpu-provider.glami.cz/disk-space-gb: "100"
    virtual: "true"
spec:
  containers:
    - name: llama
      image: ghcr.io/ggml-org/llama.cpp:server-cuda
      command:
        - /bin/sh
        - -c
        - |
          apt-get update && apt-get install -y wget &&
          wget -O /tmp/model.gguf \
            "https://huggingface.co/hugging-quants/Llama-3.2-1B-Instruct-Q4_K_M-GGUF/resolve/main/llama-3.2-1b-instruct-q4_k_m.gguf" &&
          /app/llama-server \
            -m /tmp/model.gguf \
            --port 8090 \
            --host 0.0.0.0 \
            -ngl 99
      ports:
        - containerPort: 8090
      resources:
        limits:
          nvidia.com/gpu: "1"
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

### 4. Deploy the workload

```bash
kubectl apply -f llama-cpp.yaml -n anex
```

Then forward the port to access it:

```bash
kubectl port-forward service/llama 8080:80 -n anex
```

See the [Deployment Examples](#deployment-examples) section below for more examples including AWS with Ingress.

---

## Building from Source

### Prerequisites

- **Go 1.24** or later
- **Docker** for building container images
- **kubectl** configured to access your Kubernetes cluster
- **Helm 3.x** for deploying the chart
- A **container registry** you can push to (Docker Hub, AWS ECR, GitHub Container Registry, etc.)

### Build Binaries

```bash
git clone git@github.com:anex-sh/anex.git
cd anex
make build
```

This creates four binaries in `bin/`:

| Binary | What it does |
|---|---|
| `virtual_kubelet` | The main Anex provider — presents remote GPU machines as a virtual Kubernetes node |
| `gateway_init` | Initializes the WireGuard gateway that connects your cluster to remote machines |
| `gateway_controller` | Manages HAProxy routing so traffic reaches the right remote pod |
| `container_agent` | Runs inside the remote machine to manage the container lifecycle |

You can also build individual components if needed:

```bash
make build-virtual-kubelet    # Just the provider
make build-gateway-init       # Just the gateway init
make build-gateway-controller # Just the gateway controller
make build-container-agent    # Just the container agent
```

### Build and Push Docker Images

You need to host two Docker images in a registry your cluster can pull from:

```bash
# Choose your registry and version tag
export REGISTRY="your-registry.io/your-username"
export VERSION="v1.0.0"

# 1. Build and push the main Anex image
make build-virtual-kubelet
docker build -f deploy/Dockerfile -t ${REGISTRY}/anex:${VERSION} .
docker push ${REGISTRY}/anex:${VERSION}

# 2. Build and push the gateway image
make build-gateway-init build-gateway-controller
docker build -f deploy/gateway.Dockerfile -t ${REGISTRY}/anex-gateway:${VERSION} .
docker push ${REGISTRY}/anex-gateway:${VERSION}
```

---

## Installation

Anex is installed via a Helm chart. The configuration differs slightly depending on whether you're running on a cloud-managed cluster (like AWS EKS) or locally (like miniKube).

### Installing on AWS EKS

On AWS, Anex uses a LoadBalancer to expose the gateway. Create a file called `values.quickstart.yaml`:

```yaml
deployment:
  gateway:
    class: "aws-eks"
    image:
      repository: "your-registry.io/your-username/anex-gateway"
      tag: "v1.0.0"
      pullPolicy: Always

  containers:
    virtualKubelet:
      image:
        repository: "your-registry.io/your-username/anex"
        tag: "v1.0.0"
        pullPolicy: Always

appConfig:
  cluster:
    # A unique identifier for this cluster in your VastAI account
    clusterUUID: "your-cluster-uuid"

  cloudProvider:
    active: "vastAI"
    vastAI:
      apiKey: "your-vastai-api-key"   # Get this from https://cloud.vast.ai/account/

  virtualNode:
    nodeName: "anex-virtual-node"     # Must be unique in your cluster
    labels:
      node-provider: vastai           # Used by pods to target this node
```

Then deploy:

```bash
helm upgrade --install anex ./deploy/chart \
  --namespace anex \
  --create-namespace \
  -f values.quickstart.yaml
```

### Installing on miniKube

For testing with minimal impact on existing infrastructure, we recommend using Minikube. Note that GPU Provider needs to expose a public endpoint for remote machines to connect to, therefore running it locally is usually not feasible. It's fine to run Minikube on EC2 or other cloud instances.

To expose the gateway using a NodePort and make it publicly accessible, start Minikube with port mapping:

```bash
minikube start --driver=docker --listen-address=0.0.0.0 --ports=31000:31000/udp
```

If using AWS EC2, don't forget to allow UDP port 31000 in the Security Groups.

Create a file called `values.quickstart.yaml`:

```yaml
deployment:
  gateway:
    class: "node-port"
    domain: "your-minikube-ip"        # Run `minikube ip` to get this
    nodePort: 31000
    image:
      repository: "your-registry.io/your-username/anex-gateway"
      tag: "v1.0.0"
      pullPolicy: Always

  containers:
    virtualKubelet:
      image:
        repository: "your-registry.io/your-username/anex"
        tag: "v1.0.0"
        pullPolicy: Always

appConfig:
  cluster:
    clusterUUID: "your-cluster-uuid"

  cloudProvider:
    vastAI:
      apiKey: "your-vastai-api-key"

  virtualNode:
    nodeName: "virtual-node-name"
    labels:
      node-provider: vastai
```

Then deploy:

```bash
helm upgrade --install anex ./deploy/chart \
  --namespace anex \
  --create-namespace \
  -f values.quickstart.yaml
```

---

## How Pods Work with Anex

When you deploy a pod through Anex, you use standard Kubernetes YAML with a few extra pieces:

### Annotations

Annotations on the pod tell Anex what kind of GPU machine to rent:

```yaml
annotations:
  gpu-provider.glami.cz/region: "europe"              # Preferred region
  gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"  # Acceptable GPU types (comma-separated)
  gpu-provider.glami.cz/price-max: "0.5"             # Maximum price per hour in USD
  gpu-provider.glami.cz/verified-only: "true"        # Only use verified/trusted hosts
  gpu-provider.glami.cz/disk-space-gb: "100"         # Disk space to request (in GB)
  virtual: "true"                       # Required — marks this pod as a virtual pod
```

### Node Selector and Tolerations

Every pod that should run on a remote GPU machine needs a `nodeSelector` and `tolerations` block. This tells Kubernetes to schedule the pod on the Anex virtual node instead of a regular cluster node:

```yaml
nodeSelector:
  node-provider: vastai               # Must match the label in your values.yaml
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

### VirtualService (Networking)

Standard Kubernetes Services cannot route traffic to virtual pods because they run on remote machines outside the cluster network. Anex provides a custom resource called **VirtualService** that handles this. It creates a Service with the same name and port, but routes traffic through the Anex gateway to reach the remote pod.

```yaml
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: my-service
spec:
  gateway:
    selector:
      custom-gateway: "true"
  service:
    selector:
      app: my-app          # Must match the pod's labels
    ports:
      - name: http
        port: 80
        targetPort: 8080    # The port your container listens on
```

Once deployed, you can access the service using `kubectl port-forward` or by placing an Ingress in front of it, just like a regular Kubernetes Service.

**Current limitations:**
- Pods must be annotated with `virtual: "true"` to be valid VirtualService targets
- The VirtualService must include the `gateway.selector` block as shown above

---

## Deployment Examples

### Example 1: llama.cpp on miniKube (Local Access)

This example runs [llama.cpp](https://github.com/ggml-org/llama.cpp) server on a rented GPU and exposes it locally via port-forwarding. It's the simplest way to get started — no Ingress, no auth, just a local tunnel to a remote GPU.

**What you get:** A remote GPU running llama.cpp with an OpenAI-compatible API, accessible from your laptop at `localhost:8080`.

**llama-cpp.yaml:**
```yaml
# VirtualService: makes the remote pod reachable as a Kubernetes service
apiVersion: anex.sh/v1alpha1
kind: VirtualService
metadata:
  name: llama
spec:
  gateway:
    selector:
      custom-gateway: "true"
  service:
    selector:
      app: llama
    ports:
      - name: http
        port: 80
        targetPort: 8090
---
# The llama.cpp server pod — runs on a rented GPU
apiVersion: v1
kind: Pod
metadata:
  name: llama
  labels:
    app: llama
  annotations:
    gpu-provider.glami.cz/region: "europe"
    gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"
    gpu-provider.glami.cz/price-max: "0.5"
    gpu-provider.glami.cz/verified-only: "true"
    gpu-provider.glami.cz/disk-space-gb: "100"
    virtual: "true"
spec:
  containers:
    - name: llama
      image: ghcr.io/ggml-org/llama.cpp:server-cuda
      command:
        - /bin/sh
        - -c
        - |
          apt-get update && apt-get install -y wget &&
          wget -O /tmp/model.gguf \
            "https://huggingface.co/hugging-quants/Llama-3.2-1B-Instruct-Q4_K_M-GGUF/resolve/main/llama-3.2-1b-instruct-q4_k_m.gguf" &&
          /app/llama-server \
            -m /tmp/model.gguf \
            --port 8090 \
            --host 0.0.0.0 \
            -ngl 99
      ports:
        - containerPort: 8090
      resources:
        limits:
          nvidia.com/gpu: "1"
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

**Step 1: Deploy it**

```bash
kubectl apply -f llama-cpp.yaml
```

Anex will find a matching GPU machine, rent it, download the model, and start the llama.cpp server. This can take a few minutes.

**Step 2: Forward the port to your machine**

```bash
kubectl port-forward service/llama 8080:80
```

This creates a tunnel from `localhost:8080` on your computer to the remote llama.cpp instance.

**Step 3: Use it**

In another terminal:

```bash
# Health check — make sure the server is ready
curl http://localhost:8080/health

# List available models
curl http://localhost:8080/v1/models

# Text completion (OpenAI-compatible)
curl -X POST http://localhost:8080/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Once upon a time",
    "max_tokens": 100,
    "temperature": 0.7
  }'

# Chat completion (OpenAI-compatible)
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "user", "content": "Why is the sky blue?"}
    ],
    "max_tokens": 100
  }'
```

---

### Example 2: llama.cpp on AWS with Ingress and Auth

This example runs [llama.cpp](https://github.com/ggml-org/llama.cpp) server on a rented GPU, exposed to the internet through an nginx Ingress with TLS and bearer token authentication. This is closer to a production setup.

**What you get:** A public HTTPS endpoint (e.g., `https://llama.cpp.your-domain.com`) that serves an OpenAI-compatible API, protected by a bearer token.

The setup consists of four Kubernetes resources:
1. **VirtualService** — routes traffic to the remote llama.cpp pod
2. **Ingress** — exposes the service to the internet with TLS
3. **llama Pod** — the actual llama.cpp server running on a rented GPU
4. **auth-proxy Pod + Service** — a lightweight Python proxy that validates bearer tokens before forwarding requests

**llama-ingress.yaml:**
```yaml
# 1. VirtualService: makes the remote llama pod reachable inside the cluster
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: llama
  namespace: anex
spec:
  gateway:
    selector:
      custom-gateway: "true"
  service:
    selector:
      app: llama
    ports:
      - name: http
        port: 80
        targetPort: 8090
---
# 2. Ingress: exposes the service to the internet with TLS
#    Traffic flows: Internet → Ingress → auth-proxy → VirtualService → remote GPU pod
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: llama
  namespace: anex
  annotations:
    kubernetes.io/tls-acme: "true"
    nginx.ingress.kubernetes.io/proxy-buffering: "on"
    nginx.ingress.kubernetes.io/proxy-buffers-number: "64"
    nginx.ingress.kubernetes.io/proxy-buffer-size: "32k"
    nginx.ingress.kubernetes.io/client-body-buffer-size: "1M"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "300"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "300"
spec:
  ingressClassName: "nginx"
  tls:
    - hosts:
        - llama.cpp.your-domain.com
      secretName: llama-ingress-tls
  rules:
    - host: llama.cpp.your-domain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: llama-auth-proxy
                port:
                  number: 80
---
# 3. The llama.cpp server pod — runs on a rented GPU
apiVersion: v1
kind: Pod
metadata:
  name: llama
  namespace: anex
  labels:
    app: llama
  annotations:
    gpu-provider.glami.cz/region: "europe"
    gpu-provider.glami.cz/gpu-names: "RTX 4090,RTX 3090"
    gpu-provider.glami.cz/price-max: "0.5"
    gpu-provider.glami.cz/verified-only: "true"
    gpu-provider.glami.cz/disk-space-gb: "100"
    virtual: "true"
spec:
  containers:
    - name: llama
      image: ghcr.io/ggml-org/llama.cpp:server-cuda
      command:
        - /bin/sh
        - -c
        - |
          apt-get update && apt-get install -y wget &&
          wget -O /tmp/model.gguf \
            "https://huggingface.co/hugging-quants/Llama-3.2-1B-Instruct-Q4_K_M-GGUF/resolve/main/llama-3.2-1b-instruct-q4_k_m.gguf" &&
          /app/llama-server \
            -m /tmp/model.gguf \
            --port 8090 \
            --host 0.0.0.0 \
            -ngl 99
      ports:
        - containerPort: 8090
      resources:
        limits:
          nvidia.com/gpu: "1"
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
# 4. Auth proxy — validates bearer tokens before forwarding to llama
apiVersion: v1
kind: Pod
metadata:
  name: llama-auth-proxy
  namespace: anex
  labels:
    app: llama-auth-proxy
spec:
  containers:
    - name: auth-proxy
      image: python:3.11-slim
      command:
        - /bin/sh
        - -c
        - |
          mkdir -p /app &&
          pip install flask requests &&
          cat > /app/auth_proxy.py << 'PYTHON_SCRIPT'
          import os
          import re
          import logging
          from functools import wraps
          from flask import Flask, request, Response, jsonify
          import requests
          
          logging.basicConfig(level=logging.INFO)
          logger = logging.getLogger(__name__)
          app = Flask(__name__)
          
          BACKEND_URL = os.environ.get('BACKEND_URL', 'http://llama:80')
          EXPECTED_TOKEN = os.environ.get('BEARER_TOKEN', '')
          PORT = int(os.environ.get('PORT', '8080'))
          
          def require_bearer_token(f):
              @wraps(f)
              def decorated_function(*args, **kwargs):
                  auth_header = request.headers.get('Authorization', '')
                  if not auth_header:
                      return jsonify({'error': 'Unauthorized', 'message': 'Missing Authorization header'}), 401
                  match = re.match(r'^Bearer\s+(.+)$', auth_header, re.IGNORECASE)
                  if not match:
                      return jsonify({'error': 'Unauthorized', 'message': 'Invalid Authorization format'}), 401
                  token = match.group(1)
                  if token != EXPECTED_TOKEN:
                      return jsonify({'error': 'Unauthorized', 'message': 'Invalid bearer token'}), 401
                  return f(*args, **kwargs)
              return decorated_function
          
          @app.route('/', defaults={'path': ''}, methods=['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS', 'HEAD'])
          @app.route('/<path:path>', methods=['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS', 'HEAD'])
          @require_bearer_token
          def proxy(path):
              backend_url = f"{BACKEND_URL}/{path}"
              if request.query_string:
                  backend_url += f"?{request.query_string.decode('utf-8')}"
              try:
                  headers = {key: value for key, value in request.headers if key.lower() != 'authorization'}
                  resp = requests.request(
                      method=request.method,
                      url=backend_url,
                      headers=headers,
                      data=request.get_data(),
                      params=request.args,
                      timeout=300,
                      stream=True
                  )
                  response_headers = [(name, value) for name, value in resp.headers.items()]
                  hop_by_hop = ['connection', 'keep-alive', 'proxy-authenticate', 
                               'proxy-authorization', 'te', 'trailers', 
                               'transfer-encoding', 'upgrade']
                  response_headers = [(n, v) for n, v in response_headers if n.lower() not in hop_by_hop]
                  return Response(resp.iter_content(chunk_size=4096), 
                                status=resp.status_code, headers=response_headers)
              except Exception as e:
                  return jsonify({'error': 'Service Unavailable', 'message': str(e)}), 503
          
          if __name__ == '__main__':
              logger.info(f"Auth proxy starting on port {PORT}")
              logger.info(f"Backend: {BACKEND_URL}")
              app.run(host='0.0.0.0', port=PORT, threaded=True)
          PYTHON_SCRIPT
          python /app/auth_proxy.py
      ports:
        - containerPort: 8080
      env:
        - name: BEARER_TOKEN
          value: "your-secret-bearer-token-here"  # Change this!
        - name: BACKEND_URL
          value: "http://llama:80"
        - name: PORT
          value: "8080"
      resources:
        limits:
          memory: "256Mi"
          cpu: "500m"
---
# Service for the auth proxy (the Ingress routes traffic here)
apiVersion: v1
kind: Service
metadata:
  name: llama-auth-proxy
  namespace: anex
spec:
  selector:
    app: llama-auth-proxy
  ports:
    - name: http
      port: 80
      targetPort: 8080
  type: ClusterIP
```

**Step 1: Deploy it**

```bash
kubectl apply -f llama-ingress.yaml
```

**Step 2: Wait for the model to download**

The pod needs to download the model on first start, which can take a few minutes:

```bash
kubectl wait --for=condition=Ready pod/llama -n anex --timeout=300s
```

**Step 3: Query the API**

Replace the token and hostname with your values:

```bash
export TOKEN="your-secret-bearer-token-here"
export HOST="llama.cpp.your-domain.com"

# Health check
curl -H "Authorization: Bearer $TOKEN" https://$HOST/health

# List available models
curl -H "Authorization: Bearer $TOKEN" https://$HOST/v1/models

# Text completion (OpenAI-compatible)
curl -X POST https://$HOST/v1/completions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Once upon a time",
    "max_tokens": 100,
    "temperature": 0.7
  }'

# Chat completion (OpenAI-compatible)
curl -X POST https://$HOST/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "user", "content": "Why is the sky blue?"}
    ],
    "max_tokens": 100
  }'
```

