// Navigation: a hash route decides which page renders, so the browser back
// button and bookmarks work. Routes are either platform-level (#/games) or
// scoped to a game (#/g/<id>/overview).

// Render functions are called lazily: the page-*.js files load after this
// one, so naming them directly here would capture undefined. `scopes` lists
// where a page may appear — logs works both platform-wide and per game.
const ROUTES = {
  dashboard: { title: '仪表盘',     scopes: ['platform'], render: h => renderDashboard(h) },
  games:     { title: '游戏',       scopes: ['platform'], render: h => renderGames(h) },
  accounts:  { title: '通行证账号', scopes: ['platform'], render: h => renderAccounts(h) },
  oauth:     { title: 'OAuth 应用', scopes: ['platform'], render: h => renderOAuth(h) },

  overview:  { title: '概览',       scopes: ['game'], render: h => renderOverview(h) },
  players:   { title: '玩家',       scopes: ['game'], render: h => renderPlayers(h) },
  boards:    { title: '排行榜',     scopes: ['game'], render: h => renderBoards(h) },
  content:   { title: '剧情对话',   scopes: ['game'], render: h => renderContent(h) },
  releases:  { title: '版本更新',   scopes: ['game'], render: h => renderReleases(h) },
  functions: { title: '云函数',     scopes: ['game'], render: h => renderFunctions(h) },
  webhooks:  { title: 'WebHook',    scopes: ['game'], render: h => renderWebhooks(h) },
  achievements: { title: '成就',    scopes: ['game'], render: h => renderAchievements(h) },
  announcements: { title: '公告',  scopes: ['game'], render: h => renderAnnouncements(h) },
  currency:  { title: '虚拟货币',   scopes: ['game'], render: h => renderCurrency(h) },
  mail:      { title: '邮件',       scopes: ['game'], render: h => renderMail(h) },
  bans:      { title: '封禁',       scopes: ['game'], render: h => renderBans(h) },
  exports:   { title: '数据导出',   scopes: ['game'], render: h => renderExports(h) },
  settings:  { title: '设置',       scopes: ['game'], render: h => renderSettings(h) },

  logs:      { title: '日志',       scopes: ['platform', 'game'], render: h => renderLogs(h) },
};

const NAV_PLATFORM = [
  ['dashboard', 'M3 13h8V3H3v10Zm0 8h8v-6H3v6Zm10 0h8V11h-8v10Zm0-18v6h8V3h-8Z'],
  ['games', 'M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Zm2 5v2H5v2h2v2h2v-2h2v-2H9V9H7Zm9 1a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3Z'],
  ['accounts', 'M12 12a5 5 0 1 0 0-10 5 5 0 0 0 0 10Zm0 2c-4 0-9 2-9 5v3h18v-3c0-3-5-5-9-5Z'],
  ['oauth', 'M12 1 3 5v6c0 5.5 3.8 10.7 9 12 5.2-1.3 9-6.5 9-12V5l-9-4Zm0 10.9h7c-.5 4.1-3.3 7.8-7 8.9V12H5V6.3l7-3.1v8.7Z'],
  ['logs', 'M4 3h16v2H4V3Zm0 4h16v2H4V7Zm0 4h10v2H4v-2Zm0 4h16v2H4v-2Zm0 4h10v2H4v-2Z'],
];

