#!/usr/bin/env python3
"""failover.js の `HITS vu=N node=X` を受けて、コネクション分布を上書き表示する。

見せるのは「今この瞬間、どのバックエンドに何本刺さっているか」の一点。
keep-alive なので 1 VU = 1本の長命コネクションであり、VU の現在ノードを
数え上げればそれがそのままコネクション分布になる。

k6 側でやらない理由:
  - console.log は logrus を通るので ANSI エスケープが文字列に化ける
  - VU 同士は状態を共有できないので全 VU の合算ができない
(mawk の fflush() は黙って効かないので awk ではなく Python)
"""
import re
import sys
import time

NODES = ["C0", "C1", "C2", "C3", "C4"]
ERR = "err"
BAR_WIDTH = 30
MOVES_SHOWN = 6

# logrus が msg を引用符で囲むので、`\S+` だと node の値が閉じ引用符まで飲み込む
HITS_RE = re.compile(r'HITS vu=([^\s"]+) node=([^\s"]+)')

current = {}   # vu -> node
moves = []     # 直近の移動履歴
started = time.time()


def bucket(node):
    return node if node in NODES else ERR


def render():
    conns = {n: 0 for n in NODES}
    conns[ERR] = 0
    for node in current.values():
        conns[node] += 1

    total = sum(conns.values())
    live = total - conns[ERR]
    el = int(time.time() - started)

    out = [f"elapsed {el // 60:02d}:{el % 60:02d}   vus={total}   live={live}   err={conns[ERR]}", ""]

    for n in NODES + [ERR]:
        c = conns[n]
        pct = c * 100 / total if total else 0
        bar = "█" * round(pct / 100 * BAR_WIDTH)
        out.append(f"{n:<5} {bar:<{BAR_WIDTH}} {c:>3}  {pct:>5.1f}%")

    if moves:
        out.append("")
        out.append("recent moves")
        out.extend(moves[-MOVES_SHOWN:])

    sys.stdout.write("\033[H\033[2J" + "\n".join(out) + "\n")
    sys.stdout.flush()


def main():
    # `for line in sys.stdin` はブロック単位で読むためリアルタイムにならない
    for line in iter(sys.stdin.readline, ""):
        m = HITS_RE.search(line)
        if not m:
            continue

        vu, node = m.group(1), bucket(m.group(2))
        prev = current.get(vu)
        if prev is not None and prev != node:
            moves.append(f"  {time.strftime('%H:%M:%S')}  VU{vu:<3} {prev} -> {node}")
        current[vu] = node
        render()

    render()


if __name__ == "__main__":
    main()
