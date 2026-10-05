// The landing page for a game: the numbers you check first, and anything
// that currently needs attention. Everything here links onward.

async function renderOverview(host) {
  const id = state.gameId;
  const game = state.game;
  const [stats, logs, releases, retention, funnel] = await Promise.all([
    api('GET', `/admin/api/games/${id}/stats?days=30`),
    api('GET', `/admin/api/logs?game_id=${id}&level=error&limit=5`),
    api('GET', `/admin/api/games/${id}/releases`),
    api('GET', `/admin/api/games/${id}/stats/retention?days=30`),
    api('GET', `/admin/api/games/${id}/stats/funnel?days=30`),
  ]);

  const days = stats.days;
  const today = days[days.length - 1] || { active: 0, logins: 0, new_players: 0 };
  const yesterday = days[days.length - 2] || { active: 0 };
  const published = releases.releases.filter(r => r.status === 'published');
  const latest = published[0];

  pageShell(host, {
    title: game.name,
    subtitle: `App ID ${esc(game.app_id)} · 创建于 ${fmtTime(game.created_at)}`,
    actions: `<button class="btn text" onclick="route()">刷新</button>`,
    body: `
      <div class="tiles">
        ${tile('当前在线', fmtNum(stats.online), '实时连接的玩家')}
        ${tile('今日活跃', fmtNum(today.active), delta(today.active, yesterday.active))}
        ${tile('今日新增', fmtNum(today.new_players), '首次登录的玩家')}
        ${tile('今日登录', fmtNum(today.logins), '含重复登录')}
      </div>

      <div class="card">
        <div class="card-head">
          <h2>30 天活跃趋势</h2>
          <span class="dim">时区 ${esc(stats.timezone)}</span>
        </div>
        ${trendChart(days, 'active')}
      </div>

      ${retentionCard(retention.days || [])}
      ${funnelCard(funnel.steps || [], funnel.window)}

      <div class="split">
        <div class="card">
          <div class="card-head">
            <h2>最近错误</h2>
            <a onclick="go('logs')">全部日志</a>
          </div>
          ${logs.entries.length ? `<ul class="feed">${logs.entries.map(e => `
            <li>
              <span class="badge error">${esc(e.level)}</span>
              <div class="feed-main">
                <div class="feed-title">${esc(e.message)}</div>
                <div class="dim">${esc(e.source)}${e.event ? ' · ' + esc(e.event) : ''} · ${fmtAgo(e.created_at)}</div>
              </div>
            </li>`).join('')}</ul>`
            : emptyState('✓', '没有错误', '过去的日志里没有 error 级别记录。')}
        </div>

        <div class="card">
          <div class="card-head">
            <h2>当前版本</h2>
            <a onclick="go('releases')">版本管理</a>
          </div>
          ${latest ? `
            <div class="kv"><span>最新已发布</span><b>${esc(latest.version)}</b></div>
            <div class="kv"><span>渠道</span><b>${esc(latest.channel)}</b></div>
            <div class="kv"><span>灰度</span><b>${latest.rollout_percent}%</b></div>
            <div class="kv"><span>发布时间</span><b>${fmtAgo(latest.published_at)}</b></div>
            ${latest.mandatory ? '<div class="kv"><span>强制更新</span><b>是</b></div>' : ''}`
            : emptyState('📦', '尚未发布版本', '在版本管理里创建并发布第一个版本。')}
        </div>
      </div>`,
  });
}

// ---------- retention ----------

// A dN column is only filled once the cohort is old enough for the answer to
// be final (d1 after 2 days, d7 after 8, d30 after 31), so a null means "not
// yet known" and must not be drawn as 0%.
const RETENTION_SERIES = [
  ['d1', '次日留存', 'D1'],
  ['d7', '7 日留存', 'D7'],
  ['d30', '30 日留存', 'D30'],
];

function retentionRate(row, key) {
  if (!row.cohort || row[key] == null) return null;
  return row[key] / row.cohort;
}

