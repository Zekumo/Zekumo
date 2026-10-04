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
        <div class="dim">${me.role === 'admin' ? '平台管理员' : esc(me.role)}</div>
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

async function loadMe() {
  state.me = await api('GET', '/admin/api/me');
  renderAccount();
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
