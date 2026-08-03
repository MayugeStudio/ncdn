#!/bin/bash
# failover.js を netns U 内で走らせ、出力を render.py に食わせて画面を上書き更新する。
# 集計と再描画を k6 の外に出している理由は render.py の docstring を参照。
set -eo pipefail

cd "$(dirname "$0")"

K6=$(command -v k6) || { echo "k6 not found in PATH" >&2; exit 1; }
RUN_USER=${SUDO_USER:-$(id -un)}
NETNS=${NETNS:-U}
SCRIPT=${SCRIPT:-failover.js}

# python3 は -u を付けないと出力がブロックバッファされてリアルタイムにならない
sudo ip netns exec "${NETNS}" sudo -u "${RUN_USER}" "${K6}" run -q "$@" "${SCRIPT}" 2>&1 \
	| python3 -u ./render.py
