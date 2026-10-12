// Durable server-authored match records, separate from the process-local lobby.
async function renderMatches(host) {
  const ctx = operationContext();
  const cursors = [''];
  let page = 0;
  let request = 0;
  async function load() {
    const seq = ++request;
    const before = cursors[page];
    try {
      requireCurrentContext(ctx);
      const data = await api('GET', `/admin/api/games/${ctx.gameId}/matches?limit=20${before ? `&before_id=${encodeURIComponent(before)}` : ''}`);
      if (!contextCurrent(ctx) || !host.isConnected || request !== seq) return;
      pageShell(host, {
        title: '对局记录',
        subtitle: '持久保存的服务器记录。仅管理员或云函数可以写入；参与玩家只读。房间关闭或服务重启不会删除记录。',
        actions: '<button class="btn outlined" data-match-refresh>刷新</button><button class="btn filled" data-write onclick="newMatchDialog()">创建对局</button>',
        body: data.matches.length ? `<div class="card table-card"><table><thead><tr><th>对局</th><th>状态</th><th>时间</th><th>操作</th></tr></thead><tbody>${data.matches.map(m => `<tr>
          <td><b>${esc(m.match_key)}</b><div class="dim">#${esc(m.id)} · ${esc(m.source)}</div></td>
          <td><span class="badge">${esc(m.status)}</span> v${m.revision}</td>
          <td>${esc(fmtTime(m.created_at))}${m.finished_at ? `<div class="dim">结束 ${esc(fmtTime(m.finished_at))}</div>` : ''}</td>
          <td><button class="btn text" onclick="showMatchRecord(${inlineJSON(m.id)})">详情</button>
          ${m.status === 'active' ? `<button class="btn text" data-write onclick="finishMatchDialog(${inlineJSON(m.id)},${m.revision},false)">提交结果</button><button class="btn text danger" data-write onclick="finishMatchDialog(${inlineJSON(m.id)},${m.revision},true)">取消</button>` : ''}</td>
        </tr>`).join('')}</tbody></table></div>` : emptyState('🎮', '没有对局记录', '由可信云函数或管理 API 创建对局并写入结果。'),
      });
      host.insertAdjacentHTML('beforeend', `<div class="row"><button class="btn text" data-match-prev ${page === 0 ? 'disabled' : ''}>上一页</button><span>第 ${page + 1} 页</span><button class="btn text" data-match-next ${!data.next_before_id ? 'disabled' : ''}>下一页</button></div>`);
      host.querySelector('[data-match-prev]').onclick = () => { if (page > 0) { page--; load(); } };
      host.querySelector('[data-match-next]').onclick = () => { if (data.next_before_id) { cursors[++page] = data.next_before_id; load(); } };
      host.querySelector('[data-match-refresh]').onclick = () => { page = 0; cursors.length = 1; load(); };
      applySurfaceAccess(host);
    } catch (e) { if (contextCurrent(ctx) && host.isConnected && request === seq) host.innerHTML = errorState('对局加载失败', e.message, false); }
  }
  await load();
}

function newMatchDialog() {
  const ctx = operationContext();
  openDialog({
    title: '创建服务器对局',
    body: dlgField('matchKey', '唯一对局键（幂等）') +
      '<label class="field"><textarea id="matchPlayers"></textarea><span class="label">参与玩家 UUID，每行一个（1–200）</span></label>' +
      '<label class="field"><textarea id="matchMetadata">{}</textarea><span class="label">元数据 JSON（最多 8 KiB）</span></label>',
    confirmText: '创建',
    onConfirm: async () => {
      const match_key = dlgVal('matchKey');
      const player_ids = dlgVal('matchPlayers').split(/[\s,]+/).filter(Boolean);
      const metadata = JSON.parse(dlgVal('matchMetadata'));
      requireCurrentContext(ctx);
      await api('POST', `/admin/api/games/${ctx.gameId}/matches`, { match_key, player_ids, metadata });
      requireCurrentContext(ctx);
      toast('对局已创建');
      await route();
    },
  });
}

async function showMatchRecord(id) {
  const ctx = operationContext();
  try {
    const m = await api('GET', `/admin/api/games/${ctx.gameId}/matches/${encodeURIComponent(id)}`);
    requireCurrentContext(ctx);
    openDialog({ title: `对局 ${m.match_key}`, body: `<pre style="white-space:pre-wrap;overflow-wrap:anywhere">${esc(JSON.stringify(m, null, 2))}</pre>`, confirmText: '关闭', onConfirm: async () => {} });
  } catch (e) { if (contextCurrent(ctx)) toast(e.message); }
}

function finishMatchDialog(id, revision, cancel) {
  const ctx = operationContext();
  openDialog({
    title: cancel ? '取消对局' : '提交权威对局结果',
    body: '<p>终态记录不可修改。结果对所有参与玩家可见；只提交经过服务器验证的信息。</p>' +
      (cancel ? dlgField('matchReason', '取消原因') : '<label class="field"><textarea id="matchResult">{}</textarea><span class="label">结果 JSON（最多 64 KiB）</span></label>'),
    confirmText: cancel ? '确认取消' : '提交结果',
    onConfirm: async () => {
      const result = cancel ? { reason: dlgVal('matchReason') } : JSON.parse(dlgVal('matchResult'));
      requireCurrentContext(ctx);
      await api('POST', `/admin/api/games/${ctx.gameId}/matches/${encodeURIComponent(id)}/finish`,
        { expected_revision: revision, status: cancel ? 'cancelled' : 'completed', result });
      requireCurrentContext(ctx);
      toast('终态记录已保存');
      await route();
    },
  });
}
