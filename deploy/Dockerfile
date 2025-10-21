FROM ubuntu:22.04

RUN apt-get update && \
    apt-get install -y \
    curl \
    unzip && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY bin/virtual_kubelet /usr/bin/virtual-kubelet

ENTRYPOINT [ "/usr/bin/virtual-kubelet" ]
CMD [ "help" ]
