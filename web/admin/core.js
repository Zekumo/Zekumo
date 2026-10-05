// Shared plumbing: API access, formatting, and the UI primitives (dialogs,
// snackbar, secret fields, empty states) the pages build on.

let token = localStorage.getItem('zekumo_admin_token') || '';
const WORKSPACE_KEY = 'zekumo_admin_workspace_id';
const state = {
  games: [], gameId: null, game: null, me: null,
  workspaces: [], workspaceId: null, workspace: null, activeRole: null,
  sessionEpoch: 0, workspaceEpoch: 0,
};

const ROLE_LABEL = { owner: '所有者', admin: '管理员', editor: '编辑者', viewer: '查看者' };
const ROLE_RANK = { viewer: 10, editor: 20, admin: 30, owner: 40 };
function roleLabel(role) { return ROLE_LABEL[role] || role || '未知角色'; }
function hasRole(minimum) { return (ROLE_RANK[state.activeRole] || 0) >= (ROLE_RANK[minimum] || Infinity); }
function canWrite() { return hasRole('editor'); }
function canManageMembers() { return hasRole('admin'); }

function workspaceHeader(workspaceId = state.workspaceId) {
  return workspaceId ? { 'X-Zekumo-Workspace-ID': workspaceId } : {};
}

// ---------- API ----------
const mutationRequests = new Map();

function api(method, path, body, options = {}) {
  const verb = method.toUpperCase();
  if (['GET', 'HEAD'].includes(verb)) return apiRequest(verb, path, body, options);
  const key = `${state.sessionEpoch}:${(options.workspaceId ?? state.workspaceId) || ''}:${verb}:${path}:${JSON.stringify(body)}`;
  if (mutationRequests.has(key)) return mutationRequests.get(key);
  const request = apiRequest(verb, path, body, options).finally(() => mutationRequests.delete(key));
  mutationRequests.set(key, request);
  return request;
}

async function apiRequest(method, path, body, options = {}) {
  const verb = method.toUpperCase();
  const isLogin = path === '/admin/api/login';
  const isWorkspaceCreate = path === '/admin/api/workspaces' && verb === 'POST';
  if (!['GET', 'HEAD'].includes(verb) && !isLogin && !isWorkspaceCreate && !canWrite()) {
    throw new Error('当前是查看权限，无法修改此工作区');
  }
  const requestToken = token;
  const requestSession = state.sessionEpoch;
  const selectedWorkspace = Object.prototype.hasOwnProperty.call(options, 'workspaceId')
    ? options.workspaceId : state.workspaceId;
  const res = await fetch(path, {
    method: verb,
    headers: {
      'Content-Type': 'application/json',
      ...(requestToken ? { Authorization: 'Bearer ' + requestToken } : {}),
      ...workspaceHeader(selectedWorkspace),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: options.signal,
  });
  const data = await res.json().catch(() => ({}));
  // A slow 401 from a previous session must not sign out a newer login.
  if (res.status === 401 && !isLogin) {
    if (requestToken === token && requestSession === state.sessionEpoch) logout();
    throw new Error('登录已过期');
  }
  if (res.status === 403 && requestToken === token && requestSession === state.sessionEpoch &&
      path !== '/admin/api/me' && typeof refreshIdentity === 'function') {
    // Permissions may have changed in another session. Re-read the server's
    // membership instead of leaving stale write controls enabled.
    refreshIdentity(selectedWorkspace).catch(() => {});
  }
  if (!res.ok) throw new Error(data.error ? data.error.message : res.statusText);
  return data;
}

// ---------- formatting ----------
function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

// JSON arguments embedded in an inline handler must be HTML-escaped first;
// otherwise a quote in an operator-entered title can terminate the attribute.
function inlineJSON(value) { return esc(JSON.stringify(value)); }

function fmtTime(t) { return t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : '—'; }

// Relative time reads faster than a timestamp for "is this recent?".
function fmtAgo(t) {
  if (!t) return '—';
  const secs = (Date.now() - new Date(t)) / 1000;
  if (secs < 60) return '刚刚';
  if (secs < 3600) return `${Math.floor(secs / 60)} 分钟前`;
  if (secs < 86400) return `${Math.floor(secs / 3600)} 小时前`;
  if (secs < 2592000) return `${Math.floor(secs / 86400)} 天前`;
  return new Date(t).toLocaleDateString('zh-CN');
}

// fmtUntil is fmtAgo's mirror for timestamps in the future; feeding those to
// fmtAgo yields a negative delta and reads as "刚刚".
function fmtUntil(t) {
  if (!t) return '—';
  const secs = (new Date(t) - Date.now()) / 1000;
  if (secs <= 0) return '已过期';
  if (secs < 60) return '不到 1 分钟';
  if (secs < 3600) return `${Math.floor(secs / 60)} 分钟后`;
  if (secs < 86400) return `${Math.floor(secs / 3600)} 小时后`;
  return `${Math.floor(secs / 86400)} 天后`;
}

function fmtBytes(n) {
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
  return (n / 1073741824).toFixed(2) + ' GB';
}

function fmtNum(n) { return (n ?? 0).toLocaleString('zh-CN'); }

// ---------- snackbar ----------
let toastTimer = null;
function toast(text) {
  const el = document.getElementById('toast');
  el.textContent = text;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 3000);
}

