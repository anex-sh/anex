GPU Provider — Architecture

Components and responsibilities
- Virtual Kubelet provider (VK)
  - Entrypoint: cmd/virtual-kubelet/main.go (sets logrus JSON, cobra root, registers provider)
  - Provider impl: internal/provider/anex/provider.go (Pod CRUD against external cloud; annotates pods with anex.sh/proxy-slot-id; status updates; lifecycle reconcile)
  - Cloud provider client(s): cloudprovider/vastai/* (VastAI API), cloudprovider/mock/* (mock)
  - Virtual pod abstraction: virtualpod/virtualpod.go (agent HTTP status polling, restart logic, container state transitions)

- Gateway controller (singleton)
  - Entrypoint: cmd/gateway-controller/main.go (flags/env, builds clients/informers, loads gateway pod labels, starts controller)
  - Control loop: internal/gateway/controller.go (informers, queue, cache, VirtualService finalizers/status, Service writes)
  - Reconcile logic: internal/gateway/reconcile.go (validate spec, port allocation, generated Service, HAProxy config, Conditions)
  - HAProxy manager: internal/gateway/haproxy/manager.go (Data Plane API via http/unix socket, transactions, force_reload)
  - Port allocator: internal/gateway/portalloc/allocator.go (in-memory range 6000–9999 per ownerKey)

- Container Agent (runs inside remote container)
  - Entrypoint: cmd/container-agent/main.go (cobra, flags, run subcommand)
  - Runtime: internal/agent/agent.go (HTTP server; /status, /run, /sigterm, /push_file, /restart_wireproxy; start wireproxy/promtail; process management)
  - HTTP handlers: internal/agent/server.go

- API and CRD
  - Types: api/v1alpha1/virtualservice_types.go (spec/service/gateway, status/allocatedPorts/conditions, constants)
  - CRD: deploy/chart/crds/anex.sh_virtualservices.yaml

Key flows

1) Virtual Pod lifecycle (VK provider)
- CreatePod:
  - Rejects multi-container pods; reserves a gateway slot and annotates pod with anex.sh/proxy-slot-id (internal/provider/anex/provider.go: CreatePod)
  - Initializes Pod status to Pending and ContainerWaiting; sets up config maps; constructs a virtualpod.VirtualPod and starts async provisioning (initializeVirtualPod) (internal/provider/anex/provider.go)
- Agent integration:
  - virtualpod.GetAgentAddress derives http://10.254.254.(11+slot):agentPort (virtualpod/virtualpod.go)
  - virtualpod.PodStatusUpdate polls agent /status (internal/agent/server.go) via utils.MakeRequest, updates container state, manages restarts/backoff and Pod phase (virtualpod/virtualpod.go)
- DeletePod:
  - Terminates machine via cloudprovider, releases gateway slot, finalizes virtual pod, and updates status (internal/provider/anex/provider.go: DeletePod)
- Periodic reconcile:
  - reconcilePodLifecycle ticker reads agent status with timeouts/backoff and emits Pod updates/metrics (internal/provider/anex/provider.go)

2) VirtualService reconciliation (Gateway)
- Triggered by:
  - VirtualService add/update/delete via dynamic informer; Pod events for pods annotated virtual: "true" (internal/gateway/controller.go)
- Steps (internal/gateway/reconcile.go: reconcileVirtualService):
  - Validate spec (TCP only, int ports) — validateVirtualService
  - Ensure port allocations exist/reuse (status.AllocatedPorts) — ensurePortAllocations
  - Compute matching virtual pods by selector and annotation — getMatchingPods
  - Ensure generated Service (same name/namespace) selects gateway labels; targetPort = allocated gatewayPort — ensureGeneratedService
  - Configure HAProxy frontends/backends via Data Plane API — configureHAProxy
  - Update status conditions (Ready True/False + reason) — setConditionAndUpdate
- Finalization:
  - On deletion, remove HAProxy listeners/backends, release allocated ports, delete generated Service if owned, remove finalizer (internal/gateway/reconcile.go: handleVirtualServiceFinalization)

3) Networking/data path
- Wireguard addressing:
  - WireguardSubnetBase = "10.254.254.", WireguardSlotOffset = 11; pod wg IP = 10.254.254.(11 + proxySlotID) (internal/gateway/controller.go)
- Backend port formula (wireproxy):
  - listenPort = 10000 + proxySlotID*100 + portID, where portID is index of targetPort in pod’s sorted container ports (internal/gateway/reconcile.go: calculateBackendPort)
- Load balancing:
  - For each AllocatedPort (gatewayPort), HAProxy frontend listens on gatewayPort; backends point to each matching pod’s wgIP:listenPort (internal/gateway/reconcile.go → internal/gateway/haproxy/manager.go)
- Cluster exposure:
  - Generated ClusterIP Service (same name/namespace) selects Gateway pod labels; Service.spec.ports[].port = user port; targetPort = gatewayPort (internal/gateway/reconcile.go)

Boundaries and invariants
- VirtualService is SSOT; controller owns only:
  - VirtualService.status, finalizers and the generated Service with OwnerReference (internal/gateway/reconcile.go)
- Gateway is singleton:
  - cmd/gateway-controller/main.go requires gateway pod name/namespace; uses that pod’s labels as selector for the generated Service
- Only “virtual pods” are eligible backends:
  - Check pod.Annotations["virtual"] == "true" (internal/gateway/controller.go: AnnotationVirtualPod)
- No mutation of user-defined Services:
  - If a Service with same name exists and is not owned, set Ready=False, Reason=ServiceConflict and do nothing (internal/gateway/reconcile.go)
- Port range is dedicated and persisted in status:
  - Default 6000–9999; allocator is in-memory; status.AllocatedPorts ensures stability across restarts (internal/gateway/controller.go constants; portalloc allocator; ensurePortAllocations)

External dependencies and integrations
- Kubernetes client-go:
  - Dynamic client and informers for CRD/Pods/Services; status updates via UpdateStatus (cmd/gateway-controller/main.go, internal/gateway/controller.go)
- HAProxy Data Plane API:
  - HTTP(S) or unix socket; transactions, ensure frontend/backend/servers, force_reload (internal/gateway/haproxy/manager.go)
- VastAI API:
  - Cloud operations for machines/containers (cloudprovider/vastai/*)
- Wireproxy + Promtail (inside container):
  - Agent renders wireproxy.conf from /etc/virtualpod/wireproxy.tpl and starts binaries (internal/agent/agent.go)
- Codegen:
  - controller-gen for CRD/DeepCopy (Makefile: generate, generate-crds, generate-deepcopy)

Hot spots / delicate areas
- Port allocations vs. restarts:
  - Allocator is in-memory; ensurePortAllocations must reuse status.AllocatedPorts when matching spec to avoid reshuffle (internal/gateway/reconcile.go)
- Backend port calculation:
  - calculateBackendPort requires targetPort to exist in pod’s container ports; if not found, backend is skipped with warning (internal/gateway/reconcile.go)
- Generated Service collisions:
  - Name conflicts set Ready=False with Reason=ServiceConflict; ensure ownership before updates (internal/gateway/reconcile.go)
- HAProxy transactions:
  - Data Plane readiness is polled; each change set uses a transaction and force_reload; handle partial failures carefully (internal/gateway/haproxy/manager.go)
- Concurrency and caches:
  - Controller caches virtualPods/virtualServices with mutexes; event handlers update caches and enqueue keys (internal/gateway/controller.go)
- Single-container constraint:
  - Provider only supports a single container per pod; multi-container pods should be rejected (internal/provider/anex/provider.go: CreatePod)
