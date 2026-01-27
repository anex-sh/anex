---
title: "VirtualService"
weight: 5
description: >
  VirtualService CRD for exposing virtual pods via Layer-4 load balancing
---

## Overview

The **VirtualService** Custom Resource Definition (CRD) provides a Kubernetes-native way to expose virtual pods (pods running on the GPU Provider virtual node) through a classic Kubernetes Service with Layer-4 load balancing.

### Why VirtualService?

Virtual pods run in external cloud providers (e.g., VastAI) and connect to the cluster via a Wireguard network through the Gateway. Direct Kubernetes Services cannot naturally select these pods because they use a custom networking model. The VirtualService CRD solves this by:

1. Acting as a declarative interface for exposing virtual pods
2. Automatically managing port allocation on the Gateway
3. Configuring HAProxy for Layer-4 load balancing
4. Creating a standard Kubernetes Service that routes to the Gateway

## Architecture

```
┌─────────────┐      ┌──────────────┐      ┌─────────────┐
│   Client    │─────▶│   Service    │─────▶│   Gateway   │
│             │      │  (ClusterIP) │      │  (HAProxy)  │
└─────────────┘      └──────────────┘      └─────────────┘
                                                   │
                                                   │ Wireguard
                                                   ▼
                                     ┌──────────────────────────┐
                                     │   Virtual Pods           │
                                     │ (VastAI containers)      │
                                     └──────────────────────────┘
```

## API Specification

### VirtualService

```yaml
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: my-service
  namespace: default
spec:
  gateway:
    selector:
      app.kubernetes.io/name: gpu-provider-gateway
  service:
    selector:
      app: my-app
      virtual: "true"
    ports:
      - name: http
        port: 80
        targetPort: 8080
        protocol: TCP
```

### Spec Fields

#### `spec.gateway`

Identifies which gateway pod(s) should handle this VirtualService.

- **`selector`** (required): Label selector for gateway pods. In practice, should select exactly one gateway pod.

#### `spec.service`

Defines the Service-like specification.

- **`selector`** (required): Label selector for virtual pods to include in the backend pool.
- **`ports`** (required): List of ports to expose.

#### `spec.service.ports[]`

- **`name`** (optional): Name for the port (recommended for clarity).
- **`port`** (required): Service port number (1-65535).
- **`targetPort`** (required): Target port on virtual pods (1-65535, must be an integer).
- **`protocol`** (optional): Must be `TCP` or empty (defaults to TCP).

### Status Fields

The controller automatically populates the status with operational information:

#### `status.allocatedPorts[]`

Maps each service port to its allocated gateway port:

```yaml
status:
  allocatedPorts:
    - name: http
      servicePort: 80
      targetPort: 8080
      gatewayPort: 6001
      protocol: TCP
```

#### `status.conditions[]`

Standard Kubernetes conditions array:

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Reconciled
      message: "Service and proxy configured with 2 backends"
      observedGeneration: 1
      lastTransitionTime: "2026-01-27T20:00:00Z"
```

**Condition Types:**
- `Ready`: Indicates whether the VirtualService is fully configured

**Condition Reasons:**
- `Reconciled`: Successfully configured
- `ReconcileError`: Transient error during reconciliation
- `UnsupportedSpec`: Invalid or unsupported specification
- `ServiceConflict`: Service name conflict
- `GatewayNotFound`: Gateway pod not found
- `PortAllocationError`: Failed to allocate gateway ports

## Usage Examples

### Basic HTTP Service

```yaml
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: web-service
  namespace: production
spec:
  gateway:
    selector:
      app.kubernetes.io/name: gpu-provider-gateway
  service:
    selector:
      app: web-app
      virtual: "true"
    ports:
      - name: http
        port: 80
        targetPort: 8080
        protocol: TCP
```

### Multi-Port Service

```yaml
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: api-service
  namespace: default
spec:
  gateway:
    selector:
      app.kubernetes.io/name: gpu-provider-gateway
  service:
    selector:
      app: api-server
      virtual: "true"
    ports:
      - name: http
        port: 8080
        targetPort: 8080
        protocol: TCP
      - name: grpc
        port: 9090
        targetPort: 9090
        protocol: TCP
      - name: metrics
        port: 9091
        targetPort: 9091
        protocol: TCP
