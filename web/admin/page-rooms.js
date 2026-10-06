// A bounded, read-only snapshot of this game's process-local room lobby.
async function renderRooms(host) {
  const ctx = operationContext();
  let offset = 0;
  let request = 0;
  async function load(nextOffset) {
    const seq = ++request;
    requireCurrentContext(ctx);
    host.innerHTML = `<h1 class="page-title">实时房间</h1>${skeleton(4)}`;
    try {
      const data = await api('GET', `/admin/api/games/${encodeURIComponent(ctx.gameId)}/rooms?offset=${nextOffset}&limit=20`, undefined, { workspaceId: ctx.workspaceId });
      requireCurrentContext(ctx);
      if (seq !== request || !host.isConnected) return;
      offset = data.offset;
      const rooms = data.rooms || [];
      pageShell(host, {
        title: '实时房间',
        subtitle: '当前服务进程的即时快照。房间不会持久保存；刷新查看最新状态。',
        actions: '<button class="btn outlined" data-room-refresh>刷新</button>',
        body: `<div class="card"><div class="card-head"><h2>当前连接</h2><span>${fmtNum(data.online_players)} 位在线玩家 · ${fmtNum(data.room_count)} 个房间</span></div></div>
          ${rooms.length ? `<div class="card table-card"><table><thead><tr><th>房间</th><th>房主</th><th>人数</th><th>加入状态</th></tr></thead><tbody>${rooms.map(room => `<tr>
            <td><b>${esc(room.name) || '(未命名)'}</b><div class="dim"><code>${esc(room.id)}</code></div></td>
            <td><code>${esc(room.owner_id)}</code></td><td>${fmtNum(room.member_count)} / ${fmtNum(room.max_players)}</td>
            <td><span class="badge">${room.locked ? '已锁定' : room.member_count >= room.max_players ? '已满员' : '可加入'}</span></td>
          </tr>`).join('')}</tbody></table></div>` : emptyState('☁️', '没有可显示的房间', '玩家通过 SDK 创建房间后会显示在这里。房间为空后自动移除。')}
          <div class="row"><button class="btn text" data-room-prev ${offset <= 0 ? 'disabled' : ''}>上一页</button>
          <span class="dim">共 ${fmtNum(data.total)} 个房间${rooms.length ? ` · ${offset + 1}–${offset + rooms.length}` : ''}</span>
          <button class="btn text" data-room-next ${offset + data.limit >= data.total ? 'disabled' : ''}>下一页</button></div>`,
      });
      host.querySelector('[data-room-refresh]').onclick = () => load(offset);
      host.querySelector('[data-room-prev]').onclick = () => load(Math.max(0, offset - 20));
      host.querySelector('[data-room-next]').onclick = () => load(offset + data.limit);
    } catch (error) {
      if (seq !== request || !contextCurrent(ctx) || !host.isConnected) return;
      host.innerHTML = errorState('房间加载失败', error.message, false) + '<button class="btn outlined" data-room-retry>重试</button>';
      host.querySelector('[data-room-retry]').onclick = () => load(nextOffset);
    }
  }
  await load(0);
}