const NAV_GAME = [
  ['overview', 'M3 13h8V3H3v10Zm0 8h8v-6H3v6Zm10 0h8V11h-8v10Zm0-18v6h8V3h-8Z'],
  ['players', 'M12 12a5 5 0 1 0 0-10 5 5 0 0 0 0 10Zm0 2c-4 0-9 2-9 5v3h18v-3c0-3-5-5-9-5Z'],
  ['boards', 'M4 20h4v-8H4v8Zm6 0h4V4h-4v16Zm6 0h4v-5h-4v5Z'],
  ['content', 'M20 2H4a2 2 0 0 0-2 2v18l4-4h14a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2ZM6 9h12v2H6V9Zm0 4h8v2H6v-2ZM6 5h12v2H6V5Z'],
  ['releases', 'M12 2 4 6v6c0 5 3.4 9.4 8 10 4.6-.6 8-5 8-10V6l-8-4Zm-1 14-4-4 1.4-1.4L11 13.2l4.6-4.6L17 10l-6 6Z'],
  ['functions', 'M9.4 16.6 4.8 12l4.6-4.6L8 6l-6 6 6 6 1.4-1.4Zm5.2 0 4.6-4.6-4.6-4.6L16 6l6 6-6 6-1.4-1.4Z'],
  ['webhooks', 'M10 4a4 4 0 0 1 3.8 5.3l3.4 5.8a4 4 0 1 1-1.8 1L12 10.6a4 4 0 1 1-2-6.6Zm-3 13a4 4 0 0 1 3.9-4h4.2v2h-4.2a2 2 0 1 0 0 4H12v2h-1.1A4 4 0 0 1 7 17Z'],
  ['achievements', 'M12 2 14.8 8l6.5.6-4.9 4.3 1.5 6.4L12 16l-5.9 3.3 1.5-6.4-4.9-4.3L9.2 8 12 2Z'],
  ['announcements', 'M4 4h16v12H7l-3 3V4Zm3 4v2h10V8H7Zm0 4v2h7v-2H7Z'],
  ['currency', 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm1 15.9V19h-2v-1.1c-1.7-.3-3-1.5-3-3.4h2c0 1 .8 1.6 2 1.6s1.9-.5 1.9-1.3c0-.7-.5-1.1-2.2-1.5-2-.5-3.4-1.2-3.4-3 0-1.6 1.2-2.7 2.7-3V6h2v1.3c1.6.3 2.7 1.4 2.7 3.1h-2c0-.9-.6-1.5-1.7-1.5-1.1 0-1.7.5-1.7 1.2 0 .6.5 1 2.2 1.4 2 .5 3.4 1.3 3.4 3.1 0 1.7-1.3 2.9-2.9 3.3Z'],
  ['mail', 'M20 4H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2Zm0 4-8 5-8-5V6l8 5 8-5v2Z'],
  ['bans', 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20ZM4 12a8 8 0 0 1 12.9-6.3L5.7 16.9A8 8 0 0 1 4 12Zm8 8a8 8 0 0 1-4.9-1.7L18.3 7.1A8 8 0 0 1 12 20Z'],
  ['exports', 'M5 20h14v-2H5v2ZM19 9h-4V3H9v6H5l7 7 7-7Z'],
  ['logs', 'M4 3h16v2H4V3Zm0 4h16v2H4V7Zm0 4h10v2H4v-2Zm0 4h16v2H4v-2Zm0 4h10v2H4v-2Z'],
  ['settings', 'M19.4 13a7.8 7.8 0 0 0 0-2l2-1.6-2-3.4-2.4 1a7.6 7.6 0 0 0-1.7-1L15 3.3h-4l-.3 2.6c-.6.3-1.2.6-1.7 1l-2.4-1-2 3.4L4.6 11a7.8 7.8 0 0 0 0 2l-2 1.6 2 3.4 2.4-1c.5.4 1.1.8 1.7 1l.3 2.6h4l.3-2.6c.6-.2 1.2-.6 1.7-1l2.4 1 2-3.4-2-1.6ZM13 15a3 3 0 1 1 0-6 3 3 0 0 1 0 6Z'],
];

function icon(path) { return `<svg viewBox="0 0 24 24"><path d="${path}"/></svg>`; }

function currentRoute() {
  const hash = location.hash.replace(/^#\/?/, '');
  const parts = hash.split('/').filter(Boolean);
  if (parts[0] === 'g' && parts[1]) {
    return { gameId: parts[1], page: parts[2] || 'overview' };
  }
  return { gameId: null, page: parts[0] || 'dashboard' };
}

function go(page, gameId) {
  const id = gameId !== undefined ? gameId : state.gameId;
  const scoped = id && ROUTES[page]?.scopes.includes('game');
  location.hash = scoped ? `#/g/${id}/${page}` : `#/${page}`;
}

function renderNav(active, scope) {
  const items = scope === 'game' ? NAV_GAME : NAV_PLATFORM;
  const rail = document.getElementById('navRail');
  const header = scope === 'game'
    ? `<button class="nav-back" onclick="go('dashboard', null)">${icon('M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20v-2Z')} 返回平台</button>`
    : `<div class="nav-section">平台</div>`;
  rail.innerHTML = header + items.map(([key, path]) => `
    <button class="nav-item ${key === active ? 'on' : ''}" onclick="go('${key}')">
      ${icon(path)}<span>${ROUTES[key].title}</span>
    </button>`).join('');
}

function toggleNav() { document.getElementById('navRail').classList.toggle('open'); }

function renderSwitcher() {
  const el = document.getElementById('gameSwitcher');
  if (!state.game) { el.innerHTML = ''; return; }
  el.innerHTML = `<span class="switcher-sep">/</span>
    <select onchange="go(currentRoute().page, this.value)">
      ${state.games.map(g =>
        `<option value="${g.id}" ${g.id === state.gameId ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}
    </select>`;
}

async function route() {
  const routeID = (window.__routeID || 0) + 1;
  window.__routeID = routeID;
  window.__routeAbort?.abort();
  window.__routeAbort = new AbortController();
  // Back/forward navigation dismisses a modal from the previous page instead
  // of leaving it floating over unrelated content.
  closeDialog();

  const { gameId, page } = currentRoute();
  const scope = gameId ? 'game' : 'platform';
  let def = ROUTES[page];
  if (!def || !def.scopes.includes(scope)) {
    // A game-only page without a game (or the reverse): fall back to the
    // default landing page for whichever scope we are actually in.
    location.hash = gameId ? `#/g/${gameId}/overview` : '#/dashboard';
    return;
  }

  state.gameId = gameId;
  state.game = gameId ? state.games.find(g => g.id === gameId) : null;
  if (gameId && !state.game) { // stale link, or the game was deleted
    location.hash = '#/dashboard';
    return;
  }

  renderNav(page, scope);
  renderSwitcher();
  document.getElementById('navRail').classList.remove('open');

  const host = document.getElementById('page');
  host.innerHTML = `<h1 class="page-title">${esc(def.title)}</h1>${skeleton()}`;
  try {
    await def.render(host);
  } catch (e) {
    if (routeID !== window.__routeID || e.name === 'AbortError') return;
    host.innerHTML = `<h1 class="page-title">${esc(def.title)}</h1>` +
      emptyState('⚠️', '加载失败', `${esc(e.message)}<div style="margin-top:16px"><button class="btn outlined" onclick="route()">重试</button></div>`);
  }
}

// pageShell keeps every page's header consistent: title, optional subtitle,
// and the primary action on the right.
function pageShell(host, { title, subtitle, actions = '', body }) {
  host.innerHTML = `
    <div class="page-head">
      <div>
        <h1 class="page-title">${esc(title)}</h1>
        ${subtitle ? `<p class="dim">${subtitle}</p>` : ''}
      </div>
      <div class="row">${actions}</div>
    </div>
    ${body}`;
}

async function refreshGames() {
  state.games = (await api('GET', '/admin/api/games')).games;
}

async function enterApp() {
  document.getElementById('loginView').classList.add('hidden');
  document.getElementById('appView').classList.remove('hidden');
  await Promise.all([refreshGames(), loadMe()]);
  if (!location.hash) location.hash = '#/dashboard';
  else await route();
}

window.addEventListener('hashchange', route);
