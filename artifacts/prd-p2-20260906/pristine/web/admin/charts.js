// Shared visualisation helpers: KPI tiles, day-over-day deltas, the bar
// chart and the inline sparkline. Both the platform dashboard and a game's
// overview draw these, so they live in one place — two copies with different
// signatures would silently overwrite each other.

// ---------- tiles ----------
function tile(label, value, hint) {
  return `<div class="tile">
    <div class="tile-label">${esc(label)}</div>
    <div class="tile-value">${value}</div>
    <div class="tile-hint">${hint}</div>
  </div>`;
}

function pct(part, whole) {
  if (!whole) return '—';
  return Math.round((part / whole) * 100) + '%';
}

// delta renders the day-over-day change, since a bare number says nothing
// about whether things are getting better or worse.
function delta(now, prev) {
  if (!prev) return '较昨日 —';
  const p = Math.round(((now - prev) / prev) * 100);
  if (p === 0) return '与昨日持平';
  return `<span class="${p > 0 ? 'up' : 'down'}">${p > 0 ? '▲' : '▼'} ${Math.abs(p)}%</span> 较昨日`;
}

// ---------- charts ----------
function trendChart(days, key) {
  const max = Math.max(1, ...days.map(d => d[key]));
  return `<div class="chart">
    ${days.map(d => `
      <div class="chart-col" title="${d.date}:活跃 ${d.active} · 登录 ${d.logins} · 新增 ${d.new_players}">
        <div class="chart-bar" style="height:${Math.max(2, Math.round(d[key] / max * 100))}%"></div>
      </div>`).join('')}
  </div>
  <div class="chart-axis">
    <span>${days[0]?.date || ''}</span>
    <span>峰值 ${fmtNum(max)}</span>
    <span>${days[days.length - 1]?.date || ''}</span>
  </div>`;
}

// periodSummary compares this window against the one before it, which is what
// makes a trend chart actionable rather than decorative.
function periodSummary(days, key) {
  const half = Math.floor(days.length / 2);
  const recent = days.slice(half).reduce((a, d) => a + d[key], 0);
  const before = days.slice(0, half).reduce((a, d) => a + d[key], 0);
  const avg = Math.round(recent / Math.max(1, days.length - half));
  let cmp = '与前一段持平';
  if (before) {
    const p = Math.round(((recent - before) / before) * 100);
    if (p !== 0) cmp = `<span class="${p > 0 ? 'up' : 'down'}">${p > 0 ? '▲' : '▼'} ${Math.abs(p)}%</span> 对比前一段`;
  }
  return `<div class="chart-summary">
    <span>后半段合计 <b>${fmtNum(recent)}</b></span>
    <span>日均 <b>${fmtNum(avg)}</b></span>
    <span>${cmp}</span>
  </div>`;
}

// sparkline is an inline SVG: a 7-point shape reads at a glance in a table
// cell, where a full chart would not fit.
function sparkline(values) {
  if (!values?.length) return '<span class="dim">—</span>';
  const max = Math.max(1, ...values);
  const w = 96, h = 24, step = w / Math.max(1, values.length - 1);
  const pts = values.map((v, i) => `${(i * step).toFixed(1)},${(h - (v / max) * (h - 3) - 1.5).toFixed(1)}`);
  return `<svg class="spark" viewBox="0 0 ${w} ${h}" preserveAspectRatio="none">
    <polyline points="${pts.join(' ')}"/>
    <circle cx="${pts[pts.length - 1].split(',')[0]}" cy="${pts[pts.length - 1].split(',')[1]}" r="2"/>
  </svg>`;
}
