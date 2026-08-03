#!/bin/bash

sudo apt update
sudo apt install --no-install-recommends -y \
    libssl-dev sshpass dnsutils ethtool \
    supervisor iputils-ping tcpdump bind9-dnsutils \
    build-essential libbpf-dev clang llvm bpftool
sudo apt install -y linux-headers-$(uname -r)

go get -v ./...

# k6 (負荷試験・分散計測用)
# APT リポジトリ (dl.k6.io) は amd64 しか配っていないため、Apple Silicon 上の
# arm64 コンテナでは apt 経由で入らない。k6 自体が Go 製なので go install で入れる。
go install go.k6.io/k6/v2@latest
# sudo は /etc/sudoers の secure_path で PATH を上書きするため /go/bin が見えない。
# netns 内での実行に sudo が要るので、secure_path 内の /usr/local/bin に張っておく。
sudo ln -sf "$(command -v k6)" /usr/local/bin/k6

sudo bpftool map show | grep -i maglev