// ---------- clipboard ----------
async function copyText(text, label) {
  try {
    await navigator.clipboard.writeText(text);
    toast(`已复制${label ? ' ' + label : ''}`);
  } catch {
    toast('复制失败,请手动选择');
  }
}

// ---------- reusable cell renderers ----------

// idCell shows a credential with a copy button rather than making the reader
// select the text by hand — copying an App ID is the most common action here.
function idCell(value, label) {
  const safe = esc(value);
  return `<span class="mono-chip">
    <code>${safe}</code>
    <button class="icon-btn tiny" title="复制" onclick="copyText('${safe}', '${esc(label || '')}')">
      <svg viewBox="0 0 24 24"><path d="M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1Zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2Zm0 16H8V7h11v14Z"/></svg>
    </button></span>`;
}

// secretCell keeps credentials masked until asked for. Showing every secret
// in plain text on load means a screen-share or screenshot leaks them all.
let secretSeq = 0;
function secretCell(value, label) {
  if (!canWrite()) return '<span class="badge">密钥已隐藏</span>';
  const id = 'sec' + (++secretSeq);
  window['__sec_' + id] = value;
  return `<span class="mono-chip" id="${id}">
    <code class="masked">••••••••••••••••</code>
    <button class="icon-btn tiny" title="显示" onclick="revealSecret('${id}')">
      <svg viewBox="0 0 24 24"><path d="M12 5c-7 0-10 7-10 7s3 7 10 7 10-7 10-7-3-7-10-7Zm0 12a5 5 0 1 1 0-10 5 5 0 0 1 0 10Zm0-2a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z"/></svg>
    </button>
    <button class="icon-btn tiny" title="复制" onclick="copyText(window['__sec_${id}'], '${esc(label || '')}')">
      <svg viewBox="0 0 24 24"><path d="M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1Zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2Zm0 16H8V7h11v14Z"/></svg>
    </button></span>`;
}

function revealSecret(id) {
  const host = document.getElementById(id);
  const code = host.querySelector('code');
  const shown = code.classList.toggle('masked');
  code.textContent = shown ? '••••••••••••••••' : window['__sec_' + id];
}

function emptyState(icon, title, hint) {
  return `<div class="empty">
    <div class="empty-icon">${icon}</div>
    <div class="empty-title">${esc(title)}</div>
    ${hint ? `<div class="empty-hint">${hint}</div>` : ''}
  </div>`;
}

function mascotState(kind, title, hint, action = '') {
  const safeKind = ['welcome', 'empty', 'error'].includes(kind) ? kind : 'empty';
  const source = {
    welcome: 'assets/zekumo-welcome-wave.png',
    empty: 'assets/zekumo-empty-cloud.png',
    error: 'assets/zekumo-error-concerned.png',
  }[safeKind];
  return `<div class="mascot-state ${safeKind}">
    <img src="${source}" alt="" aria-hidden="true">
    <div class="mascot-copy">
      <div class="empty-title">${esc(title)}</div>
      ${hint ? `<div class="empty-hint">${esc(hint)}</div>` : ''}
      ${action}
    </div>
  </div>`;
}

function errorState(title, hint, retry = false) {
  return mascotState('error', title, hint || '请稍后再试。', retry
    ? '<button class="btn tonal" onclick="route()">重试</button>' : '');
}

function skeleton(rows = 3) {
  return `<div class="skeleton-wrap">${'<div class="skeleton-row"></div>'.repeat(rows)}</div>`;
}

// ---------- dialogs ----------
// Replaces window.confirm/prompt: those cannot carry context, cannot be
// styled, and read as a browser error rather than part of the product.
let dialogSeq = 0;
let activeDialog = null;

function openDialog({ title, body, confirmText = '确定', danger = false, onConfirm }) {
  const id = ++dialogSeq;
  activeDialog = {
    id, submitting: false,
    context: `${state.sessionEpoch}:${state.workspaceId || ''}:${state.gameId || ''}`,
  };
  const host = document.getElementById('dialogHost');
  host.innerHTML = `
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="dialogTitle">
      <h3 id="dialogTitle">${esc(title)}</h3>
      <div class="dialog-body">${body}</div>
      <div class="dialog-actions">
        <button class="btn text" id="dlgCancel" onclick="closeDialog(${id})">取消</button>
        <button class="btn filled ${danger ? 'danger' : ''}" id="dlgConfirm">${esc(confirmText)}</button>
      </div>
    </div>`;
  document.getElementById('scrim').classList.remove('hidden');
  document.getElementById('dlgConfirm').onclick = async () => {
    if (!activeDialog || activeDialog.id !== id || activeDialog.submitting) return;
    activeDialog.submitting = true;
    const confirm = document.getElementById('dlgConfirm');
    const cancel = document.getElementById('dlgCancel');
    confirm.disabled = true;
    cancel.disabled = true;
    confirm.setAttribute('aria-busy', 'true');
    try {
      await onConfirm();
      closeDialog(id, true);
    } catch (e) {
      if (!activeDialog || activeDialog.id !== id) return;
      activeDialog.submitting = false;
      confirm.disabled = false;
      cancel.disabled = false;
      confirm.removeAttribute('aria-busy');
      toast(e.message);
    }
  };
  const first = host.querySelector('input, textarea, select');
  if (first) first.focus();
}