```

## Constraints and Limitations

### Supported Features

- ✅ TCP protocol only
- ✅ Integer target ports only
- ✅ Multiple ports per service
- ✅ Label-based pod selection
- ✅ Automatic port allocation (6000-9999 range)
- ✅ Stable port assignments across reconciliations

### Unsupported Features

- ❌ UDP or SCTP protocols
- ❌ Named ports (e.g., `targetPort: "http"`)
- ❌ Session affinity
- ❌ Service types other than ClusterIP
- ❌ External traffic policies
- ❌ Health checks (assumes all pods are ready)

Attempting to use unsupported features will result in the `Ready` condition being set to `False` with reason `UnsupportedSpec`.

## Behavior

### Port Allocation

- VirtualServices are assigned gateway ports from the range **6000-9999**
- Port allocations are **stable**: once allocated, ports are reused across updates
- Ports are freed when the VirtualService is deleted

### Service Generation

For each VirtualService, the controller creates a standard Kubernetes Service with:
- Same name and namespace as the VirtualService
- Type: ClusterIP
- Selector: Matches the gateway pod
- Owner reference: Set to the VirtualService for garbage collection

### Load Balancing

- HAProxy running in the Gateway pod performs Layer-4 load balancing
- Round-robin distribution across matching virtual pods
- Automatic backend updates when pods are added/removed/relabeled

### Lifecycle

1. **Create**: VirtualService is created → ports allocated → Service created → HAProxy configured
2. **Update**: Spec changes → ports reallocated if needed → Service updated → HAProxy reconfigured
3. **Delete**: Finalizer ensures cleanup → HAProxy config removed → ports released → Service deleted

### Restart Safety

The controller is designed to be restart-safe:
- Port allocations are persisted in `status.allocatedPorts`
- On restart, the controller rebuilds state from existing VirtualServices and Pods
- HAProxy configuration is reconstructed from cluster state

## Troubleshooting

### Check VirtualService Status

```bash
kubectl get virtualservice -n <namespace>
kubectl describe virtualservice <name> -n <namespace>
```

### Common Issues

**Ready condition is False with reason `UnsupportedSpec`:**
- Check that all ports use TCP protocol
- Verify targetPort is an integer
- Remove any unsupported fields

**Ready condition is False with reason `ServiceConflict`:**
- A Service with the same name already exists and is not owned by this VirtualService
- Rename the VirtualService or remove the conflicting Service

**Ready condition is False with reason `PortAllocationError`:**
- Gateway port range (6000-9999) may be exhausted
- Check how many VirtualServices exist and how many ports each uses

**No matching pods:**
- Verify virtual pods have labels matching the VirtualService selector
- Check that pods have annotation `virtual: "true"`
- Ensure pods are scheduled to the virtual node

### View Gateway Port Allocations

```bash
kubectl get virtualservice -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatedPorts[*].gatewayPort}{"\n"}{end}'
```

## Advanced Configuration

### Using with Ingress

VirtualServices create standard ClusterIP Services, so they work with Ingress controllers:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-ingress
spec:
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-virtual-service  # References the VirtualService
                port:
                  number: 80
```

### Multiple VirtualServices per Application

You can create multiple VirtualServices selecting the same pods with different port mappings:

```yaml
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: app-public
spec:
  gateway:
    selector:
      app.kubernetes.io/name: gpu-provider-gateway
  service:
    selector:
      app: myapp
    ports:
      - name: http
        port: 80
        targetPort: 8080
---
apiVersion: gpu-provider.glami-ml.com/v1alpha1
kind: VirtualService
metadata:
  name: app-admin
spec:
  gateway:
    selector:
      app.kubernetes.io/name: gpu-provider-gateway
  service:
    selector:
      app: myapp
    ports:
      - name: admin
        port: 8081
        targetPort: 8081
```

## Future Enhancements

Potential future improvements (not currently supported):

- UDP protocol support
- Named ports
- Health checking configuration
- Session affinity
- Custom load balancing algorithms
- Connection limits and rate limiting
- TLS termination at the gateway
