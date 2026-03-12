FROM ubuntu:22.04

RUN apt-get update && \
    apt-get install -y \
    curl unzip iptables iproute2 wireguard busybox haproxy && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install HAProxy Data Plane API
RUN curl -L https://github.com/haproxytech/dataplaneapi/releases/download/v2.9.0/dataplaneapi_2.9.0_linux_x86_64.tar.gz \
    -o /tmp/dataplaneapi.tar.gz && \
    tar -xzf /tmp/dataplaneapi.tar.gz -C /usr/local/bin/ && \
    chmod +x /usr/local/bin/dataplaneapi && \
    rm /tmp/dataplaneapi.tar.gz

RUN mkdir -p /busybox

RUN curl -L https://github.com/hairyhenderson/gomplate/releases/latest/download/gomplate_linux-amd64 \
      -o /usr/local/bin/gomplate && \
    chmod +x /usr/local/bin/gomplate

COPY dependency/bin/wstunnel /usr/local/bin/wstunnel
COPY deploy/wg0.conf.tmpl /etc/wireguard/wg0.conf.tmpl
COPY deploy/gateway.sh /gateway.sh
COPY bin/gateway_init /gateway-init
COPY bin/gateway_controller /gateway-controller

CMD [ "/gateway.sh" ]
