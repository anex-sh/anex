# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
make build                            # Build all four binaries into bin/
make test                             # go test -v ./...
make test-gateway                     # Gateway integration tests (needs envtest)
make test-one TEST=<TestName>         # Single test by name
```

After completing any task, verify with `make build`.

Gateway integration tests require kubebuilder envtest. If auto-install fails, set:
`KUBEBUILDER_ASSETS=/home/skarupa/.local/share/kubebuilder-envtest/k8s/1.35.0-linux-amd64`

## What This Is

A **Virtual Kubelet provider** that exposes containers running on rented GPU machines (Vast.AI or RunPod) as Kubernetes Pods scheduled to a virtual node. A **Gateway controller** bridges cluster-side traffic to those remote containers over a Wireguard subnet (plain UDP for Vast.AI, wstunnel WebSocket tunnel for RunPod) using HAProxy L4 load balancing.

## Four Binaries

| Binary | Entry point | Role |
|---|---|---|
| `virtual-kubelet` | `cmd/virtual-kubelet/` | Registers virtual K8s node; Pod CRUD via pluggable cloud provider |
| `gateway-controller` | `cmd/gateway-controller/` | Reconciles VirtualService CRD; programs HAProxy + generates Services |
| `gateway-init` | `cmd/gateway-init/` | One-shot init container that configures the gateway pod |
| `container-agent` | `cmd/container-agent/` | Runs inside the remote container; exposes `/status`, `/run`, `/sigterm`, `/push_file`, `/restart_wireproxy` |

## Key Concepts

**Virtual Pod** — a K8s Pod on the virtual node whose container actually runs on a remote GPU machine. Must be annotated `virtual: "true"` to be eligible for VirtualService selection.

**Cloud Provider Interface** — `cloudprovider.Client` defines the contract for machine lifecycle ops. Active provider selected via `cloudProvider.active` config (`vastai`, `runpod`, `mock`). RunPod lacks some ops (bans, `MapRunningMachines`, `CopyFileToMachine`, `RenewMachineKeys`).

**MachineSpecification** — unified machine filtering struct in `virtualpod/machine.go`. Shared filters (GPU names, regions, VRAM, RAM, CPU, price) via `anex.sh/` annotations. Provider-specific filters via `vastai.anex.sh/` and `runpod.anex.sh/` prefixes. RunPod uses a hardcoded GPU price dictionary for local filtering.

**Wstunnel** — RunPod blocks UDP, so Wireguard traffic is tunneled over WebSocket. Gateway runs wstunnel server on TCP 51821 forwarding to local UDP 51820. RunPod containers run wstunnel client connecting via `ws://gateway:51821`.

**Gateway** — singleton networking pod that terminates Wireguard and runs HAProxy. Also runs wstunnel server for RunPod connectivity. Controller requires its pod name/namespace at startup.

**VirtualService (CRD, `vsvc`)** — `anex.sh/v1alpha1`. Source of truth for L4 exposure of virtual pods. TCP only; `targetPort` must be an int; no named ports or sessionAffinity. Controller creates a ClusterIP Service with the same name/namespace that selects the Gateway pod; `targetPort` = allocated `gatewayPort`.

**Proxy Slot ID** — integer assigned per virtual pod. Annotation: `anex.sh/proxy-slot-id`.
- Wireguard IP: `10.254.254.(11 + slotID)`
- Wireproxy backend port: `10000 + slotID*100 + portID` (portID = index of targetPort in pod's sorted container ports)

**Port allocator** — in-memory, range 6000–9999. Allocations are persisted in `VirtualService.status.allocatedPorts` and reused on restart to avoid reshuffling.

## Code Map

| Path | Purpose |
|---|---|
| `internal/provider/anex/provider.go` | Pod CRUD; assigns proxy slots; annotates pods |
| `internal/provider/anex/provisioning.go` | Machine lifecycle (rent → ready → teardown) |
| `internal/provider/anex/config.go` | YAML config loading + env var overrides |
| `virtualpod/virtualpod.go` | VirtualPod struct; agent HTTP polling; restart logic |
| `virtualpod/machine.go` | MachineSpecification struct & filtering |
| `cloudprovider/client.go` | Cloud provider interface (`Client`) |
| `cloudprovider/vastai/` | Vast.AI REST API client |
| `cloudprovider/vastai/bans.go` | Machine ban logic (persistent file-based) |
| `cloudprovider/runpod/` | RunPod REST API client + provisioning |
| `cloudprovider/mock/` | Mock client for tests |
| `internal/gateway/controller.go` | Informers, queue, cache, VirtualService finalizers |
| `internal/gateway/reconcile.go` | Validate → allocate ports → generate Service → configure HAProxy → set conditions |
| `internal/gateway/haproxy/manager.go` | HAProxy Data Plane API (HTTP or Unix socket, transactions, force_reload) |
| `internal/gateway/portalloc/allocator.go` | Port range manager (6000–9999) |
| `internal/agent/agent.go` | Agent HTTP server; starts wireproxy/promtail |
| `api/v1alpha1/virtualservice_types.go` | VirtualService CRD types |

## Constraints & Invariants

- Single-container pods only — `CreatePod` rejects multi-container pods.
- Gateway is singleton — controller uses its pod labels as selector for all generated Services.
- Controller owns only: `VirtualService.status`, finalizers, and the generated Service (with OwnerReference). Never mutates user-defined Services — name conflict → `Ready=False, Reason=ServiceConflict`.
- `calculateBackendPort` requires `targetPort` to exist in the pod's container ports; missing port → backend skipped with warning.
- RunPod provider: no machine bans, no `MapRunningMachines`/`CopyFileToMachine`/`RenewMachineKeys`; uses hardcoded GPU price dictionary for local filtering.

## Configuration

Provider config: YAML file passed via `--provider-config`. Every field can be overridden by an env var derived from the nested YAML path in `SCREAMING_SNAKE_CASE` (e.g. `cloudProvider.vastAI.apiKey` → `CLOUDPROVIDER_VASTAI_APIKEY`).

`cloudProvider.active` selects the provider (`vastai`, `runpod`, `mock`). RunPod config under `cloudProvider.runPod`: `apiKey`, plus URL fields for binary downloads (`initURL`, `agentURL`, `wireproxyURL`, `wstunnelURL`, `promtailURL`).

Gateway Wireguard keys: separate YAML file at the path in `gateway.configPath` (see `config.yaml` in repo root for structure).

## CRD Codegen

After changing types in `api/v1alpha1/`:
```bash
make install-controller-gen
controller-gen object paths="./..."          # regenerate deepcopy
controller-gen crd paths="./..."             # regenerate CRD manifests
```

## Docker Images

```bash
make docker-build-kubelet VERSION=<tag>   # → public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet
make docker-build-gateway VERSION=<tag>   # → public.ecr.aws/m4v1f8q5/gpu-provider/gateway
```
