#!/bin/bash

set -e

if [ -z "$GATEWAY_CONFIG_PATH" ]; then
    echo "Error: GATEWAY_CONFIG_PATH environment variable is not set"
    exit 1
fi

if [ ! -f "$GATEWAY_CONFIG_PATH" ]; then
    echo "Error: $GATEWAY_CONFIG_PATH not found"
    exit 1
fi

cd /etc/wireguard
gomplate -d config=$GATEWAY_CONFIG_PATH -f wg0.conf.tmpl > wg0.conf

export IF=$(ip route show default | awk '{print $5}')
wg-quick up wg0

# Create HAProxy configuration
cat > /etc/haproxy/haproxy.cfg <<'EOF'
global
    log stdout local0
    maxconn 4096
    stats socket /var/run/haproxy/haproxy.sock mode 660 level admin
    stats timeout 30s

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5000
    timeout client  50000
    timeout server  50000

# Health check endpoint
frontend healthcheck
    bind *:9000
    mode http
    monitor-uri /health
    http-request return status 200 content-type text/plain string "OK\n" if { path /health }

# Note: Individual VirtualService frontends/backends will be created dynamically
# by the gateway-controller using the HAProxy runtime API
EOF

# Create HAProxy socket directory
mkdir -p /var/run/haproxy

# Start HAProxy in background
haproxy -f /etc/haproxy/haproxy.cfg &

# Keep the script running
wait
