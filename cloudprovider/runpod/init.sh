#!/bin/bash
set -euo pipefail

mkdir -p /etc/virtualpod
echo "${GPU_PROVIDER_WIREPROXY_CONFIG}" | base64 -d > /etc/virtualpod/wireproxy.tpl

curl -fsSL "${GPU_PROVIDER_WIREPROXY_URL}" -o /usr/bin/wireproxy
curl -fsSL "${GPU_PROVIDER_AGENT_URL}" -o /container_agent
[ -n "${GPU_PROVIDER_PROMTAIL_URL:-}" ] && curl -fsSL "${GPU_PROVIDER_PROMTAIL_URL}" -o /usr/bin/promtail && chmod +x /usr/bin/promtail

chmod +x /usr/bin/wireproxy /container_agent

unset AWS_WEB_IDENTITY_TOKEN_FILE
export PIP_PROXY="http://127.0.0.1:3128"
[ -n "${GPU_PROVIDER_WORKDIR:-}" ] && cd "${GPU_PROVIDER_WORKDIR}"

eval "${GPU_PROVIDER_AGENT_CMD}"
