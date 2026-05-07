#!/bin/bash
set -euo pipefail

mkdir -p /etc/virtualpod
echo "${GPU_PROVIDER_WIREPROXY_CONFIG}" | base64 -d > /etc/virtualpod/wireproxy.tpl

[ -e /usr/bin/wireproxy ] || { curl -fsSL "${GPU_PROVIDER_WIREPROXY_URL}" -o /usr/bin/wireproxy && chmod +x /usr/bin/wireproxy; }
[ -e /usr/bin/container_agent ] || { curl -fsSL "${GPU_PROVIDER_AGENT_URL}" -o /usr/bin/container_agent && chmod +x /usr/bin/container_agent; }
[ -e /usr/bin/wstunnel ] || { curl -fsSL "${GPU_PROVIDER_WSTUNNEL_URL}" -o /usr/bin/wstunnel && chmod +x /usr/bin/wstunnel; }
[ -e /usr/bin/promtail ] || { [ -n "${GPU_PROVIDER_PROMTAIL_URL:-}" ] && curl -fsSL "${GPU_PROVIDER_PROMTAIL_URL}" -o /usr/bin/promtail && chmod +x /usr/bin/promtail; }

unset AWS_WEB_IDENTITY_TOKEN_FILE
export PIP_PROXY="http://127.0.0.1:3128"

# Tunnel WG UDP traffic over WSS to gateway (RunPod blocks UDP ports)
wstunnel client -L "udp://51820:localhost:51820?timeout_sec=0" "ws://${GPU_PROVIDER_GATEWAY_ENDPOINT}:${GPU_PROVIDER_GATEWAY_WS_PORT}" &

[ -n "${GPU_PROVIDER_WORKDIR:-}" ] && cd "${GPU_PROVIDER_WORKDIR}"
eval "${GPU_PROVIDER_AGENT_CMD}"
