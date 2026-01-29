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

userlist dataplaneapi
  user admin insecure-password admin

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

# Write HAProxy reload helper script
cat > /usr/local/bin/haproxy-reload.sh <<'EOS'
#!/bin/sh
set -e
PIDFILE="/var/run/haproxy.pid"
CFG="/etc/haproxy/haproxy.cfg"
BIN="/usr/sbin/haproxy"

if [ -f "$PIDFILE" ] && [ -s "$PIDFILE" ]; then
  OLD="$(cat "$PIDFILE" || true)"
  if [ -n "$OLD" ]; then
    "$BIN" -D -f "$CFG" -p "$PIDFILE" -sf "$OLD"
    exit $?
  fi
fi

"$BIN" -D -f "$CFG" -p "$PIDFILE"
EOS
chmod +x /usr/local/bin/haproxy-reload.sh

# Create HAProxy Data Plane API configuration
cat > /etc/haproxy/dataplaneapi.yaml <<'EOF'
config_version: 2
name: haproxy

dataplaneapi:
  host: 127.0.0.1
  port: 5555
  log:
    level: debug

  userlist:
    userlist: dataplaneapi
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
    reload_delay: 0
    reload_cmd: /usr/local/bin/haproxy-reload.sh
    restart_cmd: /usr/local/bin/haproxy-reload.sh
  
  master_runtime: /var/run/haproxy/haproxy.sock
EOF

# Create necessary directories
mkdir -p /var/run/haproxy
mkdir -p /tmp/haproxy-transactions
mkdir -p /etc/haproxy/maps
mkdir -p /etc/haproxy/ssl

# Start HAProxy in background (daemon mode with PID file)
haproxy -D -f /etc/haproxy/haproxy.cfg -p /var/run/haproxy.pid

# Wait for HAProxy runtime socket to appear
for i in $(seq 1 30); do
  if [ -S /var/run/haproxy/haproxy.sock ]; then
    echo "HAProxy runtime socket is ready"
    break
  fi
  sleep 1
done

if [ ! -S /var/run/haproxy/haproxy.sock ]; then
  echo "Error: HAProxy runtime socket was not created at /var/run/haproxy/haproxy.sock within timeout"
  exit 1
fi

# Start HAProxy Data Plane API
/usr/local/bin/dataplaneapi -f /etc/haproxy/dataplaneapi.yaml --log-level debug &


# Keep the script running
wait
