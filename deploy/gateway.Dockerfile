FROM ubuntu:22.04

RUN apt-get update && \
    apt-get install -y \
    curl unzip iptables iproute2 wireguard && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

RUN curl -L https://github.com/hairyhenderson/gomplate/releases/latest/download/gomplate_linux-amd64 \
      -o /usr/local/bin/gomplate && \
    chmod +x /usr/local/bin/gomplate

COPY wg0.conf.tmpl /etc/wireguard/wg0.conf.tmpl
COPY gateway.sh /gateway.sh

CMD [ "/gateway.sh" ]
