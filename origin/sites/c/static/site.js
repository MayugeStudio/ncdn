(function () {
  'use strict';

  document.addEventListener('DOMContentLoaded', () => {
    const tbody = document.querySelector('.probe-log tbody');
    if (!tbody) return;

    document.querySelectorAll('[data-probe]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const url = btn.dataset.probe;
        const started = performance.now();

        let state = '-';
        let pop = '-';
        let head = '';

        try {
          const res = await fetch(url, { cache: 'no-store' });
          state = res.headers.get('X-Cache') || '(ヘッダなし)';
          pop = res.headers.get('X-NCDN-PoPCache-NodeId') || '(直接)';

          const type = res.headers.get('Content-Type') || '';
          if (type.startsWith('image/')) {
            const buf = await res.arrayBuffer();
            head = `${type} ${buf.byteLength} bytes`;
          } else {
            head = (await res.text()).replace(/\s+/g, ' ').slice(0, 70);
          }
        } catch (e) {
          state = 'ERROR';
          head = String(e);
        }

        const elapsed = Math.round(performance.now() - started);
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td><code>${url}</code></td>
          <td class="state ${state === 'Hit' ? 'is-hit' : state === 'Miss' ? 'is-miss' : ''}">${state}</td>
          <td>${pop}</td>
          <td>${head} <small>(${elapsed}ms)</small></td>
        `;
        tbody.prepend(tr);

        while (tbody.children.length > 12) tbody.lastElementChild.remove();
      });
    });
  });
})();
