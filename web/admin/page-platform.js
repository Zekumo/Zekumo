// Platform-level pages: the games list (the app's home), OAuth applications
// and passport accounts.

async function renderGames(host) {
  await refreshGames();
  const games = state.games;
  pageShell(host, {
    title: '游戏',
    subtitle: '每个游戏是一个独立的租户,数据互不可见。',
    actions: `<button class="btn filled" onclick="newGameDialog()">创建游戏</button>`,
    body: games.length ? `<div class="grid">${games.map(gameCard).join('')}</div>`
      : emptyState('🎮', '还没有游戏', '创建第一个游戏,拿到 App ID 后客户端就可以登录了。'),
  });
}

function gameCard(g) {
  return `<div class="game-card" onclick="go('overview', '${g.id}')">
    <div class="game-card-head">
      <span class="game-name">${esc(g.name)}</span>
      <span class="badge ${g.online > 0 ? 'ok' : ''}">${g.online} 在线</span>
    </div>
    <div class="game-card-row" onclick="event.stopPropagation()">
      <span class="dim">App ID</span>${idCell(g.app_id, 'App ID')}
    </div>
    <div class="game-card-row" onclick="event.stopPropagation()">
      <span class="dim">Secret</span>${secretCell(g.app_secret, 'App Secret')}
    </div>
    <div class="game-card-foot">
      <span class="dim">创建于 ${fmtAgo(g.created_at)}</span>
      <button class="btn text danger" onclick="event.stopPropagation();deleteGame('${g.id}','${esc(g.name)}')">删除</button>
    </div>
  </div>`;
}

function newGameDialog() {
  openDialog({
    title: '创建游戏',
    body: dlgField('ngName', '游戏名称'),
    confirmText: '创建',
    onConfirm: async () => {
      const name = dlgVal('ngName');
      if (!name) throw new Error('请输入游戏名称');
      const g = await api('POST', '/admin/api/games', { name });
      await refreshGames();
      toast('已创建');
      go('overview', g.id);
    },
  });
}

function deleteGame(id, name) {
  openDialog({
    title: `删除游戏「${name}」?`,
    body: `<p class="dim">该游戏的玩家、存档、排行榜、剧情、版本与日志都会一并删除,不可恢复。</p>`,
    confirmText: '永久删除', danger: true,
    onConfirm: async () => {
      await api('DELETE', '/admin/api/games/' + id);
      toast('已删除');
      go('games', null);
      await route();
    },
  });
}

// ---------- OAuth applications ----------
async function renderOAuth(host) {
  const { clients } = await api('GET', '/admin/api/oauth/clients');
  pageShell(host, {
    title: 'OAuth 应用',
    subtitle: '第三方应用用 Zekumo 通行证登录。公开客户端(无 secret)强制使用 PKCE。',
    actions: `<button class="btn filled" onclick="newOAuthDialog()">创建应用</button>`,
    body: clients.length ? `<div class="card table-card"><table>
      <thead><tr><th>名称</th><th>Client ID</th><th>Client Secret</th><th>回跳白名单</th><th>创建</th><th></th></tr></thead>
      <tbody>${clients.map(c => `<tr>
        <td><b>${esc(c.name)}</b></td>
        <td>${idCell(c.client_id, 'Client ID')}</td>
        <td>${c.client_secret ? secretCell(c.client_secret, 'Client Secret')
                              : '<span class="badge">公开客户端</span>'}</td>
        <td class="dim">${(c.redirect_urls || '').split('\n').filter(Boolean).map(esc).join('<br>') || '—'}</td>
        <td class="dim">${fmtAgo(c.created_at)}</td>
        <td><button class="btn text danger" onclick="deleteOAuth('${c.client_id}','${esc(c.name)}')">删除</button></td>
      </tr>`).join('')}</tbody></table></div>`
      : emptyState('🔑', '还没有 OAuth 应用', '第三方要接入通行证登录时,在这里为它注册一个应用。'),
  });
}

function newOAuthDialog() {
  openDialog({
    title: '创建 OAuth 应用',
    body: dlgField('noName', '应用名称') +
      `<label class="field"><textarea id="noUrls" placeholder=" " style="min-height:90px"></textarea>
        <span class="label">回跳地址(每行一个)</span></label>
       <label class="check"><input type="checkbox" id="noConf" checked> 机密客户端(签发 client_secret)</label>
       <p class="dim" style="margin-top:12px">scheme 与域名须完全一致,仅路径按段前缀匹配。</p>`,
    confirmText: '创建',
    onConfirm: async () => {
      const name = dlgVal('noName');
      if (!name) throw new Error('请输入应用名称');
      await api('POST', '/admin/api/oauth/clients', {
        name,
        redirect_urls: document.getElementById('noUrls').value.split('\n').map(s => s.trim()).filter(Boolean).join('\n'),
        confidential: document.getElementById('noConf').checked,
      });
      toast('已创建');
      await route();
    },
  });
}

function deleteOAuth(clientId, name) {
  confirmDelete(`应用「${name}」`, async () => {
    await api('DELETE', '/admin/api/oauth/clients/' + clientId);
    toast('已删除');
    await route();
  });
}

// ---------- passport accounts ----------
async function renderAccounts(host) {
  const search = (window.__acctSearch || '');
  const { accounts } = await api('GET', '/admin/api/accounts?search=' + encodeURIComponent(search));
  pageShell(host, {
    title: '通行证账号',
    subtitle: '平台级账号,一个账号可登录所有游戏。',
    actions: `<label class="field compact" style="width:240px">
        <input id="acctSearch" placeholder=" " value="${esc(search)}"
               onkeydown="if(event.key==='Enter'){window.__acctSearch=this.value;route()}">
        <span class="label">搜索用户名 / 昵称</span></label>`,
    body: accounts.length ? `<div class="card table-card"><table>
      <thead><tr><th>用户名</th><th>昵称</th><th>注册</th><th>最近登录</th><th></th></tr></thead>
      <tbody>${accounts.map(a => `<tr>
        <td><b>${esc(a.username)}</b></td>
        <td>${esc(a.nickname) || '—'}</td>
        <td class="dim">${fmtAgo(a.created_at)}</td>
        <td class="dim">${fmtAgo(a.last_login_at)}</td>
        <td><button class="btn text danger" onclick="deleteAccount('${a.id}','${esc(a.username)}')">删除</button></td>
      </tr>`).join('')}</tbody></table></div>`
      : emptyState('👤', search ? '没有匹配的账号' : '还没有通行证账号',
                   search ? '换个关键词试试。' : '玩家在游戏里注册通行证后会出现在这里。'),
  });
}

function deleteAccount(id, username) {
  openDialog({
    title: `删除通行证「${username}」?`,
    body: `<p class="dim">各游戏里的玩家和存档会保留,只是解除与该通行证的绑定。</p>`,
    confirmText: '删除', danger: true,
    onConfirm: async () => {
      await api('DELETE', '/admin/api/accounts/' + id);
      toast('已删除');
      await route();
    },
  });
}
