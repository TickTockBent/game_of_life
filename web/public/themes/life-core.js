/* life-core.js — shared live renderer for theme previews.
 * A page calls LifeView.mount({ cell, owner, ... }) once; everything else
 * (WebSocket, layout, hover card, participants, theme toggle) is here.
 */
(function () {
  const SECTION = 7;      // cells per engine side
  const LAYOUT = 10;      // slots per layout side
  const THEME_KEY = 'gol-theme';

  function hashString(s) {
    let h = 2166136261;
    for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
    return h >>> 0;
  }

  function relTime(iso) {
    const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 60) return 'just now';
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    return `${Math.floor(s / 86400)} d ago`;
  }

  function ownerName(node) {
    return node.displayName || `engine ${node.podId.slice(0, 6)}`;
  }

  const LifeView = {
    mount(config) {
      const view = Object.assign({
        // hooks a theme may override
        // default: golden-angle spread by join order, so the first N engines are maximally distinct
        hue(node) { return Math.round((this.rank(node) * 137.508) % 360); },
        gap: 0.18,                 // fraction of cell size left empty
        shape: 'square',           // 'square' | 'dot' | 'rounded'
        minCell: 4,
        ringOfEmpty: 1,            // empty slots drawn around occupied bbox
      }, config);

      view.canvas = document.getElementById('life');
      view.ctx = view.canvas.getContext('2d');
      view.card = document.getElementById('card');
      view.state = { grids: {}, nodes: {} };
      view.genSamples = [];
      view.ranks = {};
      view.rank = function (node) { return view.ranks[node.podId] || 0; };
      view.hoverPos = null;

      setupTheme(view);
      setupResize(view);
      setupHover(view);
      connect(view);
      setInterval(() => renderParticipants(view), 5000);
      return view;
    }
  };

  // ---------- theme (light/dark) ----------
  function setupTheme(view) {
    const root = document.documentElement;
    let stored = null;
    try { stored = localStorage.getItem(THEME_KEY); } catch (e) {}
    const forced = new URLSearchParams(location.search).get('theme');
    if (forced === 'dark' || forced === 'light') stored = forced;
    if (stored) root.dataset.theme = stored;
    const btn = document.getElementById('themeToggle');
    if (btn) btn.addEventListener('click', () => {
      const next = currentTheme() === 'dark' ? 'light' : 'dark';
      root.dataset.theme = next;
      try { localStorage.setItem(THEME_KEY, next); } catch (e) {}
      readTokens(view); draw(view);
    });
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => { readTokens(view); draw(view); });
    readTokens(view);
  }
  function currentTheme() {
    const t = document.documentElement.dataset.theme;
    if (t) return t;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  function readTokens(view) {
    document.documentElement.classList.toggle('is-dark', currentTheme() === 'dark');
    const cs = getComputedStyle(document.documentElement);
    view.tokens = {
      dark: currentTheme() === 'dark',
      bg: cs.getPropertyValue('--canvas-bg').trim(),
      dead: cs.getPropertyValue('--cell-dead').trim(),
      empty: cs.getPropertyValue('--slot-empty').trim(),
      border: cs.getPropertyValue('--section-border').trim(),
      sat: parseFloat(cs.getPropertyValue('--owner-sat')) || 60,
      light: parseFloat(cs.getPropertyValue('--owner-light')) || 55,
    };
  }

  // ---------- network ----------
  function connect(view) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    const status = document.getElementById('conn');
    ws.onopen = () => { if (status) status.dataset.state = 'live'; };
    ws.onmessage = (ev) => {
      try { ingest(view, JSON.parse(ev.data)); } catch (e) { console.error(e); }
    };
    ws.onclose = () => { if (status) status.dataset.state = 'off'; setTimeout(() => connect(view), 2000); };
  }

  function ingest(view, data) {
    if (!data.grids || !data.topology) return;
    const prevGen = view.generation || 0;
    view.state.nodes = data.topology.nodes || {};
    // stable join-order rank used for colour assignment
    const ordered = Object.values(view.state.nodes).sort((a, b) =>
      (new Date(a.registeredAt) - new Date(b.registeredAt)) || (a.podId < b.podId ? -1 : 1));
    view.ranks = {}; ordered.forEach((n, i) => { view.ranks[n.podId] = i; });
    // keep last known grid for lagging engines so the section freezes instead of vanishing
    for (const pos in data.grids) view.state.grids[pos] = data.grids[pos];
    for (const pos in view.state.grids) if (!view.state.nodes[pos]) delete view.state.grids[pos];

    let gen = 0;
    for (const pos in view.state.grids) gen = Math.max(gen, view.state.grids[pos].generation || 0);
    view.generation = gen;
    const now = performance.now();
    if (gen > prevGen) view.genSamples.push([now, gen]);
    while (view.genSamples.length && now - view.genSamples[0][0] > 5000) view.genSamples.shift();

    layout(view);
    draw(view);
    renderStats(view);
    if (!view.participantsDirtyTimer) {
      view.participantsDirtyTimer = setTimeout(() => { view.participantsDirtyTimer = null; renderParticipants(view); }, 500);
    }
  }

  // ---------- layout ----------
  function layout(view) {
    const nodes = view.state.nodes;
    let minR = LAYOUT, maxR = -1, minC = LAYOUT, maxC = -1;
    for (const pos in nodes) {
      const { row, col } = nodes[pos].position;
      minR = Math.min(minR, row); maxR = Math.max(maxR, row);
      minC = Math.min(minC, col); maxC = Math.max(maxC, col);
    }
    if (maxR < 0) { minR = 4; maxR = 5; minC = 4; maxC = 5; }
    const ring = view.ringOfEmpty;
    minR = Math.max(0, minR - ring); minC = Math.max(0, minC - ring);
    maxR = Math.min(LAYOUT - 1, maxR + ring); maxC = Math.min(LAYOUT - 1, maxC + ring);
    view.box = { minR, maxR, minC, maxC, rows: maxR - minR + 1, cols: maxC - minC + 1 };
    fit(view);
  }

  function fit(view) {
    if (!view.box) return;
    const rect = view.canvas.parentElement.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const cellsW = view.box.cols * SECTION, cellsH = view.box.rows * SECTION;
    const cell = Math.max(view.minCell, Math.floor(Math.min(rect.width / cellsW, rect.height / cellsH)));
    view.cell = cell;
    view.cssW = cellsW * cell; view.cssH = cellsH * cell;
    view.canvas.style.width = view.cssW + 'px';
    view.canvas.style.height = view.cssH + 'px';
    view.canvas.width = Math.round(view.cssW * dpr);
    view.canvas.height = Math.round(view.cssH * dpr);
    view.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }

  function setupResize(view) {
    let t; window.addEventListener('resize', () => { clearTimeout(t); t = setTimeout(() => { fit(view); draw(view); }, 80); });
  }

  // ---------- drawing ----------
  function ownerColor(view, node, alpha) {
    const h = view.hue(node);
    const { sat, light } = view.tokens;
    return `hsla(${h}, ${sat}%, ${light}%, ${alpha == null ? 1 : alpha})`;
  }

  function draw(view) {
    if (!view.box) return;
    const ctx = view.ctx, t = view.tokens, cell = view.cell;
    const { minR, minC, rows, cols } = view.box;
    ctx.clearRect(0, 0, view.cssW, view.cssH);
    if (t.bg && t.bg !== 'transparent') { ctx.fillStyle = t.bg; ctx.fillRect(0, 0, view.cssW, view.cssH); }

    const gap = Math.max(cell > 6 ? 1 : 0, Math.round(cell * view.gap));
    const size = cell - gap;
    const sec = SECTION * cell;

    for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
      const pos = (r + minR) * LAYOUT + (c + minC);
      const node = view.state.nodes[pos];
      const x0 = c * sec, y0 = r * sec;
      if (!node) {
        if (t.empty) {
          ctx.save(); ctx.setLineDash([2, 4]); ctx.strokeStyle = t.empty; ctx.lineWidth = 1;
          ctx.strokeRect(x0 + 0.5 + gap, y0 + 0.5 + gap, sec - 1 - 2 * gap, sec - 1 - 2 * gap); ctx.restore();
        }
        continue;
      }
      const grid = (view.state.grids[pos] || {}).grid;
      const alpha = node.lagging ? 0.35 : 1;
      const alive = ownerColor(view, node, alpha);
      if (view.drawSection) view.drawSection(ctx, x0, y0, sec, node, view);
      for (let y = 0; y < SECTION; y++) for (let x = 0; x < SECTION; x++) {
        const on = grid && grid[y] && grid[y][x];
        const px = x0 + x * cell + gap / 2, py = y0 + y * cell + gap / 2;
        if (on) ctx.fillStyle = view.aliveStyle ? view.aliveStyle(ctx, node, alpha, view) : alive;
        else if (t.dead && t.dead !== 'transparent') ctx.fillStyle = t.dead;
        else continue;
        paintCell(ctx, px, py, size, view.shape, on);
      }
      if (t.border) {
        ctx.strokeStyle = view.borderStyle ? view.borderStyle(node, view) : t.border;
        ctx.lineWidth = 1; ctx.strokeRect(x0 + 0.5, y0 + 0.5, sec - 1, sec - 1);
      }
      if (view.hoverPos === pos && view.hoverStyle) view.hoverStyle(ctx, x0, y0, sec, node, view);
    }
  }

  function paintCell(ctx, x, y, s, shape, alive) {
    if (shape === 'dot') {
      ctx.beginPath(); ctx.arc(x + s / 2, y + s / 2, (alive ? s : s * 0.45) / 2, 0, Math.PI * 2); ctx.fill();
    } else if (shape === 'rounded') {
      const r = Math.max(1, s * 0.3);
      ctx.beginPath(); ctx.roundRect(x, y, s, s, r); ctx.fill();
    } else {
      ctx.fillRect(x, y, s, s);
    }
  }

  // ---------- hover card ----------
  function setupHover(view) {
    const cv = view.canvas;
    const move = (ev) => {
      if (!view.box) return;
      const rect = cv.getBoundingClientRect();
      const x = ev.clientX - rect.left, y = ev.clientY - rect.top;
      const sec = SECTION * view.cell;
      const c = Math.floor(x / sec), r = Math.floor(y / sec);
      const pos = (r + view.box.minR) * LAYOUT + (c + view.box.minC);
      const node = view.state.nodes[pos];
      if (!node) { hideCard(view); return; }
      if (view.hoverPos !== pos) { view.hoverPos = pos; draw(view); }
      showCard(view, node, pos, ev.clientX, ev.clientY);
    };
    cv.addEventListener('pointermove', move);
    cv.addEventListener('pointerleave', () => hideCard(view));
    cv.addEventListener('click', (ev) => {
      if (!view.box) return;
      const rect = cv.getBoundingClientRect();
      const x = Math.floor((ev.clientX - rect.left) / view.cell) + view.box.minC * SECTION;
      const y = Math.floor((ev.clientY - rect.top) / view.cell) + view.box.minR * SECTION;
      fetch('/api/click', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ globalX: x, globalY: y, alive: true }) }).catch(() => {});
    });
  }
  function showCard(view, node, pos, cx, cy) {
    const card = view.card; if (!card) return;
    const g = view.state.grids[pos] || {};
    card.innerHTML = `<b>${escapeHtml(ownerName(node))}</b>` +
      `<span class="sw" style="background:${ownerColor(view, node)}"></span>` +
      `<small>slot ${node.position.row},${node.position.col} · gen ${g.generation || 0}` +
      `${node.lagging ? ' · catching up' : ''} · joined ${relTime(node.registeredAt)}</small>`;
    card.hidden = false;
    const pad = 14, w = card.offsetWidth, h = card.offsetHeight;
    card.style.left = Math.min(cx + pad, window.innerWidth - w - 8) + 'px';
    card.style.top = Math.min(cy + pad, window.innerHeight - h - 8) + 'px';
  }
  function hideCard(view) {
    if (view.card) view.card.hidden = true;
    if (view.hoverPos !== null) { view.hoverPos = null; draw(view); }
  }

  // ---------- side panel ----------
  function renderStats(view) {
    const gen = document.getElementById('gen'); if (gen) gen.textContent = (view.generation || 0).toLocaleString();
    const rate = document.getElementById('rate');
    if (rate) {
      const s = view.genSamples;
      let v = 0;
      if (s.length > 1) v = (s[s.length - 1][1] - s[0][1]) / ((s[s.length - 1][0] - s[0][0]) / 1000);
      rate.textContent = v.toFixed(1);
    }
    const n = Object.keys(view.state.nodes).length;
    const engines = document.getElementById('engines'); if (engines) engines.textContent = n;
    const cells = document.getElementById('cells'); if (cells) cells.textContent = (n * SECTION * SECTION).toLocaleString();
  }

  function renderParticipants(view) {
    const list = document.getElementById('participants'); if (!list) return;
    const nodes = Object.entries(view.state.nodes)
      .sort((a, b) => new Date(a[1].registeredAt) - new Date(b[1].registeredAt));
    list.innerHTML = nodes.map(([pos, node]) =>
      `<li class="${node.lagging ? 'lag' : ''}"><span class="sw" style="background:${ownerColor(view, node)}"></span>` +
      `<span class="nm">${escapeHtml(ownerName(node))}</span>` +
      `<span class="ago">${node.lagging ? 'catching up' : relTime(node.registeredAt)}</span></li>`).join('') ||
      '<li class="none">No engines yet — the grid is waiting.</li>';
  }

  function escapeHtml(s) { return String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }

  window.LifeView = LifeView;
})();
