import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const TARGET = __ENV.TARGET || 'http://192.0.2.10:8889/index.html';
const NODES = (__ENV.NODES || 'C0,C1,C2,C3,C4').split(',');
const SLEEP = Number(__ENV.SLEEP || 1);
const DURATION = __ENV.DURATION || '30s';
const VUS = Number(__ENV.VUS || 10);
const PROBE_N = Number(__ENV.PROBE_N || 10);
// handleSummary の書き出し先。run.sh から渡される
const OUT_DIR = __ENV.OUT_DIR || '/tmp/k6';
const RUN_TS = __ENV.RUN_TS || 'latest';

// ノード別のリクエスト数
const reqs = {};
// ノード別の X-Cache: Hit / Miss
const cacheHit = {};
const cacheMiss = {};
for (const n of NODES) {
  reqs[n] = new Counter(`hits_${n}`);
  cacheHit[n] = new Counter(`cacheHit_${n}`);
  cacheMiss[n] = new Counter(`cacheMiss_${n}`);
}
const hitsUnknown = new Counter('hits_unknown');
const cacheUnknown = new Counter('cache_unknown');

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

// k6 の res.headers は Go の正規化済みキー ('X-Ncdn-Popcache-Nodeid') で入っており、
// 元のヘッダ名と大文字小文字が一致しない。取りこぼさないよう大小無視で引く。
function header(res, name) {
  const want = name.toLowerCase();
  for (const k in res.headers) {
    if (k.toLowerCase() === want) return res.headers[k];
  }
  return null;
}

// 応答したキャッシュサーバを見つける
function nodeIdOf(res) {
  if (res.status !== 200) return null;
  return header(res, 'X-NCDN-PoPCache-NodeId');
}

// 'Hit' | 'Miss' | null。想定外の値は握り潰さず null にして cache_unknown で可視化する
function cacheStateOf(res) {
  const v = header(res, 'X-Cache');
  if (v === null) return null;
  const s = v.trim().toLowerCase();
  if (s === 'hit') return 'Hit';
  if (s === 'miss') return 'Miss';
  return null;
}

export function load() {
  const res = http.get(TARGET, { tags: { name: 'target' } });

  if (!check(res, { 'status is 200': (r) => r.status === 200 })) {
    hitsUnknown.add(1, { node: `status_${res.status}` });
    if (SLEEP > 0) sleep(SLEEP);
    return;
  }

  const id = nodeIdOf(res);
  if (id !== null && reqs[id] !== undefined) {
    reqs[id].add(1, { node: id });

    const state = cacheStateOf(res);
    if (state === 'Hit') cacheHit[id].add(1, { node: id });
    else if (state === 'Miss') cacheMiss[id].add(1, { node: id });
    else cacheUnknown.add(1, { node: id });
  } else {
    hitsUnknown.add(1, { node: String(id) });
  }

  if (SLEEP > 0) sleep(SLEEP);
}

export function probe() {
  // batch なら並列に飛ぶのでイテレーションが 1 秒以内に収まる
  const batch = [];
  for (let i = 0; i < PROBE_N; i++) {
    batch.push(['GET', TARGET, null, { tags: { name: 'probe' } }]);
  }
  const resps = http.batch(batch);

  const tally = {};
  let err = 0;
  let nHit = 0;
  let nMiss = 0;
  let nodeBar = ''; // どのノードが応答したか (C0->'0', C1->'1', ...)
  let cacheBar = ''; // それが Hit だったか Miss だったか

  for (const r of resps) {
    const id = nodeIdOf(r);
    const idx = NODES.indexOf(id);
    if (idx < 0) {
      err++;
      nodeBar += '.';
      cacheBar += '.';
      continue;
    }

    tally[id] = (tally[id] || 0) + 1;
    nodeBar += String(idx);

    const state = cacheStateOf(r);
    if (state === 'Hit') {
      nHit++;
      cacheBar += 'H';
    } else if (state === 'Miss') {
      nMiss++;
      cacheBar += 'M';
    } else {
      cacheBar += '?';
    }
  }

  // toISOString() は UTC になり k6 のログ接頭辞 (ローカル時刻) とズレるのでローカルで出す
  const t = new Date().toTimeString().slice(0, 8);
  let line = `${t} `;
  for (const n of NODES) {
    line += `${n}=${tally[n] || 0} `;
  }
  const rate = nHit + nMiss > 0 ? ((nHit / (nHit + nMiss)) * 100).toFixed(0) : '--';
  console.log(`${line}hit=${nHit}/${nHit + nMiss} (${rate}%) err=${err}  |${nodeBar}| |${cacheBar}|`);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count : 0;
}

export function handleSummary(data) {
  const counts = {};
  const hitCounts = {};
  const missCounts = {};
  let total = 0;
  let totalHit = 0;
  let totalMiss = 0;
  for (const n of NODES) {
    counts[n] = count(data, `hits_${n}`);
    hitCounts[n] = count(data, `cacheHit_${n}`);
    missCounts[n] = count(data, `cacheMiss_${n}`);
    total += counts[n];
    totalHit += hitCounts[n];
    totalMiss += missCounts[n];
  }

  let s = '\n=== cache server distribution (load scenario only) ===\n';
  for (const n of NODES) {
    const pct = total > 0 ? (counts[n] / total) * 100 : 0;
    s += `  ${n.padEnd(6)} ${String(counts[n]).padStart(7)}  ${pct.toFixed(1).padStart(5)}%  ${'#'.repeat(Math.round(pct / 2))}\n`;
  }
  s += `  ${'unknown'.padEnd(6)} ${String(count(data, 'hits_unknown')).padStart(7)}\n`;
  s += `  total  ${String(total).padStart(7)}\n`;

  s += '\n=== cache hit rate (X-Cache) ===\n';
  s += `  ${'node'.padEnd(6)} ${'hit'.padStart(7)} ${'miss'.padStart(7)}  ${'rate'.padStart(6)}\n`;
  for (const n of NODES) {
    const t = hitCounts[n] + missCounts[n];
    const pct = t > 0 ? (hitCounts[n] / t) * 100 : 0;
    s += `  ${n.padEnd(6)} ${String(hitCounts[n]).padStart(7)} ${String(missCounts[n]).padStart(7)}  ${pct.toFixed(1).padStart(5)}%  ${'#'.repeat(Math.round(pct / 2))}\n`;
  }
  const totalSeen = totalHit + totalMiss;
  const totalPct = totalSeen > 0 ? (totalHit / totalSeen) * 100 : 0;
  s += `  ${'ALL'.padEnd(6)} ${String(totalHit).padStart(7)} ${String(totalMiss).padStart(7)}  ${totalPct.toFixed(1).padStart(5)}%\n`;
  const cu = count(data, 'cache_unknown');
  if (cu > 0) s += `  X-Cache が読めなかった応答: ${cu}\n`;

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

