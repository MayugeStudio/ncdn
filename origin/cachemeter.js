(function () {
  'use strict';

  const fmtMs = (v) => (v < 10 ? v.toFixed(1) : Math.round(v).toString()) + 'ms';
  const pct = (v) => (v * 100).toFixed(1) + '%';

  class CacheMeter {
    constructor(root) {
      this.root = root;
      this.urls = JSON.parse(root.dataset.targets || '[]');
      this.running = false;
      this.reset();
      this.build();
    }

    reset() {
      this.hit = 0;
      this.miss = 0;
      this.unknown = 0;
      this.error = 0;
      this.bytes = 0;
      this.hitMs = [];
      this.missMs = [];
      this.nodes = new Map();
    }

    build() {
      this.root.innerHTML = `
        <div class="cm-controls">
          <label>リクエスト数 <input class="cm-count" type="number" min="1" max="5000" value="${Math.min(this.urls.length, 200)}"></label>
          <label>同時実行 <input class="cm-conc" type="number" min="1" max="32" value="6"></label>
          <label>アクセス分布
            <select class="cm-dist">
              <option value="sequential">順番に一巡</option>
              <option value="zipf">Zipf（人気に偏る）</option>
              <option value="uniform">ランダム（一様）</option>
            </select>
          </label>
          <button class="cm-run" type="button">計測する</button>
          <button class="cm-clear" type="button">リセット</button>
        </div>
        <div class="cm-bar"><span class="cm-bar-hit"></span></div>
        <div class="cm-grid">
          <div class="cm-stat"><b class="cm-rate">–</b><span>ヒット率</span></div>
          <div class="cm-stat"><b class="cm-hit">0</b><span>Hit</span></div>
          <div class="cm-stat"><b class="cm-miss">0</b><span>Miss</span></div>
          <div class="cm-stat"><b class="cm-hitms">–</b><span>Hit 平均</span></div>
          <div class="cm-stat"><b class="cm-missms">–</b><span>Miss 平均</span></div>
          <div class="cm-stat"><b class="cm-bytes">0</b><span>転送量</span></div>
        </div>
        <p class="cm-nodes"></p>
        <p class="cm-note">対象URL: <b>${this.urls.length}</b> 件</p>
      `;

      this.el = {
        count: this.root.querySelector('.cm-count'),
        conc: this.root.querySelector('.cm-conc'),
        dist: this.root.querySelector('.cm-dist'),
        run: this.root.querySelector('.cm-run'),
        clear: this.root.querySelector('.cm-clear'),
        barHit: this.root.querySelector('.cm-bar-hit'),
        rate: this.root.querySelector('.cm-rate'),
        hit: this.root.querySelector('.cm-hit'),
        miss: this.root.querySelector('.cm-miss'),
        hitms: this.root.querySelector('.cm-hitms'),
        missms: this.root.querySelector('.cm-missms'),
        bytes: this.root.querySelector('.cm-bytes'),
        nodes: this.root.querySelector('.cm-nodes'),
      };

      this.el.run.addEventListener('click', () => this.run());
      this.el.clear.addEventListener('click', () => {
        this.reset();
        this.paint();
      });
    }

    pick(i, total, dist) {
      const n = this.urls.length;
      if (dist === 'sequential') return this.urls[i % n];
      if (dist === 'uniform') return this.urls[Math.floor(Math.random() * n)];

      const r = Math.random();
      const idx = Math.min(n - 1, Math.floor(Math.pow(n + 1, r) - 1));
      return this.urls[idx];
    }

    async one(url) {
      const started = performance.now();
      try {
        const res = await fetch(url, { cache: 'no-store' });
        const buf = await res.arrayBuffer();
        const elapsed = performance.now() - started;

        this.bytes += buf.byteLength;

        const node = res.headers.get('X-NCDN-PoPCache-NodeId');
        if (node) this.nodes.set(node, (this.nodes.get(node) || 0) + 1);

        const state = res.headers.get('X-Cache');
        if (state === 'Hit') {
          this.hit++;
          this.hitMs.push(elapsed);
        } else if (state === 'Miss') {
          this.miss++;
          this.missMs.push(elapsed);
        } else {
          this.unknown++;
        }
      } catch (e) {
        this.error++;
      }
    }

    async run() {
      if (this.running || this.urls.length === 0) return;
      this.running = true;
      this.el.run.disabled = true;
      this.el.run.textContent = '計測中…';

      const total = Math.max(1, parseInt(this.el.count.value, 10) || 1);
      const conc = Math.max(1, parseInt(this.el.conc.value, 10) || 1);
      const dist = this.el.dist.value;

      let issued = 0;
      const worker = async () => {
        while (issued < total) {
          const i = issued++;
          await this.one(this.pick(i, total, dist));
          if (i % 10 === 0) this.paint();
        }
      };

      await Promise.all(Array.from({ length: conc }, worker));

      this.paint();
      this.running = false;
      this.el.run.disabled = false;
      this.el.run.textContent = '計測する';
    }

    paint() {
      const done = this.hit + this.miss + this.unknown;
      const rate = this.hit + this.miss > 0 ? this.hit / (this.hit + this.miss) : 0;
      const avg = (a) => (a.length ? fmtMs(a.reduce((x, y) => x + y, 0) / a.length) : '–');

      this.el.barHit.style.width = pct(rate);
      this.el.rate.textContent = this.hit + this.miss > 0 ? pct(rate) : '–';
      this.el.hit.textContent = this.hit;
      this.el.miss.textContent = this.miss;
      this.el.hitms.textContent = avg(this.hitMs);
      this.el.missms.textContent = avg(this.missMs);
      this.el.bytes.textContent = (this.bytes / 1024 / 1024).toFixed(2) + ' MB';

      const parts = [];
      for (const [node, n] of this.nodes) parts.push(`${node}: ${n}`);
      if (this.unknown) parts.push(`X-Cache なし: ${this.unknown}（PoPCache を経由していません）`);
      if (this.error) parts.push(`エラー: ${this.error}`);
      this.el.nodes.textContent = parts.join(' / ');
    }
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('[data-cachemeter]').forEach((el) => new CacheMeter(el));

    const nav = performance.getEntriesByType('navigation')[0];
    if (nav) {
      const set = (id, v) => {
        const el = document.getElementById(id);
        if (el) el.textContent = v;
      };
      set('resolv-dur', fmtMs(nav.domainLookupEnd - nav.domainLookupStart));
      set('conn-dur', fmtMs(nav.connectEnd - nav.connectStart));
      set('req-dur', fmtMs(nav.responseStart - nav.requestStart));
      set('res-dur', fmtMs(nav.responseEnd - nav.responseStart));
    }
  });
})();
