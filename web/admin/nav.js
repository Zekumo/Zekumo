// Navigation: a hash route decides which page renders, so the browser back
// button and bookmarks work. Workspace identity is part of the URL so Back,
// Forward, and bookmarks cannot silently pair one workspace with another's
// game ID.

// Render functions are called lazily: the page-*.js files load after this
// one, so naming them directly here would capture undefined. `scopes` lists
// where a page may appear — logs works both platform-wide and per game.
const ROUTES = {
  dashboard: { title: '仪表盘',     scopes: ['platform'], render: h => renderDashboard(h) },
  games:     { title: '游戏',       scopes: ['platform'], render: h => renderGames(h) },
  accounts:  { title: '通行证账号', scopes: ['platform'], render: h => renderAccounts(h) },
  oauth:     { title: 'OAuth 应用', scopes: ['platform'], render: h => renderOAuth(h) },
  members:   { title: '成员与权限', scopes: ['platform'], render: h => renderWorkspace(h) },

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
  ['members', 'M16 11c1.7 0 3-1.3 3-3s-1.3-3-3-3-3 1.3-3 3 1.3 3 3 3ZM8 11c1.7 0 3-1.3 3-3S9.7 5 8 5 5 6.3 5 8s1.3 3 3 3Zm0 2c-2.3 0-7 1.2-7 3.5V19h10v-2.5c0-.8.3-1.5.8-2.1C10.5 13.5 9 13 8 13Zm8 0c-1 0-2.5.5-3.8 1.4.5.6.8 1.3.8 2.1V19h10v-2.5c0-2.3-4.7-3.5-7-3.5Z'],
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
  const parts = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean);
  if (parts[0] === 'w' && parts[1]) {
    if (parts[2] === 'g' && parts[3]) {
      return { workspaceId: parts[1], gameId: parts[3], page: parts[4] || 'overview' };
    }
    return { workspaceId: parts[1], gameId: null, page: parts[2] || 'dashboard' };
  }
  // Old bookmarks remain usable; route() upgrades them with replaceState.
  if (parts[0] === 'g' && parts[1]) {
    return { workspaceId: null, gameId: parts[1], page: parts[2] || 'overview' };
  }
  return { workspaceId: null, gameId: null, page: parts[0] || 'dashboard' };
}

function routeHash(page, gameId = null, workspaceId = state.workspaceId) {
  if (!workspaceId) return '#/welcome';
  const scoped = gameId && ROUTES[page]?.scopes.includes('game');
  return scoped ? `#/w/${workspaceId}/g/${gameId}/${page}` : `#/w/${workspaceId}/${page}`;
}

function replaceRoute(page, gameId = null, workspaceId = state.workspaceId) {
  history.replaceState(null, '', routeHash(page, gameId, workspaceId));
  route();
}

function go(page, gameId, workspaceId) {
  const id = gameId !== undefined ? gameId : state.gameId;
  location.hash = routeHash(page, id, workspaceId || state.workspaceId);
}

