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
    if (typeof data.consec_errors === 'number') {
      $('errCount').textContent = data.consec_errors;
    }
  }

  // -------- Cards --------
  const statusText = (s) => s === 1 ? '活跃' : s === 3 ? '未活动' : '--';
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
      // 已消耗 = 100% - 剩余
      const remainPct = m.interval_remaining_pct ?? 0;
      const consumed = Math.max(0, Math.min(100, 100 - remainPct));
      const remainWeekly = m.weekly_remaining_pct;
      const consumedWeekly = (remainWeekly == null) ? null : Math.max(0, Math.min(100, 100 - remainWeekly));
      const remainsMs = m.interval_remains_ms ?? 0;
      const iStatus = statusText(m.interval_status);
      const wStatus = statusText(m.weekly_status);
      card.innerHTML = `
        <h3>${name} <span class="pct-kind">已用</span></h3>
        <div class="pct" data-target="${consumed}">${consumed}%</div>
        <div class="bar"><div class="bar-fill" style="width:${consumed}%"></div></div>
        <div class="meta"><span>区间</span><b>${formatRangeMs(remainsMs)} · ${iStatus}</b></div>
        <div class="meta"><span>本周</span><b>${consumedWeekly == null ? '--' : consumedWeekly}% · ${wStatus}</b></div>
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

  // -------- Settings modal --------
  const modal = $('modal');
  const keyStatus = $('keyStatus');
  const keyInputRow = $('keyInputRow');
  const keyError = $('keyError');
  const keySaveBtn = $('keySaveBtn');
  const keyCancelBtn = $('keyCancelBtn');
  const keyChangeBtn = $('keyChangeBtn');

  let keyConfigured = false;
  let saving = false;

  function setKeyUI() {
    if (keyConfigured) {
      keyStatus.textContent = '已配置 ✓';
      keyStatus.className = 'badge ok';
      keyInputRow.classList.add('hidden');
      keyChangeBtn.classList.remove('hidden');
      keySaveBtn.classList.add('hidden');
      keyCancelBtn.classList.add('hidden');
    } else {
      keyStatus.textContent = '● 未配置';
      keyStatus.className = 'badge';
      keyInputRow.classList.remove('hidden');
      keyChangeBtn.classList.add('hidden');
      keySaveBtn.classList.remove('hidden');
      keyCancelBtn.classList.remove('hidden');
    }
  }

  function openModal() {
    setKeyUI();
    keyError.classList.add('hidden');
    keyError.textContent = '';
    $('keyInput').value = '';
    modal.classList.remove('hidden');
    if (!keyConfigured) setTimeout(() => $('keyInput').focus(), 100);
  }
  function closeModal() {
    if (saving) return;
    modal.classList.add('hidden');
  }

  $('settingsBtn').addEventListener('click', openModal);
  if ($('emptySettingsBtn')) $('emptySettingsBtn').addEventListener('click', openModal);
  $('modal').querySelector('.modal-backdrop').addEventListener('click', closeModal);
  keyCancelBtn.addEventListener('click', closeModal);
  keyChangeBtn.addEventListener('click', () => {
    keyConfigured = false;
    setKeyUI();
    setTimeout(() => $('keyInput').focus(), 100);
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
  });

  keySaveBtn.addEventListener('click', async () => {
    const v = $('keyInput').value.trim();
    if (!v) { showKeyError('请输入 API Key'); return; }
    saving = true;
    keySaveBtn.disabled = true;
    keySaveBtn.textContent = '验证中…';
    keyError.classList.add('hidden');
    try {
      const res = await fetch('/api/settings/key', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ api_key: v }),
      });
      if (!res.ok) {
        const t = await res.text();
        let msg = t;
        try { msg = JSON.parse(t).error || t; } catch (_) {}
        showKeyError(msg);
        return;
      }
      keyConfigured = true;
      setKeyUI();
      // hide empty state if shown
      $('emptyState').classList.add('hidden');
      // refresh status (server should be picking up new key on next tick)
    } catch (e) {
      showKeyError(e.message);
    } finally {
      saving = false;
      keySaveBtn.disabled = false;
      keySaveBtn.textContent = '保存并验证';
    }
  });

  function showKeyError(msg) {
    keyError.textContent = msg;
    keyError.classList.remove('hidden');
  }

  // initial key state from /api/status (also consolidated with Task 14 init call)
  fetch('/api/status').then((r) => r.json()).then((s) => {
    keyConfigured = !!s.keyring_configured;
    if (keyConfigured) $('emptyState').classList.add('hidden');
    else $('emptyState').classList.remove('hidden');
    if (typeof s.consec_errors === 'number') {
      $('errCount').textContent = s.consec_errors;
    }
    if (s.db_size_mb) { /* shown elsewhere if needed */ }
  }).catch(() => {});
  connect();

  // -------- Error count refresh --------
  setInterval(() => {
    fetch('/api/status').then((r) => r.json()).then((s) => {
      if (typeof s.consec_errors === 'number') {
        $('errCount').textContent = s.consec_errors;
      }
    }).catch(() => {});
  }, 5000);

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
    card.innerHTML = `<div class="chart-title">${name} · 消耗率 (${state.range})</div><div class="chart"></div>`;
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
    // 反转剩余 -> 已消耗(100% - remaining)
    const iMin = points.map((p) => +(100 - p.interval_max).toFixed(2));
    const iMax = points.map((p) => +(100 - p.interval_min).toFixed(2));
    const iAvg = points.map((p) => +(100 - p.interval_avg).toFixed(2));
    const wAvg = points.map((p) => +(100 - p.weekly_avg).toFixed(2));
    return {
      animation: true,
      animationDuration: 600,
      animationEasing: 'cubicOut',
      legend: {
        data: ['区间消耗', '本周消耗'],
        textStyle: { color: '#6b7390', fontSize: 11 },
        top: 0, right: 8,
        itemWidth: 14, itemHeight: 8,
      },
      grid: { left: 48, right: 24, top: 36, bottom: 32 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: '#131836',
        borderColor: '#1f2547',
        textStyle: { color: '#e6e9f5' },
        formatter: (params) => {
          const p = params[0];
          const i = p.dataIndex;
          return `${new Date(xs[i]).toLocaleString()}<br/>` +
            `<b style="color:${accent}">区间消耗</b> min ${iMin[i].toFixed(1)}% · avg ${iAvg[i]}% · max ${iMax[i].toFixed(1)}%<br/>` +
            `<b style="color:#a855f7">本周消耗</b> avg ${wAvg[i]}%`;
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
          name: '区间消耗',
          type: 'line',
          data: xs.map((t, i) => [t, iMin[i], iMax[i]]),
          lineStyle: { opacity: 0 },
          stack: 'iminmax',
          symbol: 'none',
          areaStyle: { color: accent, opacity: 0.08 },
          smooth: true,
        },
        {
          name: '区间消耗',
          type: 'line',
          data: xs.map((t, i) => [t, iAvg[i]]),
          lineStyle: { color: accent, width: 2 },
          itemStyle: { color: accent },
          areaStyle: { color: accent, opacity: 0.18 },
          symbol: 'none',
          smooth: true,
        },
        {
          name: '本周消耗',
          type: 'line',
          data: xs.map((t, i) => [t, wAvg[i]]),
          lineStyle: { color: '#a855f7', width: 1.5, type: 'dashed' },
          itemStyle: { color: '#a855f7' },
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
