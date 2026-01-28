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

# Create HAProxy configuration with Data Plane API
cat > /etc/haproxy/haproxy.cfg <<'EOF'
global
    log stdout local0
    maxconn 4096
    stats socket /var/run/haproxy/haproxy.sock mode 660 level admin expose-fd listeners
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
# by the gateway-controller using the HAProxy Data Plane API
EOF

# Create HAProxy Data Plane API configuration
cat > /etc/haproxy/dataplaneapi.yml <<'EOF'
config_version: 2
name: haproxy

dataplaneapi:
  host: 0.0.0.0
  port: 0
  unix_socket: /var/run/haproxy/dataplane.sock
  unix_socket_mode: "0660"
  user:
    - name: admin
      password: admin
      insecure: true
  
  transaction:
    transaction_dir: /tmp/haproxy-transactions

  resources:
    maps_dir: /etc/haproxy/maps
    ssl_certs_dir: /etc/haproxy/ssl

haproxy:
  config_file: /etc/haproxy/haproxy.cfg
  haproxy_bin: /usr/sbin/haproxy
  reload:
    reload_delay: 5
    reload_cmd: kill -SIGUSR2 1
    restart_cmd: kill -SIGUSR2 1
  
  master_runtime: /var/run/haproxy/haproxy.sock
EOF

# Create necessary directories
mkdir -p /var/run/haproxy
mkdir -p /tmp/haproxy-transactions
mkdir -p /etc/haproxy/maps
mkdir -p /etc/haproxy/ssl

# Start HAProxy in background
haproxy -f /etc/haproxy/haproxy.cfg &

# Wait for HAProxy to start
sleep 2

# Start HAProxy Data Plane API
/usr/local/bin/dataplaneapi -f /etc/haproxy/dataplaneapi.yml &

# Keep the script running
wait
