# VirtualService Implementation Summary

This document summarizes the implementation of the VirtualService CRD and Gateway controller for the GPU Provider project.

## Overview

The VirtualService feature introduces a Kubernetes-native way to expose virtual pods (running in external GPU providers like VastAI) through standard Kubernetes Services. The implementation follows Kubernetes controller patterns and provides Layer-4 load balancing via HAProxy running in the Gateway pod.

## What Was Implemented

### 1. API Types (CRD)

**Files Created:**
- `api/v1alpha1/groupversion_info.go` - API group/version registration
- `api/v1alpha1/virtualservice_types.go` - VirtualService CRD type definitions
- `api/v1alpha1/zz_generated.deepcopy.go` - Generated deep copy methods
- `deploy/chart/crds/gpu-provider.glami-ml.com_virtualservices.yaml` - Generated CRD manifest

**Key Features:**
- API group: `gpu-provider.glami-ml.com/v1alpha1`
- Declarative spec for service definition
- Gateway selector for identifying the gateway pod
- Service selector for matching virtual pods
- Port definitions (TCP only, integer targetPorts)
- Status with allocated ports and conditions
- Kubebuilder validation markers

### 2. Gateway Controller

**Files Created:**
- `internal/gateway/controller.go` - Main controller structure and event handling
- `internal/gateway/reconcile.go` - Reconciliation logic
- `internal/gateway/portalloc/allocator.go` - Port allocation management
- `internal/gateway/haproxy/manager.go` - HAProxy configuration management

**Key Features:**
- Watches VirtualService and Pod resources
- Maintains in-memory cache of virtual pods and their Wireguard IPs
- Declarative reconciliation loop with idempotent operations
- Finalizer-based cleanup
- Status condition management
- Restart-safe design

### 3. Port Allocation

**Component:** `internal/gateway/portalloc/`

**Features:**
- Allocates gateway ports from range 6000-9999
- Thread-safe allocation and release
- Stable allocations (persisted in VirtualService status)
- Per-owner tracking for cleanup

### 4. HAProxy Integration

**Component:** `internal/gateway/haproxy/`

**Features:**
- Runtime API integration for dynamic configuration
- Frontend/backend management
- Backend server updates (add/remove/update)
- Graceful cleanup on VirtualService deletion

**Note:** The current implementation uses HAProxy's runtime API which has limitations for creating new frontends/backends dynamically. A production implementation should consider:
- Generating HAProxy config files and reloading
- Using HAProxy Enterprise features
- Pre-configuring frontend/backend templates

### 5. Generated Kubernetes Services

**Features:**
- Creates ClusterIP Service for each VirtualService
- Service selector matches gateway pod
- Port mappings: user port → gateway port
- Owner references for garbage collection
- Conflict detection (prevents overwriting existing Services)

### 6. Documentation

**Files Created:**
- `docs/content/en/docs/virtualservice/_index.md` - Comprehensive user documentation
- `examples/virtualservice-basic.yaml` - Example manifest

## Integration Requirements

To fully integrate this implementation into the GPU Provider, the following steps are needed:

### 1. Update go.mod Dependencies

The implementation added:
```
sigs.k8s.io/controller-runtime v0.19.4
```

Run to ensure all dependencies are resolved:
```bash
go mod tidy
```

### 2. Create Gateway Controller Entrypoint

Create `cmd/gateway-controller/main.go` that:
- Sets up Kubernetes client and informers
- Creates VirtualService informer using controller-runtime
- Initializes the Gateway controller
- Starts the controller with workers
- Handles graceful shutdown

Example structure:
```go
package main

import (
    "context"
    "flag"
    "os"
    
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/klog/v2"
    ctrl "sigs.k8s.io/controller-runtime"
    
    gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
    "gitlab.devklarka.cz/ai/gpu-provider/internal/gateway"
)

func main() {
    // Setup flags, config, clients
    // Create informers
    // Initialize controller
    // Run controller
}
```

### 3. Update Gateway Deployment

**File:** `deploy/chart/templates/deployment.yaml`

Add the gateway-controller container to the gateway pod:

```yaml
- name: gateway-controller
  image: {{ .Values.deployment.containers.gateway.image.repository }}:{{ .Values.deployment.containers.gateway.image.tag }}
  imagePullPolicy: {{ .Values.deployment.containers.gateway.image.pullPolicy }}
  command: ["/gateway-controller"]
  args:
    - --gateway-pod-name=$(POD_NAME)
    - --gateway-namespace=$(POD_NAMESPACE)
    - --haproxy-socket=/var/run/haproxy/haproxy.sock
  env:
    - name: POD_NAME
      valueFrom:
        fieldRef:
          fieldPath: metadata.name
    - name: POD_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
  volumeMounts:
    - name: haproxy-socket
      mountPath: /var/run/haproxy
```

### 4. Install CRD

The CRD must be installed before deploying the controller:

```bash
kubectl apply -f deploy/chart/crds/gpu-provider.glami-ml.com_virtualservices.yaml
```

Or include in Helm chart's CRD directory for automatic installation.

### 5. RBAC Permissions

Update `deploy/chart/templates/clusterrole.yaml` to add permissions:

```yaml
- apiGroups: ["gpu-provider.glami-ml.com"]
  resources: ["virtualservices"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: ["gpu-provider.glami-ml.com"]
  resources: ["virtualservices/status"]
  verbs: ["update", "patch"]
- apiGroups: [""]
  resources: ["services"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]
```

### 6. HAProxy Configuration

Configure HAProxy in the gateway container to:
- Enable the runtime API socket (e.g., `/var/run/haproxy/haproxy.sock`)
- Pre-configure frontends/backends or use a template-based approach
- Listen on allocated gateway ports (6000-9999)

