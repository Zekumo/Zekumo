// In-game mail: send a broadcast or a targeted batch, optionally attaching
// currency rewards, and review delivery / read / claim rates afterwards.

const MAIL_MAX_RECIPIENTS = 1000;
const MAIL_DEFAULT_DAYS = 30;

async function renderMail(host) {
  const ctx = operationContext();
  // The reward picker needs the currency catalogue, so both load together.
  const [{ mail }, { currencies }] = await Promise.all([
    api('GET', `/admin/api/games/${ctx.gameId}/mail`),
    api('GET', `/admin/api/games/${ctx.gameId}/currencies`),
  ]);
  if (!contextCurrent(ctx)) return;
  const rows = mail || [];
  window.__mailCurrencies = currencies || [];

  pageShell(host, {
    title: '游戏内邮件',
    subtitle: `向全服或指定玩家发送系统邮件，可附带货币奖励；默认 ${MAIL_DEFAULT_DAYS} 天有效。`,
    actions: `<button class="btn filled" data-write onclick="mailDialog()">发送邮件</button>`,
    body: rows.length ? `<div class="card table-card"><table>
        <thead><tr><th>邮件</th><th>投递</th><th>附件</th><th>已读</th><th>领取率</th><th>有效期</th><th></th></tr></thead>
        <tbody>${rows.map(mailRow).join('')}</tbody>
      </table></div>`
      : emptyState('✉', '还没有邮件', '发送第一封邮件,用于活动补偿或全服福利。'),
  });
}

function mailRow(m) {
  const rewards = mailRewards(m);
  // Broadcast mail has no recipient rows until a player interacts with it,
  // so a claim *rate* is only meaningful for a targeted send.
  const claimRate = m.broadcast
    ? `${fmtNum(m.claimed)} 人`
    : `${pct(m.claimed, m.recipients)} <span class="dim">(${fmtNum(m.claimed)}/${fmtNum(m.recipients)})</span>`;
  const expired = new Date(m.expires_at) <= new Date();
  return `<tr>
    <td><b>${esc(m.title)}</b><div class="dim">${fmtAgo(m.created_at)}</div></td>
    <td>${m.broadcast
      ? '<span class="badge ok">全服广播</span>'
      : `<span class="badge">定向 ${fmtNum(m.recipients)} 人</span>`}</td>
    <td class="dim">${rewards.length ? rewards.map(r =>
      `${esc(currencyLabel(r.currency_id))} ×${fmtNum(r.amount)}`).join('、') : '无'}</td>
    <td class="dim">${fmtNum(m.read_count)}</td>
    <td>${claimRate}</td>
    <td class="dim">${expired ? '<span class="badge">已过期</span>' : fmtUntil(m.expires_at)}</td>
    <td><button class="btn text" onclick="previewMail(${inlineJSON(m)})">查看</button></td>
  </tr>`;
}

// rewards arrives as JSON from the API and may be null for older rows.
function mailRewards(m) {
  return Array.isArray(m.rewards) ? m.rewards : [];
}

function currencyLabel(id) {
  const c = (window.__mailCurrencies || []).find(x => x.id === id);
  return c ? (c.display_name || c.name) : id;
}

function mailDialog() {
  const currencies = window.__mailCurrencies || [];
  openDialog({
    title: '发送邮件',
    body: `${dlgField('mailTitle', '标题')}
      <label class="field"><textarea id="mailBody" placeholder=" "></textarea><span class="label">正文</span></label>
      <label class="check"><input id="mailBroadcast" type="checkbox" checked onchange="toggleMailRecipients()"> 全服广播</label>
      <label class="field" id="mailRecipientsField" hidden>
        <textarea id="mailRecipients" placeholder=" "></textarea>
        <span class="label">收件玩家 ID(每行一个,最多 ${MAIL_MAX_RECIPIENTS} 个)</span></label>
      ${dlgField('mailExpires', `有效期天数(留空=${MAIL_DEFAULT_DAYS} 天)`, '', 'number')}
      <div class="card-head" style="margin:20px 0 8px"><h2 style="font-size:15px">货币奖励</h2></div>
      ${currencies.length
        ? currencies.map(c => `<div class="kv">
            <span>${esc(c.display_name || c.name)}</span>
            <input class="reward-input" type="number" min="0" id="reward_${esc(c.id)}" placeholder="0">
          </div>`).join('')
        : `<p class="dim">这个游戏还没有定义货币,邮件将不带附件。</p>`}`,
    confirmText: '发送',
    onConfirm: async () => {
      const title = dlgVal('mailTitle');
      if (!title) throw new Error('请输入邮件标题');

      const broadcast = document.getElementById('mailBroadcast').checked;
      let recipients = 'all';
      if (!broadcast) {
        recipients = document.getElementById('mailRecipients').value
          .split('\n').map(s => s.trim()).filter(Boolean);
        if (!recipients.length) throw new Error('请填写至少一个收件玩家 ID');
        if (recipients.length > MAIL_MAX_RECIPIENTS) {
          throw new Error(`一次最多 ${MAIL_MAX_RECIPIENTS} 个收件人`);
        }
      }

      const rewards = [];
      for (const c of currencies) {
        const amount = Number(document.getElementById(`reward_${c.id}`).value);
        if (!amount) continue;
        if (!Number.isInteger(amount) || amount <= 0) {
          throw new Error(`「${c.display_name || c.name}」的奖励数量必须是正整数`);
        }
        rewards.push({ currency_id: c.id, amount });
      }

      const payload = { title, body: document.getElementById('mailBody').value, recipients, rewards };
      const days = dlgVal('mailExpires');
      if (days) {
        const n = Number(days);
        if (!Number.isInteger(n) || n <= 0) throw new Error('有效期天数必须是正整数');
        payload.expires_in = n * 86400;
      }

      const res = await api('POST', `/admin/api/games/${state.gameId}/mail`, payload);
      toast(broadcast ? '已全服发送' : `已投递 ${fmtNum(res.delivered)} 人`);
      await route();
    },
  });
  toggleMailRecipients();
}

function toggleMailRecipients() {
  const broadcast = document.getElementById('mailBroadcast');
  const field = document.getElementById('mailRecipientsField');
  if (broadcast && field) field.hidden = broadcast.checked;
}

function previewMail(m) {
  const rewards = mailRewards(m);
  openDialog({
    title: `邮件 · ${m.title}`,
    body: `<div class="announcement-preview"><h3>${esc(m.title)}</h3>
        <p>${esc(m.body).replace(/\n/g, '<br>') || '(无正文)'}</p></div>
      <div class="kv"><span>附件</span><b>${rewards.length
        ? rewards.map(r => `${esc(currencyLabel(r.currency_id))} ×${fmtNum(r.amount)}`).join('、')
        : '无'}</b></div>
      <div class="kv"><span>投递</span><b>${m.broadcast ? '全服广播' : `定向 ${fmtNum(m.recipients)} 人`}</b></div>
      <div class="kv"><span>已读 / 已领取</span><b>${fmtNum(m.read_count)} / ${fmtNum(m.claimed)}</b></div>
      <div class="kv"><span>过期时间</span><b>${fmtTime(m.expires_at)}</b></div>`,
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}
