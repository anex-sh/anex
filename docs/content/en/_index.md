---
title: "Anex Documentation"
---

{{< blocks/cover title="Anex" image_anchor="top" height="full" >}}
<a class="btn btn-lg btn-primary me-3 mb-4" href="/docs/">
  Learn More <i class="fas fa-arrow-alt-circle-right ms-2"></i>
</a>
<a class="btn btn-lg btn-secondary me-3 mb-4" href="/docs/getting-started/">
  Get Started <i class="fab fa-github ms-2 "></i>
</a>
<p class="lead mt-5">Run GPU workloads on cheap cloud GPU providers from your existing Kubernetes cluster</p>
{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}
Anex lets you run GPU workloads on cheap cloud GPU providers (Vast.AI, RunPod) while managing them through your existing Kubernetes cluster. You write standard Kubernetes manifests, and Anex takes care of renting machines, setting up networking, and making remote GPU pods feel like they're part of your cluster.
{{% /blocks/lead %}}

{{% blocks/section color="dark" type="row" %}}

{{% blocks/feature icon="fa-lightbulb" title="Native Kubernetes" %}}
Standard Pods, Deployments and Jobs scheduled to a Virtual Kubelet node. No new APIs to learn — just pod annotations to pick the machine.
{{% /blocks/feature %}}

{{% blocks/feature icon="fab fa-github" title="Open Source" %}}
Source on [github.com/anex-sh/anex](https://github.com/anex-sh/anex). Pluggable cloud providers; Vast.AI and RunPod supported today.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-rocket" title="Cheap GPUs" %}}
Rent only what you need, when you need it. Pods scale up by provisioning machines on demand and tear them down when removed.
{{% /blocks/feature %}}

{{% /blocks/section %}}
