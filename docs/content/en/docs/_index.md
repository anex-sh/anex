---
title: "GPU Provider"
linkTitle: "GPU Provider"
weight: 20
---

## What is GPU Provider

- Tool for running gpu workloads deployed as standard pods in significantly cheaper cloud provider like VastAI and others
- GPU Provider is the extension of virtual kubelet. Deploying it into cluster creates virtual node. Pods scheduled there will spawn its containers in selected cloud provider (VastAI)
- Provider exposes Gateway with Wireguard endpoint to which remote containers connect. This allows for full communication between containers and rest of the cluster
- Scheduling will be done by default scheduler. No changes to existing infra needs to be done

## Limitations

- only 1 container per pod
  applicable for: VastAI

- no image command default (must be explicitly specified)
  applicable for: all

- kubectl commands acting on node (logs|exec) are not available
  applicable for: managed clusters without access to root CA

- user-space limited networking model
  applicable for: VastAI

  needs to connect over HTTP Proxy
  or
  configure TLS tunnel

## Unimplemented Features (as of v0.2.0)

- service accounts