function renderNav(active, scope) {
  const items = scope === 'game' ? NAV_GAME : NAV_PLATFORM;
  const rail = document.getElementById('navRail');
  const header = scope === 'game'
    ? `<button class="nav-back" onclick="go('dashboard', null)">${icon('M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20v-2Z')} 返回工作区</button>`
    : `<div class="nav-section">${esc(state.workspace?.name || '工作区')}</div>`;
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
    <select aria-label="切换游戏" onchange="go(currentRoute().page, this.value)">
      ${state.games.map(g =>
        `<option value="${esc(g.id)}" ${g.id === state.gameId ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}
    </select>`;
}

function renderWorkspaceSwitcher() {
  const el = document.getElementById('workspaceSwitcher');
  if (!state.workspaceId || !state.workspaces.length) { el.innerHTML = ''; return; }
  el.innerHTML = `<span class="switcher-sep">/</span>
    <label class="workspace-select-wrap">
      <span class="sr-only">切换工作区</span>
      <select aria-label="切换工作区" onchange="selectWorkspace(this.value)">
        ${state.workspaces.map(w => `<option value="${esc(w.id)}" ${w.id === state.workspaceId ? 'selected' : ''}>${esc(w.name)}</option>`).join('')}
      </select>
    </label>
    <span class="workspace-role">${esc(roleLabel(state.activeRole))}</span>`;
}

function selectWorkspace(workspaceId) {
  if (!workspaceId || workspaceId === state.workspaceId) return;
  location.hash = routeHash('dashboard', null, workspaceId);
}

async function activateWorkspace(workspaceId) {
  const workspace = state.workspaces.find(w => w.id === workspaceId);
  if (!workspace) throw new Error('你已无法访问这个工作区');
  const session = state.sessionEpoch;
  const epoch = ++state.workspaceEpoch;

  resetScopedUIState();
  state.games = [];
  state.gameId = null;
  state.game = null;
  state.workspaceId = workspaceId;
  state.workspace = workspace;
  state.activeRole = workspace.role || null;
  localStorage.setItem(WORKSPACE_KEY, workspaceId);
  renderWorkspaceSwitcher();
  renderAccount();

  const [gamesData, meData] = await Promise.all([
    api('GET', '/admin/api/games', undefined, { workspaceId }),
    api('GET', '/admin/api/me', undefined, { workspaceId }),
  ]);
  if (session !== state.sessionEpoch || epoch !== state.workspaceEpoch || workspaceId !== state.workspaceId) {
    return false;
  }
  state.games = gamesData.games || [];
  state.me = normalizeMe(meData);
  state.workspaces = meData.workspaces || state.workspaces;
  state.workspace = state.workspaces.find(w => w.id === workspaceId) || null;
  if (!state.workspace) throw new Error('你已无法访问这个工作区');
  state.activeRole = meData.active_role || state.workspace.role || null;
  renderWorkspaceSwitcher();
  renderAccount();
  return true;
}

let routeSeq = 0;
async function route() {
  const seq = ++routeSeq;
  const requested = currentRoute();
  if (!state.workspaces.length) {
    renderWorkspaceWelcome();
    return;
  }

  const desiredWorkspace = requested.workspaceId || state.workspaceId || state.workspaces[0].id;
  if (!state.workspaces.some(w => w.id === desiredWorkspace)) {
    replaceRoute('dashboard', null, state.workspaceId || state.workspaces[0].id);
    return;
  }
  if (desiredWorkspace !== state.workspaceId || !state.workspace) {
    const host = document.getElementById('page');
    host.innerHTML = `<div class="workspace-loading">${skeleton(4)}</div>`;
    try {
      if (!await activateWorkspace(desiredWorkspace) || seq !== routeSeq) return;
    } catch (e) {
      if (seq !== routeSeq) return;
      host.innerHTML = errorState('工作区加载失败', e.message, true);
      return;
    }
  }

  if (!requested.workspaceId) {
    replaceRoute(requested.page, requested.gameId, desiredWorkspace);
    return;
  }

  const { gameId, page } = requested;
  const scope = gameId ? 'game' : 'platform';
  const def = ROUTES[page];
  if (!def || !def.scopes.includes(scope)) {
    replaceRoute(gameId ? 'overview' : 'dashboard', gameId, desiredWorkspace);
    return;
  }

  state.gameId = gameId;
  state.game = gameId ? state.games.find(g => g.id === gameId) : null;
  if (gameId && !state.game) {
    replaceRoute('dashboard', null, desiredWorkspace);
    return;
  }

  renderNav(page, scope);
  renderSwitcher();
  document.getElementById('navRail').classList.remove('open');

  // Every route gets its own detached surface. A slow older renderer may still
  // finish, but it can only paint the surface removed by the newer route.
  const pageHost = document.getElementById('page');
  const host = document.createElement('div');
  host.className = 'route-surface';
  host.innerHTML = `<h1 class="page-title">${esc(def.title)}</h1>${skeleton()}`;
  pageHost.replaceChildren(host);
  try {
    await def.render(host);
    if (seq !== routeSeq || !host.isConnected) return;
    applySurfaceAccess(host);
  } catch (e) {
    if (seq !== routeSeq || !host.isConnected) return;
    host.innerHTML = `<h1 class="page-title">${esc(def.title)}</h1>` + errorState('加载失败', e.message);
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
    ${readOnlyBanner()}${body}`;
}

async function refreshGames(workspaceId = state.workspaceId) {
  const session = state.sessionEpoch;
  const epoch = state.workspaceEpoch;
  const games = (await api('GET', '/admin/api/games', undefined, { workspaceId })).games || [];
  if (session === state.sessionEpoch && epoch === state.workspaceEpoch && workspaceId === state.workspaceId) {
    state.games = games;
  }
  return games;
}

async function enterApp(loginData = null) {
  document.getElementById('loginView').classList.add('hidden');
  document.getElementById('appView').classList.remove('hidden');
  const session = state.sessionEpoch;
  let initial = loginData;
  if (!initial?.workspaces) initial = await api('GET', '/admin/api/me', undefined, { workspaceId: null });
  if (session !== state.sessionEpoch) return;

  state.me = normalizeMe(initial);
  state.workspaces = initial.workspaces || [];
  renderAccount();
  if (!state.workspaces.length) {
    state.workspaceId = null;
    state.workspace = null;
    state.activeRole = null;
    renderWorkspaceWelcome();
    return;
  }

  const requested = currentRoute().workspaceId;
  const stored = localStorage.getItem(WORKSPACE_KEY);
  const preferred = [requested, stored, initial.default_workspace_id, initial.active_workspace_id]
    .find(id => state.workspaces.some(w => w.id === id)) || state.workspaces[0].id;
  if (!location.hash || requested !== preferred) {
    history.replaceState(null, '', routeHash('dashboard', null, preferred));
  }
  await route();
}

window.addEventListener('hashchange', route);
