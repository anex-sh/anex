#!/bin/bash

set -e

if [ ! -f /etc/wireguard/proxy.yaml ]; then
    echo "Error: /etc/wireguard/proxy.yaml not found"
    exit 1
fi

cd /etc/wireguard
gomplate -d config=proxy.yaml -f wg0.conf.tmpl > wg0.conf

export IF=$(ip route show default | awk '{print $5}')
wg-quick up wg0

busybox httpd -f -p 0.0.0.0:9000 -h /busybox
