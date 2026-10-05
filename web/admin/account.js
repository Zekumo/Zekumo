// The signed-in operator: avatar, identity and session in the top app bar.
// There is no admin user table — the operator comes from configuration — so
// the avatar is derived from the name rather than uploaded.

// avatarColor picks a stable hue from the name, so the same operator always
// gets the same avatar instead of a random one each load.
function avatarColor(name) {
  let hash = 0;
  for (const ch of name) hash = (hash * 31 + ch.codePointAt(0)) % 360;
  return hash;
}

function initials(name) {
  if (!name) return '?';
  // Latin names abbreviate to a letter; CJK reads better with one glyph.
  const first = [...name][0];
  return /[a-zA-Z]/.test(first) ? first.toUpperCase() : first;
}

function renderAccount() {
  const me = state.me;
  if (!me) return;
  const hue = avatarColor(me.username);
  const av = document.getElementById('avatar');
  av.textContent = initials(me.username);
  av.style.background = `hsl(${hue} 42% 42%)`;
  document.getElementById('accountName').textContent = me.username;

  const warn = {
    default_password: '仍在使用默认管理员密码',
    default_jwt_secret: '仍在使用默认 JWT 密钥',
  };
  document.getElementById('accountMenu').innerHTML = `
    <div class="menu-head">
      <span class="avatar lg" style="background:hsl(${hue} 42% 42%)">${esc(initials(me.username))}</span>
      <div>
        <div class="menu-name">${esc(me.username)}</div>
        <div class="dim">${state.workspaceId ? esc(roleLabel(state.activeRole)) : '账号'}</div>
      </div>
    </div>
    <div class="menu-info">
      <div class="kv"><span>登录于</span><b>${fmtAgo(me.issued_at)}</b></div>
      <div class="kv"><span>会话到期</span><b title="${fmtTime(me.expires_at)}">${fmtUntil(me.expires_at)}</b></div>
    </div>
    ${me.warnings?.length ? `<div class="menu-warn">
      ${me.warnings.map(w => `<div>⚠ ${esc(warn[w] || w)}</div>`).join('')}
    </div>` : ''}
    <div class="menu-sep"></div>
    <button class="menu-item" onclick="cycleTheme()">
      <span>主题</span><span class="dim" id="menuTheme">${themeLabel()}</span>
    </button>
    <button class="menu-item danger" onclick="logout()">退出登录</button>`;
}

function toggleAccountMenu(e) {
  e.stopPropagation();
  document.getElementById('accountMenu').classList.toggle('hidden');
}

document.addEventListener('click', () => {
  document.getElementById('accountMenu')?.classList.add('hidden');
});

function normalizeMe(data) {
  const user = data.user || {};
  return {
    ...data,
    id: user.id || data.id,
    username: user.username || data.username,
    role: data.active_role || data.role,
  };
}

async function loadMe(workspaceId = state.workspaceId) {
  const session = state.sessionEpoch;
  const data = await api('GET', '/admin/api/me', undefined, { workspaceId });
  if (session !== state.sessionEpoch || workspaceId !== state.workspaceId) return null;
  state.me = normalizeMe(data);
  state.workspaces = data.workspaces || state.workspaces || [];
  state.workspace = state.workspaces.find(w => w.id === state.workspaceId) || null;
  state.activeRole = data.active_role || state.workspace?.role || null;
  renderAccount();
  return state.me;
}

async function refreshIdentity(workspaceId = state.workspaceId) {
  if (!token || workspaceId !== state.workspaceId) return;
  const previousRole = state.activeRole;
  const me = await loadMe(workspaceId);
  if (!me) return;
  const stillMember = state.workspaces.some(w => w.id === workspaceId);
  if (!stillMember) {
    const fallback = state.workspaces[0];
    if (fallback && typeof selectWorkspace === 'function') selectWorkspace(fallback.id);
    else if (!fallback && typeof renderWorkspaceWelcome === 'function') renderWorkspaceWelcome();
    return;
  }
  if (previousRole !== state.activeRole && typeof activateWorkspace === 'function') {
    await activateWorkspace(workspaceId);
    await route();
    return;
  }
  renderAccount();
  if (typeof renderWorkspaceSwitcher === 'function') renderWorkspaceSwitcher();
  applySurfaceAccess(document.getElementById('page'));
}

// securityBanner warns, in the console itself, about credentials that are
// still at their shipped defaults. Until now this only appeared in the
// startup log, where nobody running the console would see it.
function securityBanner() {
  const w = state.me?.warnings || [];
  if (!w.length) return '';
  const text = {
    default_password: '管理员密码仍是默认值,任何人都能登录这个控制台。',
    default_jwt_secret: 'JWT 签名密钥仍是默认值,任何人都能伪造令牌。',
  };
  return `<div class="banner">
    <span class="banner-icon">⚠</span>
    <div>
      <b>部署尚未加固</b>
      <ul>${w.map(k => `<li>${esc(text[k] || k)}</li>`).join('')}</ul>
      <span class="dim">通过环境变量 ADMIN_PASSWORD 与 JWT_SECRET 设置后重启即可。</span>
    </div>
  </div>`;
}
