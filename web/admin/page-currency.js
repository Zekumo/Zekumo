// Virtual currencies: the type catalogue (at most 8 per game), manual
// compensation grants, and a lookup of any player's balance and ledger.

const CURRENCY_MAX = 8;

// Ledger rows are the audit trail for money, so the source of each row is
// spelled out rather than shown as a raw enum.
const LEDGER_KIND = {
  grant: '发放', spend: '消费', mail: '邮件奖励', function: '云函数',
};

async function renderCurrency(host) {
  const { currencies } = await api('GET', `/admin/api/games/${state.gameId}/currencies`);
  const rows = currencies || [];
  const full = rows.length >= CURRENCY_MAX;

  pageShell(host, {
    title: '虚拟货币',
    subtitle: `服务端发放与消费，账本按幂等流水号去重。每个游戏最多 ${CURRENCY_MAX} 种货币。`,
    actions: `
      <button class="btn text" onclick="currencyPlayerDialog()">查玩家余额</button>
      <button class="btn filled" data-write onclick="currencyDialog()" ${full ? 'disabled' : ''}>
        ${full ? '已达上限' : '新建货币'}</button>`,
    body: rows.length ? `<div class="card table-card">
        <div class="card-head">
          <h2>货币种类</h2>
          <span class="dim">${rows.length} / ${CURRENCY_MAX}</span>
        </div>
        <table>
          <thead><tr><th>货币</th><th>标识</th><th>货币 ID</th><th>创建</th><th></th></tr></thead>
          <tbody>${rows.map(currencyRow).join('')}</tbody>
        </table>
      </div>`
      : emptyState('¤', '还没有货币',
          '创建第一种货币(如金币、钻石)后,即可用管理接口或 <code>mc.currency.grant</code> 发放。'),
  });
}

function currencyRow(c) {
  return `<tr>
    <td>
      <b>${esc(c.display_name) || esc(c.name)}</b>
      ${c.icon_url ? `<div class="dim">${esc(c.icon_url)}</div>` : ''}
    </td>
    <td><code>${esc(c.name)}</code></td>
    <td>${idCell(c.id, '货币 ID')}</td>
    <td class="dim">${fmtAgo(c.created_at)}</td>
    <td><div class="row">
      <button class="btn text" data-write onclick="grantDialog(${inlineJSON(c)})">发放</button>
      <button class="btn text" data-write onclick="currencyDialog(${inlineJSON(c)})">编辑</button>
      <button class="btn text danger" data-write onclick="deleteCurrency(${inlineJSON(c.id)},${inlineJSON(c.display_name || c.name)})">删除</button>
    </div></td>
  </tr>`;
}

// name is immutable after creation: cloud functions and the ledger reference
// it, so the backend only accepts display_name / icon_url on update.
function currencyDialog(c = null) {
  const editing = Boolean(c);
  openDialog({
    title: editing ? `编辑货币「${c.display_name || c.name}」` : '新建货币',
    body: `${editing
        ? `<div class="kv"><span>标识</span><b><code>${esc(c.name)}</code></b></div>`
        : dlgField('curName', '标识(如 gold,创建后不可改)')}
      ${dlgField('curDisplay', '显示名称', c?.display_name || '')}
      ${dlgField('curIcon', '图标 URL', c?.icon_url || '')}`,
    confirmText: editing ? '保存' : '创建',
    onConfirm: async () => {
      const payload = {
        display_name: dlgVal('curDisplay'),
        icon_url: dlgVal('curIcon'),
      };
      if (!editing) {
        payload.name = dlgVal('curName');
        if (!payload.name) throw new Error('请输入货币标识');
        if (payload.name.length > 32) throw new Error('货币标识最长 32 个字符');
      }
      await api(editing ? 'PUT' : 'POST',
        editing ? `/admin/api/currencies/${c.id}` : `/admin/api/games/${state.gameId}/currencies`,
        payload);
      toast(editing ? '已保存' : '已创建');
      await route();
    },
  });
}

function deleteCurrency(id, name) {
  confirmDelete(`货币「${name}」(连带余额与流水)`, async () => {
    await api('DELETE', `/admin/api/currencies/${id}`);
    toast('已删除');
    await route();
  });
}

