// Player bans: the audit list for a game, plus the ban / unban actions the
// players page links into. A ban also invalidates tokens already issued, so
// it takes effect without waiting for the player to log in again.

async function renderBans(host) {
  const { bans } = await api('GET', `/admin/api/games/${state.gameId}/bans`);
  const rows = bans || [];

  pageShell(host, {
    title: '封禁记录',
    subtitle: '封禁立即生效：已签发的令牌失效，封禁期内登录返回 403。',
    actions: `<button class="btn filled" data-write onclick="banDialog()">封禁玩家</button>`,
    body: rows.length ? `<div class="card table-card"><table>
        <thead><tr><th>玩家</th><th>原因</th><th>时长</th><th>状态</th><th>操作者</th><th>封禁时间</th><th></th></tr></thead>
        <tbody>${rows.map(banRow).join('')}</tbody>
      </table></div>`
      : emptyState('🚫', '没有封禁记录', '在「玩家」页或这里封禁违规玩家,记录会留在这份清单里。'),
  });
}

// A ban row has three possible outcomes, and they are not interchangeable:
// manually lifted, expired on its own, or still in force.
function banState(b) {
  if (b.lifted_at) return { label: `已解禁 ${fmtAgo(b.lifted_at)}`, cls: '', active: false };
  if (b.expires_at && new Date(b.expires_at) <= new Date()) {
    return { label: '已到期', cls: '', active: false };
  }
  return { label: '封禁中', cls: 'error', active: true };
}

function banRow(b) {
  const st = banState(b);
  return `<tr>
    <td><b>${esc(b.nickname) || '(未命名)'}</b><div class="dim"><code>${esc(b.player_id)}</code></div></td>
    <td>${esc(b.reason)}</td>
    <td class="dim">${b.expires_at ? `至 ${fmtTime(b.expires_at)}` : '永久'}</td>
    <td><span class="badge ${st.cls}">${st.label}</span></td>
    <td class="dim">${esc(b.operator) || '—'}</td>
    <td class="dim">${fmtAgo(b.created_at)}</td>
    <td>${st.active
      ? `<button class="btn text" data-write onclick="unbanPlayer(${inlineJSON(b.player_id)},${inlineJSON(b.nickname)})">解禁</button>`
      : ''}</td>
  </tr>`;
}

// Durations are offered as presets because typing a second count is where
// operators get an order of magnitude wrong.
const BAN_DURATIONS = [
  ['3600', '1 小时'], ['86400', '1 天'], ['604800', '7 天'],
  ['2592000', '30 天'], ['0', '永久'],
];

function banDialog(playerId = '', nickname = '') {
  openDialog({
    title: nickname ? `封禁「${nickname}」` : '封禁玩家',
    body: `${playerId
        ? `<div class="kv"><span>玩家 ID</span><b><code>${esc(playerId)}</code></b></div>`
        : dlgField('banPlayer', '玩家 ID')}
      ${dlgField('banReason', '封禁原因(必填)')}
      <label class="field"><select id="banDuration">
        ${BAN_DURATIONS.map(([secs, label]) =>
          `<option value="${secs}">${label}</option>`).join('')}
      </select><span class="label">封禁时长</span></label>`,
    confirmText: '封禁', danger: true,
    onConfirm: async () => {
      const id = playerId || dlgVal('banPlayer');
      if (!id) throw new Error('请输入玩家 ID');
      const reason = dlgVal('banReason');
      if (!reason) throw new Error('请填写封禁原因');
      await api('POST', `/admin/api/players/${id}/ban`, {
        reason, duration_secs: Number(document.getElementById('banDuration').value),
      });
      toast('已封禁');
      await route();
    },
  });
}

function unbanPlayer(playerId, nickname) {
  openDialog({
    title: `解禁「${nickname || playerId}」?`,
    body: `<p class="dim">解禁后该玩家可以立即重新登录。</p>`,
    confirmText: '解禁',
    onConfirm: async () => {
      await api('POST', `/admin/api/players/${playerId}/unban`, {});
      toast('已解禁');
      await route();
    },
  });
}
