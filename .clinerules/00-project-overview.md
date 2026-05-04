GPU Provider — Project Overview

What this repo is / does
- Exposes containers running on an external GPU cloud provider (VastAI or Mock) as Kubernetes Pods scheduled to a virtual node implemented on Virtual Kubelet.
  - Entrypoint: cmd/virtual-kubelet/main.go
  - Provider implementation: internal/provider/anex/provider.go
- Provides a Gateway controller that watches a VirtualService CRD and configures HAProxy L4 load balancing to forward traffic to virtual pods over a Wireguard subnet.
  - Entrypoint: cmd/gateway-controller/main.go
  - Controller: internal/gateway/controller.go, internal/gateway/reconcile.go
  - HAProxy manager: internal/gateway/haproxy/manager.go
- Ships a lightweight Container Agent binary that runs in the remote container environment, exposes status over HTTP, and can bootstrap wireproxy/promtail as needed.
  - Entrypoint: cmd/container-agent/main.go
  - Agent: internal/agent/agent.go, internal/agent/server.go
- Defines a VirtualService CRD (anex.sh/v1alpha1) as the single source of truth for Service-like L4 targeting of virtual pods and creates a corresponding ClusterIP Service pointing at the Gateway.
  - Types: api/v1alpha1/virtualservice_types.go
  - CRD manifest: deploy/chart/crds/anex.sh_virtualservices.yaml

Non-goals and constraints
- Single-container Pods only on the virtual node (enforced in internal/provider/anex/provider.go: CreatePod rejects multiple containers).
- Gateway is singleton (controller requires labels of the running gateway pod; see cmd/gateway-controller/main.go and controller constructor).
- Networking: Wireguard subnet + wireproxy client in remote containers; not normal CNI.
  - Pod Wireguard IP derived from proxy slot: 10.254.254.(11 + slot) in internal/gateway/controller.go (WireguardSubnetBase/Offset).
- VirtualService feature set intentionally constrained:
  - TCP only; protocol must be TCP or empty (internal/gateway/reconcile.go: validateVirtualService).
  - targetPort must be an int; no named ports; no sessionAffinity (not supported/validated in api/v1alpha1 and reconcile.go).
- Port allocation for VirtualService is from a dedicated range (default 6000–9999) and persisted in status (internal/gateway/controller.go constants; ensurePortAllocations in reconcile.go).
- Idempotent reconciliation with finalizers, owner refs, and status conditions; do not mutate user Services (generated Service is owned by the VirtualService; internal/gateway/reconcile.go).

Start here pointers (auditable code)
- Virtual Kubelet entrypoint and provider registration:
  - cmd/virtual-kubelet/main.go
  - internal/provider/anex/provider.go
- Gateway controller and reconcile logic:
  - cmd/gateway-controller/main.go
  - internal/gateway/controller.go
  - internal/gateway/reconcile.go
- Data plane and allocations:
  - internal/gateway/haproxy/manager.go
  - internal/gateway/portalloc/allocator.go
- CRD and API types:
  - api/v1alpha1/virtualservice_types.go
  - deploy/chart/crds/anex.sh_virtualservices.yaml
- Agent runtime:
  - cmd/container-agent/main.go
  - internal/agent/agent.go
  - internal/agent/server.go
- Virtual pod abstraction used by the provider:
  - virtualpod/virtualpod.go

Current state notes
- CRD group/version: anex.sh/v1alpha1 (api/v1alpha1/groupversion_info.go).
- VirtualService status carries allocatedPorts and standard conditions; controller updates .status via dynamic client (internal/gateway/controller.go:updateVirtualServiceStatus, internal/gateway/reconcile.go:setConditionAndUpdate).
- Generated Service:
  - Name/namespace = VirtualService name/namespace; type ClusterIP; selector = gateway pod labels; targetPort = allocated gatewayPort (internal/gateway/reconcile.go: ensureGeneratedService).
- HAProxy is configured via the Data Plane API (HTTP or Unix socket) with transactions and force_reload (internal/gateway/haproxy/manager.go).
- Wireguard/wireproxy:
  - Gateway assigns proxy slot ids; provider annotates pods with anex.sh/proxy-slot-id (internal/provider/anex/provider.go).
  - Agent can render wireproxy config and run it; status exposed on /status (internal/agent/*).
- Examples for VirtualService live under examples/ (e.g., examples/virtualservice-basic.yaml). If behavior differs, the above code paths are the source of truth.
