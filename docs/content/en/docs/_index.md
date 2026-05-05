---
title: "Anex"
linkTitle: "Anex"
weight: 20
---

## What is Anex

- Tool for running GPU workloads as standard Kubernetes Pods on significantly cheaper cloud providers like [Vast.AI](https://vast.ai/) and [RunPod](https://runpod.io/)
- Anex is a [Virtual Kubelet](https://virtual-kubelet.io/) provider. Deploying it into a cluster registers a virtual node — Pods scheduled there have their containers spawned on a rented machine in the configured cloud provider
- A Gateway pod terminates a Wireguard tunnel that remote containers connect to, so cluster traffic can reach pods running on the rented machine
- Scheduling stays with the default scheduler. No changes to existing infra required

## Cloud Providers

Anex selects the active provider via `cloudProvider.active` (`vastai`, `runpod`, or `mock`):

| Provider | Notes |
|---|---|
| `vastai` | Full feature set: machine bans, hardware filters incl. DLPerf, GPU mapping. Wireguard over plain UDP. |
| `runpod` | Wireguard tunneled over WebSocket (wstunnel) since RunPod blocks UDP. No machine bans; uses a hardcoded GPU price dictionary for filtering. |
| `mock` | In-memory provider used in tests. |

## Limitations

- One container per pod (enforced by ValidatingAdmissionPolicy)
- No init containers; image command must be set explicitly on the container
- `kubectl logs` / `kubectl exec` are not available in managed clusters without root CA access
- User-space networking on Vast.AI: traffic to the cluster goes through an HTTP proxy or a TCP tunnel
- `Service` resources can't reach virtual pods directly — use `VirtualService` (CRD) instead
- Service accounts are not propagated to remote pods

## Roadmap

- Full Kubernetes Service API support (`LoadBalancer` already partially via NodePort `VirtualService`)
- Service account propagation
