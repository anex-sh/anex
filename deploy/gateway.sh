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

busybox httpd -f -p 0.0.0.0:9000 -h /busybox
