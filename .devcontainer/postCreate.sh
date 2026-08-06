#!/bin/bash

sudo apt update
sudo apt install --no-install-recommends -y \
    libssl-dev sshpass dnsutils ethtool \
    supervisor iputils-ping tcpdump bind9-dnsutils \
    build-essential libbpf-dev clang llvm bpftool
sudo apt install -y linux-headers-$(uname -r)

go get -v ./...

# k6 (負荷試験・分散計測用)
go install go.k6.io/k6/v2@latest
sudo ln -sf "$(command -v k6)" /usr/local/bin/k6


sudo bpftool map show | grep -i maglev
