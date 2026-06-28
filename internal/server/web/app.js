(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const fmtTime = (ms) => {
    const d = new Date(ms);
    return d.toLocaleTimeString();
  };
  const fmtAgo = (ms) => {
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    return Math.floor(s / 3600) + 'h';
  };
  const formatRangeMs = (ms) => {
    const s = Math.round(ms / 1000);
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    return Math.floor(s / 3600) + 'h ' + (s % 3600 ? Math.floor((s % 3600) / 60) + 'm' : '');
  };

  const state = {
    models: new Map(),   // name -> snapshot
    range: '24h',
    ws: null,
    backoff: 1000,
    charts: new Map(),   // name -> echarts instance
  };

  // -------- WebSocket --------
  function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${location.host}/api/ws`;
    const ws = new WebSocket(url);
    state.ws = ws;
    ws.onopen = () => {
      state.backoff = 1000;
      $('wsStatus').className = 'ok';
      $('wsStatus').textContent = '●';
      $('wsDot').className = 'dot ok';
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === 'snapshot') handleSnapshot(msg.data);
      } catch (e) { console.error('ws parse', e); }
    };
    ws.onclose = () => {
      $('wsStatus').className = 'err';
      $('wsStatus').textContent = '●';
      $('wsDot').className = 'dot err';
      setTimeout(connect, state.backoff);
      state.backoff = Math.min(state.backoff * 2, 30000);
    };
    ws.onerror = () => { try { ws.close(); } catch (_) {} };
  }

  function handleSnapshot(data) {
    const list = data.models || [];
    const fetchedAt = data.fetched_at || Date.now();
    list.forEach((m) => state.models.set(m.model_name, { ...m, fetched_at: fetchedAt }));
    renderCards();
    $('lastUpdate').textContent = fmtTime(fetchedAt);
    $('fetchAgo').textContent = fmtAgo(fetchedAt);
  }

  // -------- Cards --------
  function renderCards() {
    const root = $('cards');
    if (state.models.size === 0) {
      if (!root.querySelector('.skeleton')) {
        root.innerHTML = '<div class="card skeleton" style="height:160px"></div>'.repeat(2);
      }
      return;
    }
    root.innerHTML = '';
    [...state.models.keys()].sort().forEach((name) => {
      const m = state.models.get(name);
      const card = document.createElement('div');
      card.className = 'card';
      card.dataset.model = name;
      const pct = m.interval_remaining_pct ?? 0;
      const weekly = m.weekly_remaining_pct;
      const remainsMs = m.interval_remains_ms ?? 0;
      card.innerHTML = `
        <h3>${name}</h3>
        <div class="pct" data-target="${pct}">${pct}%</div>
        <div class="bar"><div class="bar-fill" style="width:${pct}%"></div></div>
        <div class="meta"><span>区间剩余</span><b>${formatRangeMs(remainsMs)}</b></div>
        <div class="meta"><span>本周剩余</span><b>${weekly ?? '--'}%</b></div>
      `;
      root.appendChild(card);
    });
  }

  // -------- Status bar refresh --------
  setInterval(() => {
    const last = [...state.models.values()].map((m) => m.fetched_at).sort().pop();
    if (last) $('fetchAgo').textContent = fmtAgo(last);
  }, 1000);

  // -------- Range buttons --------
  document.querySelectorAll('.range-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.range-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      state.range = btn.dataset.range;
      // charts will pick this up in Task 15
    });
  });

  // -------- Settings modal (skeleton: open/close only) --------
  const modal = $('modal');
  const openModal = () => modal.classList.remove('hidden');
  const closeModal = () => modal.classList.add('hidden');
  $('settingsBtn').addEventListener('click', openModal);
  const emptyBtn = $('emptySettingsBtn');
  if (emptyBtn) emptyBtn.addEventListener('click', openModal);
  $('modal').querySelector('.modal-backdrop').addEventListener('click', closeModal);
  $('keyCancelBtn').addEventListener('click', closeModal);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
  });

  // -------- Init --------
  fetch('/api/status').then((r) => r.json()).then((s) => {
    if (s.keyring_configured) $('emptyState').classList.add('hidden');
    else $('emptyState').classList.remove('hidden');
    if (s.db_size_mb) { /* shown elsewhere if needed */ }
  }).catch(() => {});
  connect();
})();
