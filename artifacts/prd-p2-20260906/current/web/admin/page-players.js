// Players in the current game, and a readable view of one player's saves.

const PROVIDER_LABEL = { guest: '游客', password: '账密', sso: '通行证' };

async function renderPlayers(host) {
  const search = window.__playerSearch || '';
  const { players } = await api('GET',
    `/admin/api/games/${state.gameId}/players?search=${encodeURIComponent(search)}`);

  pageShell(host, {
    title: '玩家',
    subtitle: `${state.game.name} 的玩家。点任意一行查看存档。`,
    actions: `<label class="field compact" style="width:260px">
        <input id="pSearch" placeholder=" " value="${esc(search)}"
               onkeydown="if(event.key==='Enter'){window.__playerSearch=this.value;route()}">
        <span class="label">搜索昵称 / 标识</span></label>`,
    body: players.length ? `<div class="card table-card"><table>
        <thead><tr><th>昵称</th><th>登录方式</th><th>标识</th><th>注册</th><th>最近登录</th></tr></thead>
        <tbody>${players.map(p => `<tr class="clickable" onclick="showPlayer('${p.id}','${esc(p.nickname)}')">
          <td><b>${esc(p.nickname) || '(未命名)'}</b>${p.banned ? ' <span class="badge error">封禁</span>' : ''}</td>
          <td><span class="badge">${PROVIDER_LABEL[p.provider] || esc(p.provider)}</span>
              ${p.account_id ? '<span class="badge ok">已绑通行证</span>' : ''}</td>
          <td class="dim"><code>${esc(p.identifier)}</code></td>
          <td class="dim">${fmtAgo(p.created_at)}</td>
          <td class="dim">${fmtAgo(p.last_login_at)}</td>
        </tr>`).join('')}</tbody></table></div>`
      : emptyState('🙋', search ? '没有匹配的玩家' : '还没有玩家',
                   search ? '换个关键词试试。'
                          : `客户端用 App ID <code>${esc(state.game.app_id)}</code> 调 /v1/auth/login 后,玩家会出现在这里。`),
  });
}

// showPlayer renders each save slot as its own labelled block; one big JSON
// dump of every slot is not something you can actually read.
async function showPlayer(id, nickname) {
  const { entries } = await api('GET', `/admin/api/players/${id}/data`);
  openDialog({
    title: `${nickname} 的存档`,
    body: entries.length
      ? entries.map(e => `
          <div class="save-slot">
            <div class="save-head">
              <code>${esc(e.key)}</code>
              <span class="dim">更新于 ${fmtAgo(e.updated_at)}</span>
            </div>
            <pre class="save-body">${esc(JSON.stringify(e.value, null, 2))}</pre>
          </div>`).join('')
      : `<p class="dim">这个玩家还没有任何存档。</p>`,
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}
