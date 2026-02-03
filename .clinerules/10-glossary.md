GPU Provider — Glossary

Use these terms consistently across issues, code, and documentation.

Virtual Node
- Meaning: A Kubernetes node abstraction backed by Virtual Kubelet that schedules Pods to an external GPU provider instead of a real kubelet.
- Also known as / synonyms: VK node, virtual-kubelet node
- Where in code (paths): cmd/virtual-kubelet/main.go; internal/provider/glami/provider.go
- Notes / gotchas: Only single-container Pods are supported by the provider (internal/provider/glami/provider.go: CreatePod).

Virtual Pod
- Meaning: A Kubernetes Pod scheduled to the Virtual Node whose container actually runs on an external provider machine.
- Also known as / synonyms: remote pod, virtualized pod
- Where in code (paths): virtualpod/virtualpod.go (abstraction), internal/provider/glami/provider.go (lifecycle), internal/gateway/controller.go (matching)
- Notes / gotchas: Must be annotated virtual: "true" to be eligible for VirtualService selection (internal/gateway/controller.go: AnnotationVirtualPod and checks).

Gateway
- Meaning: Singleton networking component that terminates Wireguard, exposes HAProxy L4 listeners, and bridges traffic from the cluster to virtual pods.
- Also known as / synonyms: GPU Provider Gateway
- Where in code (paths): cmd/gateway-controller/main.go, internal/gateway/*
- Notes / gotchas: Assumed single instance; controller requires labels of the running gateway pod.

Gateway Controller
- Meaning: Controller that reconciles VirtualService objects and programs HAProxy + generated Services.
- Also known as / synonyms: VirtualService controller, gateway reconciler
- Where in code (paths): cmd/gateway-controller/main.go; internal/gateway/controller.go; internal/gateway/reconcile.go
- Notes / gotchas: Uses dynamic client to update VirtualService.status; idempotent with finalizers and owner refs.

VirtualService (CRD)
- Meaning: The single source of truth for Service-like L4 targeting of virtual pods.
- Also known as / synonyms: vsvc, gpu-provider.glami-ml.com/v1alpha1 VirtualService
- Where in code (paths): api/v1alpha1/virtualservice_types.go; deploy/chart/crds/gpu-provider.glami-ml.com_virtualservices.yaml
- Notes / gotchas: Only TCP; targetPort must be int; no named ports or sessionAffinity (internal/gateway/reconcile.go: validateVirtualService).

AllocatedPorts
- Meaning: Status field mapping each VirtualService port to an allocated gatewayPort.
- Also known as / synonyms: port mappings, gateway allocations
- Where in code (paths): api/v1alpha1/virtualservice_types.go (AllocatedPort); internal/gateway/reconcile.go (ensurePortAllocations)
- Notes / gotchas: Persisted in .status; stable across restarts; range 6000–9999 by default (internal/gateway/controller.go).

GatewayPort
- Meaning: The L4 port number on the Gateway that HAProxy listens on for a VirtualService port.
- Also known as / synonyms: frontend port
- Where in code (paths): api/v1alpha1/virtualservice_types.go (AllocatedPort.GatewayPort); internal/gateway/haproxy/manager.go; internal/gateway/reconcile.go
- Notes / gotchas: Mapped from ServicePort via allocator; used as Service.spec.ports[].targetPort of the generated Service.

Wireguard Subnet
- Meaning: Private subnet used between Gateway and virtual pods.
- Also known as / synonyms: wg subnet
- Where in code (paths): internal/gateway/controller.go (WireguardSubnetBase/Offset)
- Notes / gotchas: Pod wg IP = 10.254.254.(11 + proxySlotID). Derived from proxy slot id annotation.

Wireproxy
- Meaning: Userspace Wireguard client used in remote containers (cannot run full kernel WG).
- Also known as / synonyms: wg userspace proxy
- Where in code (paths): internal/agent/agent.go (startWireproxy); internal/agent/server.go
- Notes / gotchas: Agent renders config from /etc/virtualpod, runs wireproxy, and can restart it via /restart_wireproxy.

Proxy Slot ID
- Meaning: Integer slot assigned to each virtual pod to derive WG IP and port offsets.
- Also known as / synonyms: gateway slot, slot index
- Where in code (paths): Annotation key gpu-provider.glami.cz/proxy-slot-id (internal/gateway/controller.go); assigned in internal/provider/glami/provider.go (CreatePod)
- Notes / gotchas: Used to compute wg IP and wireproxy port formula.

Generated Service
- Meaning: ClusterIP Service created and owned by the VirtualService to expose L4 traffic via the Gateway.
- Also known as / synonyms: derived Service, Gateway-targeting Service
- Where in code (paths): internal/gateway/reconcile.go (ensureGeneratedService)
- Notes / gotchas: Same name/namespace as VirtualService; selector = gateway labels; targetPort = allocated gatewayPort.

HAProxy Data Plane API
- Meaning: API used to program HAProxy dynamically with transactions and optional force reload.
- Also known as / synonyms: HAProxy runtime API (Data Plane)
- Where in code (paths): internal/gateway/haproxy/manager.go
- Notes / gotchas: Supports both HTTP(S) endpoint and Unix socket; uses transactions per change set.

Port Allocator
- Meaning: In-memory allocator for gateway ports per VirtualService owner.
- Also known as / synonyms: allocation range manager
- Where in code (paths): internal/gateway/portalloc/allocator.go
- Notes / gotchas: Range defaults to 6000–9999; allocations are tracked per ownerKey (namespace/name).

Condition Types/Reasons (VirtualService)
- Meaning: Status conditions signaling reconcile state.
- Also known as / synonyms: Ready condition, status reasons
- Where in code (paths): api/v1alpha1/virtualservice_types.go (ConditionTypeReady, Reason*); internal/gateway/reconcile.go (setConditionAndUpdate)
- Notes / gotchas: Ready=True when Service + HAProxy configured; UnsupportedSpec and ServiceConflict are terminal until fixed.

Provider (Glami Provider)
- Meaning: Virtual Kubelet provider implementing Pod lifecycle against external cloud (VastAI/Mock).
- Also known as / synonyms: VK provider
- Where in code (paths): internal/provider/glami/*
- Notes / gotchas: Annotates pods with proxy-slot-id; manages machine lifecycle; reconciles container state via Agent.

Container Agent
- Meaning: Binary running in the remote container environment to simulate pod runtime and report status.
- Also known as / synonyms: agent
- Where in code (paths): cmd/container-agent/main.go; internal/agent/*
- Notes / gotchas: HTTP endpoints: /healthz, /status, /run, /sigterm, /push_file, /restart_wireproxy.

Wireproxy Port Formula
- Meaning: How backend listener ports are computed on the wireproxy side for each pod.
- Also known as / synonyms: backend port mapping
- Where in code (paths): internal/gateway/reconcile.go (calculateBackendPort)
- Notes / gotchas: listenPort = 10000 + proxySlotID*100 + portID, where portID is the index of targetPort within the pod’s sorted container ports.

VirtualService Selector
- Meaning: Label selector over virtual pods to form backend membership.
- Also known as / synonyms: service selector
- Where in code (paths): api/v1alpha1/virtualservice_types.go (ServiceSpec.Selector); internal/gateway/controller.go (matching)
- Notes / gotchas: Only pods annotated virtual: "true" are considered; readiness is assumed but lifecycle updates still handled.
