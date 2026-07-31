import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const TARGET = __ENV.TARGET || 'http://192.0.2.10:8889/statusz';
const NODES = (__ENV.NODES || 'C0,C1').split(',');
const SLEEP = Number(__ENV.SLEEP || 1);

const hits = {};
for (const n of NODES) {
  hits[n] = new Counter(`hits_${n}`);
}
const hitsUnknown = new Counter('hits_unknown');

export const options = {
  vus: 10,
  duration: '10s',
  noConnectionReuse: true,
};

export default function () {
  const res = http.get(TARGET, { tags: { name: 'statusz' } });

  if (!check(res, { 'status is 200': (r) => r.status === 200 })) {
    hitsUnknown.add(1, { node: 'error' });
    if (SLEEP > 0) sleep(SLEEP);
    return;
  }

  let id = null;
  try {
    const body = res.json();
    id = body.id || body.PopCacheId || null;
  } catch (e) {
    // JSON でなければ無視する
  }

  if (id !== null && hits[id] !== undefined) {
    hits[id].add(1, { node: id });
  } else {
    hitsUnknown.add(1, { node: String(id) });
  }

  if (SLEEP > 0) sleep(SLEEP);
}