// The headline number is the most recent cohort that has a final answer,
// which is more honest than averaging over half-finished cohorts.
function latestRate(rows, key) {
  for (let i = rows.length - 1; i >= 0; i--) {
    const rate = retentionRate(rows[i], key);
    if (rate != null) return { rate, row: rows[i] };
  }
  return null;
}

function retentionCard(rows) {
  if (!rows.length) {
    return `<div class="card">
      <div class="card-head"><h2>留存率</h2></div>
      ${emptyState('📈', '还没有留存数据', '留存按注册日分群,每小时计算一次;新游戏需要等第一批玩家满一天。')}
    </div>`;
  }
  return `<div class="card">
    <div class="card-head">
      <h2>留存率</h2>
      <span class="dim">按注册日分群 · 近 ${rows.length} 天</span>
    </div>
    <div class="tiles compact">
      ${RETENTION_SERIES.map(([key, label]) => {
        const latest = latestRate(rows, key);
        return tile(label,
          latest ? Math.round(latest.rate * 100) + '%' : '—',
          latest ? `${latest.row.date} 注册的 ${fmtNum(latest.row.cohort)} 人`
                 : '样本还不够,需要更多时间');
      }).join('')}
    </div>
    ${retentionChart(rows)}
  </div>`;
}

// One row of bars per cohort day, three series side by side. Cohorts whose
// answer is not final yet are rendered as a gap, not a zero-height bar.
function retentionChart(rows) {
  return `<div class="retention-chart">
      ${rows.map(row => `<div class="retention-day" title="${row.date}:注册 ${row.cohort} 人${
          RETENTION_SERIES.map(([key, , short]) => {
            const rate = retentionRate(row, key);
            return rate == null ? '' : ` · ${short} ${Math.round(rate * 100)}%`;
          }).join('')}">
        ${RETENTION_SERIES.map(([key]) => {
          const rate = retentionRate(row, key);
          return rate == null
            ? `<div class="retention-bar pending"></div>`
            : `<div class="retention-bar ${key}" style="height:${Math.max(2, Math.round(rate * 100))}%"></div>`;
        }).join('')}
      </div>`).join('')}
    </div>
    <div class="chart-axis">
      <span>${rows[0]?.date || ''}</span>
      <span>${RETENTION_SERIES.map(([key, label]) =>
        `<span class="legend"><i class="swatch ${key}"></i>${label}</span>`).join(' ')}</span>
      <span>${rows[rows.length - 1]?.date || ''}</span>
    </div>`;
}

// ---------- registration funnel ----------

const FUNNEL_LABEL = {
  registered: '注册',
  first_login: '首次登录',
  returned_7d: '7 天内再次登录',
};

function funnelCard(steps, windowDays) {
  const top = steps[0]?.count || 0;
  if (!top) {
    return `<div class="card">
      <div class="card-head"><h2>注册漏斗</h2></div>
      ${emptyState('🪣', '还没有漏斗数据', '漏斗只统计已满 7 天、结果已确定的注册分群。')}
    </div>`;
  }
  return `<div class="card">
    <div class="card-head">
      <h2>注册漏斗</h2>
      <span class="dim">近 ${windowDays || 30} 天已满 7 天的分群</span>
    </div>
    <div class="funnel">
      ${steps.map((s, i) => {
        const share = s.count / top;
        const prev = i ? steps[i - 1].count : s.count;
        return `<div class="funnel-step">
          <div class="funnel-head">
            <span>${esc(FUNNEL_LABEL[s.name] || s.name)}</span>
            <b>${fmtNum(s.count)} <span class="dim">${Math.round(share * 100)}%</span></b>
          </div>
          <div class="funnel-track">
            <div class="funnel-fill" style="width:${Math.max(1, share * 100)}%"></div>
          </div>
          ${i ? `<div class="dim funnel-drop">较上一步 ${pct(s.count, prev)}</div>` : ''}
        </div>`;
      }).join('')}
    </div>
  </div>`;
}
