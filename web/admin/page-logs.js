// Logs. Filters are chips rather than dropdowns because level and source are
// the two things you toggle constantly while chasing something down.

const LOG_LEVELS = ['error', 'warn', 'info', 'debug'];
const LOG_SOURCES = ['http', 'funcs', 'hooks', 'client'];
const logFilter = { level: '', source: '', q: '', scope: 'game', beforeId: 0, rows: [] };

async function renderLogs(host) {
  // Reachable both platform-wide and inside a game; with no game selected the
  // scope switch has nothing to switch between.
  const perGame = !!state.gameId;
  if (!perGame) logFilter.scope = 'all';

  const q = new URLSearchParams({ limit: 50 });
  if (perGame && logFilter.scope === 'game') q.set('game_id', state.gameId);
  if (logFilter.level) q.set('level', logFilter.level);
  if (logFilter.source) q.set('source', logFilter.source);
  if (logFilter.q) q.set('q', logFilter.q);
  const { entries } = await api('GET', '/admin/api/logs?' + q);
  logFilter.rows = entries;
  logFilter.beforeId = entries.length ? entries[entries.length - 1].id : 0;

  pageShell(host, {
    title: '日志',
    subtitle: perGame
      ? 'HTTP 访问(默认只记失败与慢请求)、云函数、WebHook 投递、客户端上报。'
      : '全平台日志。选择某个游戏可以只看它的记录。',
    body: `
      <div class="filters">
        <div class="chips">
          <button class="chip ${!logFilter.level ? 'on' : ''}" onclick="setLog('level','')">全部级别</button>
          ${LOG_LEVELS.map(l => `<button class="chip ${logFilter.level === l ? 'on' : ''}"
             onclick="setLog('level','${l}')">${l}</button>`).join('')}
        </div>
        <div class="chips">
          <button class="chip ${!logFilter.source ? 'on' : ''}" onclick="setLog('source','')">全部来源</button>
          ${LOG_SOURCES.map(s => `<button class="chip ${logFilter.source === s ? 'on' : ''}"
             onclick="setLog('source','${s}')">${s}</button>`).join('')}
        </div>
        ${perGame ? `<div class="chips">
          <button class="chip ${logFilter.scope === 'game' ? 'on' : ''}" onclick="setLog('scope','game')">仅本游戏</button>
          <button class="chip ${logFilter.scope === 'all' ? 'on' : ''}" onclick="setLog('scope','all')">含平台</button>
        </div>` : ''}
        <label class="field compact" style="width:260px">
          <input id="logQ" placeholder=" " value="${esc(logFilter.q)}"
                 onkeydown="if(event.key==='Enter'){setLog('q',this.value)}">
          <span class="label">搜索消息 / 事件</span></label>
      </div>

      <div class="card table-card" id="logCard">
        ${entries.length ? logTable(entries) : emptyState('📋', '没有匹配的日志', '换个过滤条件试试。')}
      </div>
      ${entries.length === 50 ? `<div class="row" style="justify-content:center;margin-top:16px">
        <button class="btn outlined" onclick="loadMoreLogs()">加载更早</button></div>` : ''}`,
  });
}

function logTable(entries) {
  return `<table><thead><tr>
      <th style="width:130px">时间</th><th style="width:80px">级别</th>
      <th style="width:90px">来源</th><th>事件 / 消息</th>
    </tr></thead><tbody id="logRows">${entries.map(logRow).join('')}</tbody></table>`;
}

function logRow(e) {
  const fields = e.fields && Object.keys(e.fields).length
    ? `<div class="log-fields">${Object.entries(e.fields)
        .map(([k, v]) => `<span><i>${esc(k)}</i> ${esc(typeof v === 'object' ? JSON.stringify(v) : v)}</span>`)
        .join('')}</div>` : '';
  return `<tr class="log-${esc(e.level)}">
    <td class="dim" title="${fmtTime(e.created_at)}">${fmtAgo(e.created_at)}</td>
    <td><span class="badge ${e.level === 'error' ? 'error' : e.level === 'warn' ? 'warn' : ''}">${esc(e.level)}</span></td>
    <td class="dim">${esc(e.source)}</td>
    <td>
      ${e.event ? `<code>${esc(e.event)}</code> ` : ''}${esc(e.message)}
      ${fields}
    </td>
  </tr>`;
}

function setLog(key, value) {
  logFilter[key] = value;
  route();
}

async function loadMoreLogs() {
  const q = new URLSearchParams({ limit: 50, before_id: logFilter.beforeId });
  if (state.gameId && logFilter.scope === 'game') q.set('game_id', state.gameId);
  if (logFilter.level) q.set('level', logFilter.level);
  if (logFilter.source) q.set('source', logFilter.source);
  if (logFilter.q) q.set('q', logFilter.q);
  const { entries } = await api('GET', '/admin/api/logs?' + q);
  if (!entries.length) return toast('没有更早的日志了');
  logFilter.beforeId = entries[entries.length - 1].id;
  document.getElementById('logRows').insertAdjacentHTML('beforeend', entries.map(logRow).join(''));
}
