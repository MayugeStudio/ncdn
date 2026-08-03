import http from 'k6/http';
import { sleep } from 'k6';
import { Counter } from 'k6/metrics';

const NODES = ['C0', 'C1', 'C2', 'C3', 'C4']
const LOG_INTERVAL_MS = Number(__ENV.LOG_INTERVAL_MS || 1000);

const hits = {};
for (const n of NODES) {
  hits[n] = new Counter(n)
}
const hitsUnknown = new Counter('hits_unknown');

let lastLogAt = 0;

export const options = {
  vus: 30,
  duration: '5m',
  noConnectionReuse: false,
}

// 見たいのは「この VU の1本のコネクションが今どこに刺さっているか」だけ。
// 累計は Counter がサマリ用に持っているので、ここでは出さない。
// 集計と再描画は render.py 側 (console.log からは ANSI が出せないため)。
function logHits(current) {
  const now = Date.now();
  if (now - lastLogAt < LOG_INTERVAL_MS) return;
  lastLogAt = now;

  console.log(`HITS vu=${__VU} node=${current}`);
}

export default function () {
  // res を try の外で宣言しないと、catch 節から参照したときに
  // ReferenceError になって元のエラーが握り潰される。
  let res;
  let id = 'ERR';
  try {
    res = http.get('http://192.0.2.10:8889/statusz');
    const body = res.json();
    id = body.id;
    if (id !== null && hits[id] !== undefined) {
      hits[id].add(1, { node: id });
    } else {
      hitsUnknown.add(1, { node: String(id) });
      id = 'unknown';
    }
  } catch (e) {
    hitsUnknown.add(1, { node: 'error' });
    id = 'ERR';
  }

  logHits(id);
  sleep(0.1);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count : 0;
}

export function handleSummary(data) {
  const counts = {};
  let total = 0;
  for (const n of NODES) {
    counts[n] = count(data, n);
    total += counts[n];
  }
  let s = '';
  for (const n of NODES) {
    const p = total > 0 ? counts[n]/total * 100 : 0;
    s += `${n.padEnd(6)}: ${p.toFixed(1)}% ${String(counts[n]).padStart(5)}\n`
  }
  s += `total: ${total}\n`
  const failed = data.metrics.http_req_failed;
  if (failed) {
    s += `http_req_failed: ${(failed.values.rate * 100).toFixed(2)}%\n`;
  }
  const out = { stdout: s };
  return out
}
