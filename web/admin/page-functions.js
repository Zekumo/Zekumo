// Cloud functions: list on the left, editor plus test runner on the right.

const SAMPLE_FN = `// 每日签到:同一天只发一次
const uid = request.player.id;
const today = new Date().toISOString().slice(0, 10);
if (mc.playerdata.get(uid, 'last_signin') === today) ({ ok: false, reason: '今天已签到' })
else {
  mc.playerdata.set(uid, 'last_signin', today);
  const gold = (mc.playerdata.get(uid, 'gold') || 0) + 100;
  mc.playerdata.set(uid, 'gold', gold);
  ({ ok: true, gold })
}`;

async function renderFunctions(host) {
  const { functions } = await api('GET', `/admin/api/games/${state.gameId}/functions`);
  const current = functions.find(f => f.name === window.__fn) || null;

  pageShell(host, {
    title: '云函数',
    subtitle: '服务端 JS(goja 沙箱,10 秒上限)。全局有 request 与 mc.kv / mc.playerdata / mc.players / mc.leaderboard / mc.http。',
    actions: `<button class="btn filled" data-write onclick="newFnDialog()">新建函数</button>`,
    body: functions.length ? `
      <div class="master-detail">
        <div class="card list-card">
          ${functions.map(f => `
            <button class="list-item ${f.name === current?.name ? 'on' : ''}" onclick="openFn('${esc(f.name)}')">
              <div class="list-title">${esc(f.name)}</div>
              <div class="dim">
                ${f.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge">停用</span>'}
                ${f.public ? '<span class="badge">免登录</span>' : ''}
                ${f.cron_secs > 0 ? `<span class="badge">每 ${f.cron_secs}s</span>` : ''}
              </div>
            </button>`).join('')}
        </div>
        <div class="card" id="fnDetail">
          ${current ? '' : emptyState('⚡', '选择一个函数', '从左侧列表里挑一个,或新建。')}
        </div>
      </div>`
      : emptyState('⚡', '还没有云函数',
          '用来做发奖、防作弊校验、定时结算这类服务端逻辑,不用自己部署服务。'),
  });

  if (current) await openFn(current.name);
}

async function openFn(name) {
  const ctx = operationContext();
  window.__fn = name;
  const f = await api('GET', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}`);
  if (!contextCurrent(ctx) || window.__fn !== name) return;
  const host = document.getElementById('fnDetail');
  if (!host) return;
  host.innerHTML = `
    <div class="card-head">
      <h2>${esc(f.name)}</h2>
      <span class="dim">更新于 ${fmtAgo(f.updated_at)}</span>
    </div>
    <div class="row">
      <label class="check"><input type="checkbox" id="fnEnabled" ${f.enabled ? 'checked' : ''}> 启用</label>
      <label class="check"><input type="checkbox" id="fnPublic" ${f.public ? 'checked' : ''}> 免登录可调</label>
      <label class="field compact" style="width:170px">
        <input id="fnCron" type="number" placeholder=" " value="${f.cron_secs}">
        <span class="label">定时(秒,0=关)</span></label>
    </div>
    <label class="field" style="margin-top:16px">
      <textarea id="fnCode" placeholder=" " style="min-height:300px">${esc(f.code)}</textarea>
      <span class="label">函数代码</span></label>
    <div class="row" style="margin-top:16px">
      <button class="btn filled" data-write onclick="saveFn('${esc(f.name)}')">保存</button>
      <button class="btn outlined" data-write onclick="testFn('${esc(f.name)}')">测试运行</button>
      <button class="btn text danger" data-write onclick="deleteFn('${esc(f.name)}')">删除</button>
    </div>
    <div id="fnOut"></div>
    <p class="dim" style="margin-top:20px">
      调用地址:<code>POST /v1/functions/${esc(f.name)}</code>${f.public
        ? `,或免登录 <code>POST /v1/apps/${esc(state.game.app_id)}/functions/${esc(f.name)}</code>` : ''}
    </p>`;
  applySurfaceAccess(host);
}

function fnPayload() {
  return {
    code: document.getElementById('fnCode').value,
    enabled: document.getElementById('fnEnabled').checked,
    public: document.getElementById('fnPublic').checked,
    cron_secs: parseInt(document.getElementById('fnCron').value) || 0,
  };
}

async function saveFn(name) {
  try {
    await api('PUT', `/admin/api/games/${state.gameId}/functions/${encodeURIComponent(name)}`, fnPayload());
    toast('已保存');
    await route();
  } catch (e) { toast('保存失败:' + e.message); }
}

async function testFn(name) {
  const ctx = operationContext();
  const out = document.getElementById('fnOut');
  out.innerHTML = `<div class="dim" style="margin-top:16px">运行中…</div>`;
  try {
    // Save first so the run reflects what is on screen.
    await api('PUT', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}`, fnPayload());
    requireCurrentContext(ctx);
    const res = await api('POST', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/test`, {});
    requireCurrentContext(ctx);
    out.innerHTML = `
      <div class="run-result ok">
        <div class="run-head">返回值</div>
        <pre>${esc(JSON.stringify(res.result, null, 2))}</pre>
        ${res.logs?.length ? `<div class="run-head">console 输出</div><pre>${esc(res.logs.join('\n'))}</pre>` : ''}
      </div>`;
  } catch (e) {
    out.innerHTML = `<div class="run-result err"><div class="run-head">执行失败</div><pre>${esc(e.message)}</pre></div>`;
  }
}

function newFnDialog() {
  const ctx = operationContext();
  openDialog({
    title: '新建云函数',
    body: dlgField('nfName', '函数名(字母、数字、- 和 _)') +
      `<p class="dim" style="margin-top:12px">会预填一个每日签到的示例,保存前可以改。</p>`,
    confirmText: '创建',
    onConfirm: async () => {
      const name = dlgVal('nfName');
      if (!name) throw new Error('请输入函数名');
      requireCurrentContext(ctx);
      await api('PUT', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}`,
        { code: SAMPLE_FN, enabled: true, public: false, cron_secs: 0 });
      requireCurrentContext(ctx);
      window.__fn = name;
      toast('已创建');
      await route();
    },
  });
}

function deleteFn(name) {
  confirmDelete(`函数「${name}」`, async () => {
    await api('DELETE', `/admin/api/games/${state.gameId}/functions/${encodeURIComponent(name)}`);
    window.__fn = null;
    toast('已删除');
    await route();
  });
}
