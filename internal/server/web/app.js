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

  // -------- Charts --------
  const RANGE_MS = { '1h': 3600e3, '6h': 6 * 3600e3, '24h': 24 * 3600e3, '7d': 7 * 86400e3, '31d': 31 * 86400e3 };
  const ACCENT = { general: '#00d4ff', video: '#a855f7' };

  async function refreshCharts() {
    const models = [...state.models.keys()];
    if (models.length === 0) return;
    const from = Date.now() - RANGE_MS[state.range];
    const to = Date.now();
    for (const name of models) {
      const res = await fetch(`/api/history?model=${encodeURIComponent(name)}&range=${state.range}&bucket=auto`);
      if (!res.ok) continue;
      const data = await res.json();
      const chart = ensureChart(name);
      chart.setOption(buildOption(name, data.points || [], from, to), true);
    }
  }

  function ensureChart(name) {
    if (state.charts.has(name)) return state.charts.get(name);
    const root = $('charts');
    const card = document.createElement('div');
    card.className = 'chart-card';
    card.dataset.model = name;
    card.innerHTML = `<div class="chart-title">${name} · 区间剩余率 (${state.range})</div><div class="chart"></div>`;
    root.appendChild(card);
    const el = card.querySelector('.chart');
    const c = echarts.init(el, null, { renderer: 'canvas' });
    state.charts.set(name, c);
    new ResizeObserver(() => c.resize()).observe(el);
    return c;
  }

  function buildOption(name, points, from, to) {
    const accent = ACCENT[name] || '#00d4ff';
    const xs = points.map((p) => p.t);
    const mins = points.map((p) => p.min);
    const maxs = points.map((p) => p.max);
    const avgs = points.map((p) => +p.avg.toFixed(2));
    return {
      animation: true,
      animationDuration: 600,
      animationEasing: 'cubicOut',
      grid: { left: 48, right: 24, top: 24, bottom: 32 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: '#131836',
        borderColor: '#1f2547',
        textStyle: { color: '#e6e9f5' },
        formatter: (params) => {
          const p = params[0];
          const i = p.dataIndex;
          return `${new Date(xs[i]).toLocaleString()}<br/>` +
            `min ${mins[i].toFixed(1)}% · avg ${avgs[i]}% · max ${maxs[i].toFixed(1)}%`;
        },
      },
      xAxis: {
        type: 'time',
        min: from, max: to,
        axisLine: { lineStyle: { color: '#1f2547' } },
        axisLabel: { color: '#6b7390', fontSize: 11 },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value', min: 0, max: 100,
        axisLine: { show: false },
        axisLabel: { color: '#6b7390', fontSize: 11, formatter: '{value}%' },
        splitLine: { lineStyle: { color: 'rgba(31,37,71,0.5)' } },
      },
      series: [
        {
          name: 'min-max',
          type: 'line',
          data: xs.map((t, i) => [t, mins[i], maxs[i]]),
          lineStyle: { opacity: 0 },
          stack: 'minmax',
          symbol: 'none',
          areaStyle: { color: accent, opacity: 0.08 },
          smooth: true,
        },
        {
          name: 'avg',
          type: 'line',
          data: xs.map((t, i) => [t, avgs[i]]),
          lineStyle: { color: accent, width: 2 },
          itemStyle: { color: accent },
          areaStyle: { color: accent, opacity: 0.18 },
          symbol: 'none',
          smooth: true,
        },
      ],
    };
  }

  // re-render charts on range change
  document.querySelectorAll('.range-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      // (already wired above) – call refresh
      refreshCharts();
    });
  });

  // refresh charts periodically (every 30s) and after each WS snapshot (debounced)
  setInterval(refreshCharts, 30000);
  let chartRefreshTimer = null;
  const oldHandle = handleSnapshot;
  handleSnapshot = function (data) {
    oldHandle(data);
    clearTimeout(chartRefreshTimer);
    chartRefreshTimer = setTimeout(refreshCharts, 1500);
  };
})();
