GPU Provider — Dev Workflows (Placeholder)

Build
- TODO
- Found in Makefile:
  - make build (builds all binaries into bin/)
  - make build-virtual-kubelet
  - make build-gateway-init
  - make build-gateway-controller
  - make build-container-agent
  - make generate (runs deepcopy + CRD codegen)
  - make install-controller-gen (installs controller-gen v0.16.5)
  - make docker-build (builds VK, gateway, container-agent images)
