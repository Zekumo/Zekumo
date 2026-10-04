// WebHooks: endpoints plus their recent delivery history, which is what you
// actually need when one stops working.

async function renderWebhooks(host) {
  const data = await api('GET', `/admin/api/games/${state.gameId}/webhooks`);
  pageShell(host, {
    title: 'WebHook',
    subtitle: `平台事件实时推送到你的地址,失败重试 3 次。请求头带 HMAC-SHA256 签名,收到后务必验签。`,
    actions: `<button class="btn filled" onclick="newHookDialog(${JSON.stringify(data.known_events).replace(/"/g, '&quot;')})">添加 WebHook</button>`,
    body: data.webhooks.length ? `
      ${data.webhooks.map(h => `
        <div class="card">
          <div class="card-head">
            <h2 class="hook-url">${esc(h.url)}</h2>
            <span class="badge ${h.enabled ? 'ok' : ''}">${h.enabled ? '启用' : '停用'}</span>
          </div>
          <div class="kv"><span>订阅事件</span><b>${h.events === '*' ? '全部' : esc(h.events)}</b></div>
          <div class="kv"><span>签名密钥</span>${secretCell(h.secret, 'Secret')}</div>
          <div class="row" style="margin-top:16px">
            <button class="btn outlined" onclick="testHook('${h.id}')">发送测试事件</button>
            <button class="btn text" onclick="showDeliveries('${h.id}','${esc(h.url)}')">投递记录</button>
            <button class="btn text danger" onclick="deleteHook('${h.id}')">删除</button>
          </div>
        </div>`).join('')}
      <p class="dim">可订阅事件:${data.known_events.map(e => `<code>${esc(e)}</code>`).join(' ')}</p>`
      : emptyState('🔔', '还没有 WebHook',
          '玩家注册、登录、提交分数、发布版本时,平台可以把事件推送到你的服务。'),
  });
}

function newHookDialog(events) {
  openDialog({
    title: '添加 WebHook',
    body: dlgField('nhUrl', '接收地址(https://…)') +
      dlgField('nhEvents', '订阅事件(逗号分隔,留空=全部)') +
      `<p class="dim" style="margin-top:12px">可用:${(events || []).map(e => `<code>${esc(e)}</code>`).join(' ')}</p>
       <p class="dim">内网地址不可达 —— 否则 WebHook 会变成内网探测器。</p>`,
    confirmText: '添加',
    onConfirm: async () => {
      const url = dlgVal('nhUrl');
      if (!url) throw new Error('请输入接收地址');
      await api('POST', `/admin/api/games/${state.gameId}/webhooks`, { url, events: dlgVal('nhEvents') });
      toast('已添加');
      await route();
    },
  });
}

async function testHook(id) {
  try {
    const res = await api('POST', `/admin/api/webhooks/${id}/test`, {});
    toast(res.ok ? `测试成功(HTTP ${res.status})` : `测试失败(${res.status || '连接失败'})`);
  } catch (e) { toast('测试失败:' + e.message); }
}

async function showDeliveries(id, url) {
  const { deliveries } = await api('GET', `/admin/api/webhooks/${id}/deliveries`);
  openDialog({
    title: '投递记录',
    body: `<p class="dim" style="margin-bottom:12px">${esc(url)}</p>` + (deliveries.length
      ? `<table><thead><tr><th>事件</th><th>HTTP</th><th>尝试</th><th>结果</th><th>时间</th></tr></thead>
         <tbody>${deliveries.map(d => `<tr>
           <td><code>${esc(d.event)}</code></td>
           <td>${d.status || '—'}</td>
           <td>${d.attempts}</td>
           <td><span class="badge ${d.ok ? 'ok' : 'error'}">${d.ok ? '成功' : '失败'}</span></td>
           <td class="dim">${fmtAgo(d.created_at)}</td>
         </tr>`).join('')}</tbody></table>`
      : `<p class="dim">还没有投递记录。</p>`),
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}

function deleteHook(id) {
  confirmDelete('这个 WebHook', async () => {
    await api('DELETE', '/admin/api/webhooks/' + id);
    toast('已删除');
    await route();
  });
}
