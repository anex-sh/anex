FROM golang:1.24-bookworm

LABEL org.opencontainers.image.title="gpu-provider CI image" \
      org.opencontainers.image.description="CI image for linting, testing, building, docker and helm releases" \
      org.opencontainers.image.source="gitlab.devklarka.cz/ai/gpu-provider"

ARG HELM_VERSION=v3.15.4
ARG GOLANGCI_LINT_VERSION=v1.64.8

# Base tooling
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    unzip \
    bash \
    make \
    gcc g++ libc6-dev pkg-config \
    jq \
    docker.io \
  && rm -rf /var/lib/apt/lists/*

# AWS CLI v2 (self-contained; no Python runtime)
RUN curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip \
  && unzip -q /tmp/awscliv2.zip -d /tmp \
  && /tmp/aws/install -i /usr/local/aws-cli -b /usr/local/bin \
  && rm -rf /tmp/* \
  && aws --version

# Helm 3
RUN curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" -o /tmp/helm.tgz \
  && tar -xzf /tmp/helm.tgz -C /tmp \
  && mv /tmp/linux-amd64/helm /usr/local/bin/helm \
  && chmod +x /usr/local/bin/helm \
  && rm -rf /tmp/*

# golangci-lint
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b /usr/local/bin ${GOLANGCI_LINT_VERSION}

# Default env suitable for static builds (jobs can override as needed)
ENV CGO_ENABLED=0
WORKDIR /workspace

# Quick sanity default command
CMD ["bash", "-lc", "go version && golangci-lint --version && helm version && aws --version && docker --version"]
