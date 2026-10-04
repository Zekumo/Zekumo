// Leaderboards. Boards are created implicitly by the first score submission,
// so this page discovers them rather than offering a "create" action.

async function renderBoards(host) {
  const { boards } = await api('GET', `/admin/api/games/${state.gameId}/leaderboards`);
  const active = window.__board && boards.some(b => b.board === window.__board)
    ? window.__board : boards[0]?.board;

  if (!boards.length) {
    pageShell(host, {
      title: '排行榜',
      subtitle: '榜单由第一次提交分数时自动创建。',
      body: emptyState('🏆', '还没有排行榜',
        '客户端调用 <code>POST /v1/leaderboards/{board}/score</code> 提交分数后,榜单会出现在这里。'),
    });
    return;
  }

  const { entries } = await api('GET',
    `/admin/api/games/${state.gameId}/leaderboards/${encodeURIComponent(active)}?limit=50`);

  pageShell(host, {
    title: '排行榜',
    subtitle: '榜单由第一次提交分数时自动创建。',
    body: `
      <div class="chips">${boards.map(b => `
        <button class="chip ${b.board === active ? 'on' : ''}" onclick="window.__board='${esc(b.board)}';route()">
          ${esc(b.board)} <span class="chip-count">${b.players}</span>
        </button>`).join('')}</div>

      <div class="card table-card">
        <div class="card-head">
          <h2>${esc(active)}</h2>
          <span class="dim">前 ${entries.length} 名</span>
        </div>
        <table>
          <thead><tr><th style="width:80px">名次</th><th>玩家</th><th style="text-align:right">分数</th></tr></thead>
          <tbody>${entries.map(e => `<tr>
            <td>${rankCell(e.rank)}</td>
            <td><b>${esc(e.nickname) || '(未命名)'}</b> <span class="dim"><code>${esc(e.player_id.slice(0, 8))}</code></span></td>
            <td style="text-align:right"><b>${fmtNum(e.score)}</b></td>
          </tr>`).join('')}</tbody>
        </table>
      </div>`,
  });
}

function rankCell(rank) {
  const medal = { 1: 'gold', 2: 'silver', 3: 'bronze' }[rank];
  return medal ? `<span class="rank ${medal}">${rank}</span>` : `<span class="rank">${rank}</span>`;
}
