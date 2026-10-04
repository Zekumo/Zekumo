// The landing page for a game: the numbers you check first, and anything
// that currently needs attention. Everything here links onward.

async function renderOverview(host) {
  const id = state.gameId;
  const [stats, logs, releases] = await Promise.all([
    api('GET', `/admin/api/games/${id}/stats?days=30`),
    api('GET', `/admin/api/logs?game_id=${id}&level=error&limit=5`),
    api('GET', `/admin/api/games/${id}/releases`),
  ]);

  const days = stats.days;
  const today = days[days.length - 1] || { active: 0, logins: 0, new_players: 0 };
  const yesterday = days[days.length - 2] || { active: 0 };
  const published = releases.releases.filter(r => r.status === 'published');
  const latest = published[0];

  pageShell(host, {
    title: state.game.name,
    subtitle: `App ID ${esc(state.game.app_id)} · 创建于 ${fmtTime(state.game.created_at)}`,
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
