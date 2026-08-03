import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const TARGET = __ENV.TARGET || 'http://192.0.2.10:8889/statusz';
const NODES = (__ENV.NODES || 'C0,C1,C2,C3,C4').split(',');
const SLEEP = Number(__ENV.SLEEP || 1);
const DURATION = __ENV.DURATION || '30s';
const VUS = Number(__ENV.VUS || 10);
const PROBE_N = Number(__ENV.PROBE_N || 10);
// handleSummary の書き出し先。run.sh から渡される
const OUT_DIR = __ENV.OUT_DIR || '/tmp/k6';
const RUN_TS = __ENV.RUN_TS || 'latest';

const hits = {};
for (const n of NODES) {
  hits[n] = new Counter(`hits_${n}`);
}
const hitsUnknown = new Counter('hits_unknown');

export const options = {
  scenarios: {
    // 本番の負荷。集計用の Counter はこちらだけが積む
    load: {
      executor: 'constant-vus',
      vus: VUS,
      duration: DURATION,
      exec: 'load',
    },
    // 実況用。毎秒1イテレーション、その中で PROBE_N 発撃って1行にまとめる
    probe: {
      executor: 'constant-arrival-rate',
      rate: 1,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 2,
      exec: 'probe',
    },
  },

  // 1 リクエスト = 1 コネクション = 1 ハッシュサンプル にするために必須。
  // keep-alive のままだと 5-tuple が変わらず同じバックエンドに固定される。
  noConnectionReuse: true,
};

function nodeIdOf(res) {
  if (res.status !== 200) return null;
  try {
    const body = res.json();
    return body.id || body.PopCacheId || null;
  } catch (e) {
    return null;
  }
}

export function load() {
  const res = http.get(TARGET, { tags: { name: 'statusz' } });

  if (!check(res, { 'status is 200': (r) => r.status === 200 })) {
    hitsUnknown.add(1, { node: `status_${res.status}` });
    if (SLEEP > 0) sleep(SLEEP);
    return;
  }

  const id = nodeIdOf(res);
  if (id !== null && hits[id] !== undefined) {
    hits[id].add(1, { node: id });
  } else {
    hitsUnknown.add(1, { node: String(id) });
  }

  if (SLEEP > 0) sleep(SLEEP);
}

export function probe() {
  // batch なら並列に飛ぶのでイテレーションが 1 秒以内に収まる
  const reqs = [];
  for (let i = 0; i < PROBE_N; i++) {
    reqs.push(['GET', TARGET, null, { tags: { name: 'probe' } }]);
  }
  const resps = http.batch(reqs);

  const tally = {};
  let err = 0;
  for (const r of resps) {
    const id = nodeIdOf(r);
    if (id !== null && hits[id] !== undefined) tally[id] = (tally[id] || 0) + 1;
    else err++;
  }

  // toISOString() は UTC になり k6 のログ接頭辞 (ローカル時刻) とズレるのでローカルで出す
  const t = new Date().toTimeString().slice(0, 8);
  let line = `${t} `;
  let bar = '';
  for (let i = 0; i < NODES.length; i++) {
    const c = tally[NODES[i]] || 0;
    line += `${NODES[i]}=${c} `;
    bar += String(i).repeat(c); // C0->'0', C1->'1', ...
  }
  bar += '.'.repeat(Math.max(0, PROBE_N - bar.length)); // 失敗した分
  console.log(`${line}err=${err}  |${bar}|`);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count : 0;
}

export function handleSummary(data) {
  const counts = {};
  let total = 0;
  for (const n of NODES) {
    counts[n] = count(data, `hits_${n}`);
    total += counts[n];
  }

  let s = '\n=== cache server distribution (load scenario only) ===\n';
  for (const n of NODES) {
    const pct = total > 0 ? (counts[n] / total) * 100 : 0;
    s += `  ${n.padEnd(6)} ${String(counts[n]).padStart(7)}  ${pct.toFixed(1).padStart(5)}%  ${'#'.repeat(Math.round(pct / 2))}\n`;
  }
  s += `  ${'unknown'.padEnd(6)} ${String(count(data, 'hits_unknown')).padStart(7)}\n`;
  s += `  total  ${String(total).padStart(7)}\n`;

  const dur = data.metrics.http_req_duration;
  if (dur) s += `\n  http_req_duration  avg=${dur.values.avg.toFixed(2)}ms p95=${dur.values['p(95)'].toFixed(2)}ms\n`;
  const failed = data.metrics.http_req_failed;
  if (failed) s += `  http_req_failed    ${(failed.values.rate * 100).toFixed(2)}%\n`;

  // 戻り値のキーがファイルパスになり、k6 がその中身を書き出す。
  // 'stdout' だけは端末への出力。
  const out = { stdout: s };
  // 全メトリクスの生データも残す (デフォルトサマリで見えていた項目は全部ここにある)
  out[`${OUT_DIR}/summary-${RUN_TS}.json`] = JSON.stringify(data, null, 2);
  return out;
}