// ---------- manual grant ----------

// The idempotency key is what makes a retry safe, so it is prefilled rather
// than left to the operator: an empty key would be rejected by the server.
function grantDialog(c) {
  openDialog({
    title: `发放「${c.display_name || c.name}」`,
    body: `${dlgField('grantPlayer', '玩家 ID')}
      ${dlgField('grantAmount', '数量(正整数)', '', 'number')}
      ${dlgField('grantKey', '幂等流水号', newIdempotencyKey())}
      ${dlgField('grantNote', '备注(如活动补偿)', '')}
      <p class="dim" style="margin-top:12px">同一流水号重复提交不会重复发放。</p>`,
    confirmText: '发放',
    onConfirm: async () => {
      const playerId = dlgVal('grantPlayer');
      if (!playerId) throw new Error('请输入玩家 ID');
      const amount = Number(dlgVal('grantAmount'));
      if (!Number.isInteger(amount) || amount <= 0) throw new Error('数量必须是正整数');
      const key = dlgVal('grantKey');
      if (!key) throw new Error('请填写幂等流水号');
      const res = await api('POST',
        `/admin/api/games/${state.gameId}/currency/${c.id}/grant`,
        { player_id: playerId, amount, idempotency_key: key, note: dlgVal('grantNote') });
      toast(res.duplicate
        ? `该流水号已发放过,余额 ${fmtNum(res.balance)}`
        : `已发放,余额 ${fmtNum(res.balance)}`);
    },
  });
}

function newIdempotencyKey() {
  const rand = Math.random().toString(36).slice(2, 8);
  return `admin-${Date.now()}-${rand}`;
}

// ---------- per-player lookup ----------

function currencyPlayerDialog() {
  openDialog({
    title: '查玩家余额',
    body: dlgField('balPlayer', '玩家 ID') +
      `<p class="dim" style="margin-top:12px">玩家 ID 可在「玩家」页复制。</p>`,
    confirmText: '查询',
    onConfirm: async () => {
      const playerId = dlgVal('balPlayer');
      if (!playerId) throw new Error('请输入玩家 ID');
      const { balances } = await api('GET', `/admin/api/players/${playerId}/currency`);
      showPlayerBalances(playerId, balances || []);
    },
  });
}

function showPlayerBalances(playerId, balances) {
  openDialog({
    title: '玩家余额',
    body: `<div class="kv"><span>玩家 ID</span><b><code>${esc(playerId)}</code></b></div>
      ${balances.length
        ? balances.map(b => `<div class="kv">
            <span>${esc(b.display_name) || esc(b.name)}</span>
            <b>${fmtNum(b.balance)}
              <button class="btn text" onclick="showPlayerLedger(${inlineJSON(playerId)},${inlineJSON(b)})">流水</button>
            </b></div>`).join('')
        : `<p class="dim">这个游戏还没有定义货币。</p>`}`,
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}

async function showPlayerLedger(playerId, currency) {
  const { entries } = await api('GET',
    `/admin/api/players/${playerId}/currency/${currency.currency_id}/ledger?limit=50`);
  const rows = entries || [];
  openDialog({
    title: `${currency.display_name || currency.name} · 流水`,
    body: rows.length ? `<div class="table-card"><table>
        <thead><tr><th>时间</th><th>来源</th><th style="text-align:right">变动</th><th style="text-align:right">余额</th><th>备注</th></tr></thead>
        <tbody>${rows.map(e => `<tr>
          <td class="dim">${fmtTime(e.created_at)}</td>
          <td><span class="badge">${esc(LEDGER_KIND[e.kind] || e.kind)}</span></td>
          <td style="text-align:right"><b class="amount ${e.amount < 0 ? 'down' : 'up'}">${e.amount > 0 ? '+' : ''}${fmtNum(e.amount)}</b></td>
          <td style="text-align:right">${fmtNum(e.balance_after)}</td>
          <td class="dim">${esc(e.note)}</td>
        </tr>`).join('')}</tbody>
      </table></div>`
      : `<p class="dim">还没有流水记录。</p>`,
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}