function closeDialog(id = activeDialog?.id, force = false) {
  if (!activeDialog || (id != null && id !== activeDialog.id)) return false;
  if (activeDialog.submitting && !force) {
    toast('正在处理，请稍候');
    return false;
  }
  activeDialog = null;
  document.getElementById('dialogHost').innerHTML = '';
  document.getElementById('scrim').classList.add('hidden');
  return true;
}

function forceCloseDialog() { if (activeDialog) closeDialog(activeDialog.id, true); }

function confirmDelete(what, onConfirm) {
  openDialog({
    title: `删除${what}?`,
    body: `<p class="dim">此操作不可撤销。</p>`,
    confirmText: '删除', danger: true, onConfirm,
  });
}

function dlgField(id, label, value = '', type = 'text') {
  return `<label class="field">
    <input id="${id}" type="${type}" placeholder=" " value="${esc(value)}">
    <span class="label">${esc(label)}</span></label>`;
}

function dlgVal(id) { return document.getElementById(id).value.trim(); }

// ---------- auth ----------
let loginPending = false;
async function login() {
  if (loginPending) return;
  const submit = document.getElementById('loginSubmit');
  loginPending = true;
  if (submit) { submit.disabled = true; submit.setAttribute('aria-busy', 'true'); }
  try {
    const data = await api('POST', '/admin/api/login', {
      username: document.getElementById('loginUser').value,
      password: document.getElementById('loginPass').value,
    });
    token = data.token;
    state.sessionEpoch += 1;
    localStorage.setItem('zekumo_admin_token', token);
    await enterApp(data);
  } catch (e) {
    toast('登录失败:' + e.message);
  } finally {
    loginPending = false;
    if (submit) { submit.disabled = false; submit.removeAttribute('aria-busy'); }
  }
}

function logout() {
  state.sessionEpoch += 1;
  state.workspaceEpoch += 1;
  token = '';
  localStorage.removeItem('zekumo_admin_token');
  localStorage.removeItem(WORKSPACE_KEY);
  resetScopedUIState();
  state.games = [];
  state.gameId = null;
  state.game = null;
  state.me = null;
  state.workspaces = [];
  state.workspaceId = null;
  state.workspace = null;
  state.activeRole = null;
  document.body.removeAttribute('data-read-only');
  document.getElementById('loginPass').value = '';
  document.getElementById('page').replaceChildren();
  document.getElementById('navRail').replaceChildren();
  document.getElementById('gameSwitcher').replaceChildren();
  document.getElementById('workspaceSwitcher')?.replaceChildren();
  document.getElementById('accountMenu').replaceChildren();
  document.getElementById('avatar').textContent = '';
  document.getElementById('accountName').textContent = '';
  document.getElementById('appView').classList.add('hidden');
  document.getElementById('loginView').classList.remove('hidden');
}

function resetScopedUIState() {
  forceCloseDialog();
  if (typeof exportTimer !== 'undefined' && exportTimer) {
    clearTimeout(exportTimer);
    exportTimer = null;
  }
  for (const key of ['__script', '__fn', '__board', '__mailCurrencies', '__playerSearch', '__acctSearch']) {
    delete window[key];
  }
  for (const key of Object.keys(window)) {
    if (key.startsWith('__sec_')) delete window[key];
  }
  if (typeof logFilter !== 'undefined') {
    Object.assign(logFilter, { level: '', source: '', q: '', scope: 'game', beforeId: 0, rows: [] });
  }
  secretSeq = 0;
}

function readOnlyBanner() {
  return canWrite() ? '' : `<div class="banner role-banner">
    <span class="banner-icon">👁</span>
    <div><b>查看权限</b><div class="dim">你可以浏览此工作区的数据，但不能修改。</div></div>
  </div>`;
}

function applySurfaceAccess(host) {
  document.body.toggleAttribute('data-read-only', !canWrite());
  host.querySelectorAll('[data-write]').forEach(el => {
    const minimum = el.dataset.minRole || 'editor';
    if (hasRole(minimum)) return;
    el.disabled = true;
    el.setAttribute('aria-disabled', 'true');
    el.title = `需要${roleLabel(minimum)}或更高权限`;
  });
}

function operationContext(gameId = state.gameId) {
  return {
    sessionEpoch: state.sessionEpoch,
    workspaceEpoch: state.workspaceEpoch,
    workspaceId: state.workspaceId,
    gameId,
  };
}

function contextCurrent(ctx) {
  return ctx.sessionEpoch === state.sessionEpoch &&
    ctx.workspaceEpoch === state.workspaceEpoch &&
    ctx.workspaceId === state.workspaceId && ctx.gameId === state.gameId;
}

function requireCurrentContext(ctx) {
  if (!contextCurrent(ctx)) throw new Error('工作区或游戏已切换，操作已停止');
}
