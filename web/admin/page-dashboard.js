// The landing page. Opening the console should answer "how is the platform
// doing right now", not show a setup form — so this is data first: platform
// totals, an adjustable activity trend, retention, per-game breakdown, and
// the platform's own health.

// Chart selection persists across navigation so a chosen view sticks.
const chartView = { metric: 'active', days: 30 };
const METRICS = {
  active:      { label: '活跃玩家', key: 'active' },
  new_players: { label: '新增玩家', key: 'new_players' },
  logins:      { label: '登录次数', key: 'logins' },
};
const RANGES = [7, 30, 90];

async function renderDashboard(host) {
  const [stats, errors, health] = await Promise.all([
    api('GET', `/admin/api/stats?days=${chartView.days}`),
    api('GET', '/admin/api/logs?level=error&limit=6'),
    hasRole('owner') ? api('GET', '/admin/api/health') : Promise.resolve(null),
  ]);

  const days = stats.days;
  const yesterday = days[days.length - 2] || { active: 0, logins: 0, new_players: 0 };
  const t = stats.today;
  const r = stats.retention;

  pageShell(host, {
    title: '仪表盘',
    subtitle: `${esc(state.workspace?.name || '当前工作区')} · ${stats.totals.games} 个游戏 · 时区 ${esc(stats.timezone)}`,
    actions: `<button class="btn text" onclick="route()">刷新</button>`,
    body: `
      ${securityBanner()}
      <div class="tiles">
        ${tile('当前在线', fmtNum(stats.totals.online), '所有游戏的实时连接')}
        ${tile('今日活跃', fmtNum(t.active), delta(t.active, yesterday.active))}
        ${tile('今日新增', fmtNum(t.new_players), delta(t.new_players, yesterday.new_players))}
        ${tile('次日留存', pct(r.d1_back, r.d1_cohort),
               r.d1_cohort ? `昨日新增 ${r.d1_cohort} 人,今日回访 ${r.d1_back} 人` : '昨日没有新增玩家')}
      </div>

      <div class="card">
        <div class="card-head">
          <h2>${METRICS[chartView.metric].label}趋势</h2>
          <div class="row" style="gap:8px">
            <div class="chips" style="margin:0">
              ${Object.entries(METRICS).map(([k, m]) => `<button class="chip ${chartView.metric === k ? 'on' : ''}"
                 onclick="setChart('metric','${k}')">${m.label}</button>`).join('')}
            </div>
            <div class="chips" style="margin:0">
              ${RANGES.map(d => `<button class="chip ${chartView.days === d ? 'on' : ''}"
                 onclick="setChart('days',${d})">${d} 天</button>`).join('')}
            </div>
          </div>
        </div>
        ${trendChart(days, METRICS[chartView.metric].key)}
        ${periodSummary(days, METRICS[chartView.metric].key)}
      </div>

      <div class="split">
        <div class="card table-card">
          <div class="card-head">
            <h2>各游戏今日表现</h2>
            <a onclick="go('games', null)">管理游戏</a>
          </div>
          ${stats.games.length ? `<table>
            <thead><tr><th>游戏</th><th style="width:110px">7 日走势</th>
              <th style="text-align:right">在线</th><th style="text-align:right">活跃</th>
              <th style="text-align:right">新增</th></tr></thead>
            <tbody>${stats.games.map(g => `<tr class="clickable" onclick="go('overview','${g.id}')">
              <td><b>${esc(g.name)}</b></td>
              <td>${sparkline(g.trend)}</td>
              <td style="text-align:right">${g.online ? `<span class="badge ok">${g.online}</span>` : '0'}</td>
              <td style="text-align:right">${fmtNum(g.active)}</td>
              <td style="text-align:right">${fmtNum(g.new_players)}</td>
            </tr>`).join('')}</tbody></table>`
            : emptyState('🎮', '还没有游戏', '创建第一个游戏后,这里会显示它的实时数据。')}
        </div>

        <div class="card">
          <div class="card-head">
            <h2>最近错误</h2>
            <a onclick="go('logs', null)">全部日志</a>
          </div>
          ${errors.entries.length ? `<ul class="feed">${errors.entries.map(e => `
            <li>
              <span class="badge error">${esc(e.source)}</span>
              <div class="feed-main">
                <div class="feed-title">${esc(e.message)}</div>
                <div class="dim">${gameName(e.game_id)}${e.event ? ' · ' + esc(e.event) : ''} · ${fmtAgo(e.created_at)}</div>
              </div>
            </li>`).join('')}</ul>`
            : emptyState('✓', '一切正常', '最近没有 error 级别的日志。')}
        </div>
      </div>

      ${health ? healthCard(health, stats, r) : ''}`,
  });
}

function setChart(key, value) {
  chartView[key] = value;
  route();
}

// ---------- platform health ----------
function healthCard(h, stats, r) {
  const hookRate = h.webhook_total ? pct(h.webhook_ok, h.webhook_total) : '—';
  const dep = d => d.ok
    ? `<span class="badge ok">正常 ${d.latency_ms.toFixed(1)}ms</span>`
    : `<span class="badge error">异常</span> <span class="dim">${esc(d.error || '')}</span>`;
  return `<div class="card">
    <div class="card-head">
      <h2>平台状态</h2>
      <span class="dim">
        ${esc(h.version || 'dev')}${h.env && h.env !== 'production' ? ` <span class="badge">${esc(h.env)}</span>` : ''}
        · 已运行 ${fmtDuration(h.uptime_seconds)}
      </span>
    </div>
    <div class="health">
      <div class="health-col">
        <div class="kv"><span>PostgreSQL</span>${dep(h.postgres)}</div>
        <div class="kv"><span>Redis</span>${dep(h.redis)}</div>
        <div class="kv"><span>WebHook 投递成功率</span>
          <b>${hookRate}${h.webhook_total ? ` <span class="dim">(${fmtNum(h.webhook_total)} 次)</span>` : ''}</b></div>
      </div>
      <div class="health-col">
        <div class="kv"><span>24 小时云函数报错</span>
          <b class="${h.function_errors_24h ? 'bad' : ''}">${fmtNum(h.function_errors_24h)}</b></div>
        <div class="kv"><span>24 小时接口 5xx</span>
          <b class="${h.http_errors_24h ? 'bad' : ''}">${fmtNum(h.http_errors_24h)}</b></div>
        <div class="kv"><span>7 日回访率</span>
          <b title="7 天前新增的玩家中之后再登录过的比例">${pct(r.d7_back, r.d7_cohort)}</b></div>
      </div>
      <div class="health-col">
        <div class="kv"><span>产物存储</span><b>${fmtBytes(h.artifact_bytes)}</b></div>
        <div class="kv"><span>日志条数</span><b>${fmtNum(h.log_rows)}</b></div>
        <div class="kv"><span>聊天消息</span><b>${fmtNum(h.chat_rows)}</b></div>
      </div>
    </div>
  </div>`;
}

function fmtDuration(secs) {
  if (secs < 60) return `${secs} 秒`;
  if (secs < 3600) return `${Math.floor(secs / 60)} 分钟`;
  if (secs < 86400) return `${Math.floor(secs / 3600)} 小时 ${Math.floor((secs % 3600) / 60)} 分`;
  return `${Math.floor(secs / 86400)} 天 ${Math.floor((secs % 86400) / 3600)} 小时`;
}

function gameName(id) {
  const g = state.games.find(x => x.id === id);
  return g ? esc(g.name) : '平台';
}
