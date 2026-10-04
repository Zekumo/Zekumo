// Per-game settings: the credentials a client needs, and the two allowlists
// that gate SSO redirects and cloud-function outbound requests.

async function renderSettings(host) {
  const g = state.game;
  pageShell(host, {
    title: '设置',
    subtitle: state.game.name,
    body: `
      <div class="card">
        <h2>接入凭据</h2>
        <p class="dim" style="margin-bottom:16px">App ID 可以放进客户端;App Secret 必须保密,只在服务端使用。</p>
        <div class="kv"><span>App ID</span>${idCell(g.app_id, 'App ID')}</div>
        <div class="kv"><span>App Secret</span>${secretCell(g.app_secret, 'App Secret')}</div>
        <div class="kv"><span>游戏 ID</span>${idCell(g.id, '游戏 ID')}</div>
      </div>

      <div class="card">
        <h2>SSO 回跳白名单</h2>
        <p class="dim" style="margin-bottom:16px">
          托管授权页 <code>/sso/authorize</code> 只会回跳到这里列出的地址。每行一个;
          <b>scheme 与域名须完全一致</b>,仅路径按段前缀匹配 ——
          填 <code>https://good.com</code> 不会放行 <code>https://good.com.evil.com</code>。
          留空则禁用授权页回跳。
        </p>
        <label class="field">
          <textarea id="setSso" placeholder=" " style="min-height:120px">${esc(g.sso_redirect_urls || '')}</textarea>
          <span class="label">回跳地址</span></label>
        <div class="row" style="margin-top:16px"><button class="btn filled" onclick="saveSSO()">保存</button></div>
      </div>

      <div class="card">
        <h2>云函数出站白名单</h2>
        <p class="dim" style="margin-bottom:16px">
          <code>mc.http.fetch</code> 只能访问这里列出的域名,支持 <code>*.example.com</code>。
          内网、环回与云元数据地址在任何情况下都被拦截。
        </p>
        <label class="field">
          <textarea id="setHttp" placeholder=" " style="min-height:100px">${esc(g.func_http_allowlist || '')}</textarea>
          <span class="label">允许的域名</span></label>
        <div class="row" style="margin-top:16px"><button class="btn filled" onclick="saveAllowlist()">保存</button></div>
      </div>

      <div class="card danger-zone">
        <h2>危险操作</h2>
        <div class="row between">
          <span class="dim">删除游戏会连带删除全部玩家、存档、排行榜、剧情、版本与日志。</span>
          <button class="btn filled danger" onclick="deleteGame('${g.id}','${esc(g.name)}')">删除游戏</button>
        </div>
      </div>`,
  });
}

async function saveSSO() {
  try {
    await api('PUT', `/admin/api/games/${state.gameId}/sso`,
      { redirect_urls: document.getElementById('setSso').value });
    await refreshGames();
    toast('已保存');
  } catch (e) { toast('保存失败:' + e.message); }
}

async function saveAllowlist() {
  try {
    await api('PUT', `/admin/api/games/${state.gameId}/http-allowlist`,
      { hosts: document.getElementById('setHttp').value });
    await refreshGames();
    toast('已保存');
  } catch (e) { toast('保存失败:' + e.message); }
}
