// Shared plumbing: API access, formatting, and the UI primitives (dialogs,
// snackbar, secret fields, empty states) the pages build on.

let token = localStorage.getItem('zekumo_admin_token') || '';
const state = { games: [], gameId: null, game: null, me: null };

// ---------- API ----------
async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401 && path !== '/admin/api/login') { logout(); throw new Error('登录已过期'); }
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

function skeleton(rows = 3) {
  return `<div class="skeleton-wrap">${'<div class="skeleton-row"></div>'.repeat(rows)}</div>`;
}

// ---------- dialogs ----------
// Replaces window.confirm/prompt: those cannot carry context, cannot be
// styled, and read as a browser error rather than part of the product.
function openDialog({ title, body, confirmText = '确定', danger = false, onConfirm }) {
  const host = document.getElementById('dialogHost');
  host.innerHTML = `
    <div class="dialog" role="dialog" aria-modal="true">
      <h3>${esc(title)}</h3>
      <div class="dialog-body">${body}</div>
      <div class="dialog-actions">
        <button class="btn text" onclick="closeDialog()">取消</button>
        <button class="btn filled ${danger ? 'danger' : ''}" id="dlgConfirm">${esc(confirmText)}</button>
      </div>
    </div>`;
  document.getElementById('scrim').classList.remove('hidden');
  document.getElementById('dlgConfirm').onclick = async () => {
    try {
      await onConfirm();
      closeDialog();
    } catch (e) { toast(e.message); }
  };
  const first = host.querySelector('input, textarea, select');
  if (first) first.focus();
}

function closeDialog() {
  document.getElementById('dialogHost').innerHTML = '';
  document.getElementById('scrim').classList.add('hidden');
}

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
async function login() {
  try {
    const data = await api('POST', '/admin/api/login', {
      username: document.getElementById('loginUser').value,
      password: document.getElementById('loginPass').value,
    });
    token = data.token;
    localStorage.setItem('zekumo_admin_token', token);
    await enterApp();
  } catch (e) { toast('登录失败:' + e.message); }
}

function logout() {
  token = '';
  localStorage.removeItem('zekumo_admin_token');
  document.getElementById('appView').classList.add('hidden');
  document.getElementById('loginView').classList.remove('hidden');
}