Example haproxy.cfg additions:
```
global
    stats socket /var/run/haproxy/haproxy.sock mode 660 level admin

# Template frontend/backend that can be dynamically configured
frontend dynamic-frontend-template
    bind *:6000-9999
    mode tcp
    default_backend dynamic-backend-template

backend dynamic-backend-template
    mode tcp
    balance roundrobin
```

### 7. Virtual Pod Annotations

Ensure virtual pods are created with the required annotation:
- `virtual: "true"` - Identifies pods as virtual
- `gpu-provider.glami.cz/proxy-slot-id: "<slot-id>"` - Wireguard slot assignment

This is already done in `internal/provider/glami/provider.go`.

## Testing the Implementation

### 1. Build and Deploy

```bash
# Build container images
make docker-build

# Deploy to cluster
helm upgrade --install gpu-provider ./deploy/chart -f values.yaml
```

### 2. Create a Test VirtualService

```bash
kubectl apply -f examples/virtualservice-basic.yaml
```

### 3. Verify Status

```bash
# Check VirtualService status
kubectl get virtualservice
kubectl describe virtualservice my-virtual-service

# Check generated Service
kubectl get service my-virtual-service
kubectl describe service my-virtual-service

# Check controller logs
kubectl logs -l app.kubernetes.io/name=gpu-provider-gateway -c gateway-controller
```

### 4. Test Connectivity

```bash
# From a pod in the cluster
curl http://my-virtual-service

# Check HAProxy stats
kubectl exec -it <gateway-pod> -- socat - /var/run/haproxy/haproxy.sock <<< "show stat"
```

## Known Limitations and Future Work

### Current Limitations

1. **HAProxy Dynamic Configuration**: The current implementation uses HAProxy's runtime API which has limitations. Consider implementing config file generation with reload instead.

2. **No Health Checks**: All pods are assumed ready. Future versions should support health checking.

3. **TCP Only**: Only TCP protocol is supported. UDP/SCTP support requires additional work.

4. **No Client Updates**: The controller needs to use proper typed clients for VirtualService updates. Current implementation has placeholder methods.

5. **Port Range**: Fixed 6000-9999 range. Consider making this configurable.

### Future Enhancements

- [ ] Implement typed client for VirtualService updates
- [ ] Add health checking support
- [ ] Support UDP protocol
- [ ] Add metrics/monitoring (Prometheus metrics)
- [ ] Implement connection limits and rate limiting
- [ ] Add TLS termination option
- [ ] Support session affinity
- [ ] Make port range configurable
- [ ] Add admission webhook for validation
- [ ] Implement HAProxy config file generation approach
- [ ] Add E2E tests

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                      Kubernetes Cluster                       │
│                                                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              Gateway Pod                              │   │
│  │                                                        │   │
│  │  ┌──────────────────┐  ┌─────────────────────────┐  │   │
│  │  │ Gateway          │  │ Gateway Controller      │  │   │
│  │  │ Controller       │  │                         │  │   │
│  │  │                  │  │ - Watches VirtualSvc    │  │   │
│  │  │ - HAProxy        │◄─┤ - Watches Pods          │  │   │
│  │  │ - Wireguard      │  │ - Allocates Ports       │  │   │
│  │  │ - Port Forward   │  │ - Manages Services      │  │   │
│  │  └──────────────────┘  └─────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────┘   │
│               ▲                          ▲                   │
│               │                          │                   │
│  ┌────────────┴───────┐     ┌───────────┴──────────┐       │
│  │  Service (ClusterIP)│     │  VirtualService CRD  │       │
│  │  - Selects Gateway  │     │  - Spec: ports, sel  │       │
│  │  - Port mapping     │     │  - Status: allocated │       │
│  └─────────────────────┘     └──────────────────────┘       │
│                                                               │
└───────────────────────────────┬───────────────────────────────┘
                                │ Wireguard VPN
                                ▼
                ┌───────────────────────────────┐
                │      Virtual Pods (VastAI)    │
                │  - Labels match selector      │
                │  - Annotation: virtual=true   │
                │  - Wireguard connected        │
                └───────────────────────────────┘
```

## Files Summary

### New Files Created
```
api/v1alpha1/
├── groupversion_info.go           # API registration
├── virtualservice_types.go        # CRD types
└── zz_generated.deepcopy.go       # Generated

internal/gateway/
├── controller.go                  # Main controller
├── reconcile.go                   # Reconciliation logic
├── haproxy/
│   └── manager.go                 # HAProxy integration
└── portalloc/
    └── allocator.go               # Port allocation

deploy/chart/crds/
└── gpu-provider.glami-ml.com_virtualservices.yaml  # CRD manifest

docs/content/en/docs/virtualservice/
└── _index.md                      # User documentation

examples/
└── virtualservice-basic.yaml      # Example manifest

hack/
└── boilerplate.go.txt            # License header
```

### Files to Create/Modify

**Need to Create:**
- `cmd/gateway-controller/main.go` - Controller entrypoint

**Need to Modify:**
- `deploy/chart/templates/deployment.yaml` - Add controller container
- `deploy/chart/templates/clusterrole.yaml` - Add RBAC permissions
- `deploy/gateway.sh` - Configure HAProxy with runtime socket
- `Makefile` - Add build targets for gateway-controller

## Conclusion

This implementation provides a solid foundation for the VirtualService feature. The controller follows Kubernetes best practices with:
- Declarative API design
- Idempotent reconciliation
- Proper finalizer handling
- Status conditions
- Restart safety

The main integration work remaining is creating the controller entrypoint, updating deployment manifests, and enhancing the HAProxy configuration approach for production use.
