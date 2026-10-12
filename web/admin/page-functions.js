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
    subtitle: '服务端 JS(goja 沙箱,10 秒上限)。全局有 request 与 mc.kv / mc.playerdata / mc.players / mc.leaderboard / mc.currency / mc.achievements / mc.http。',
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
  const { draft } = await api('GET', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/draft`);
  if (!contextCurrent(ctx) || window.__fn !== name) return;
  const { schedule } = await api('GET', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/schedule`);
  const editor = draft || f;
  if (!contextCurrent(ctx) || window.__fn !== name) return;
  const host = document.getElementById('fnDetail');
  if (!host) return;
  host.dataset.revision = String(f.revision);
  host.dataset.draftRevision = String(draft?.revision || 0);
  host.innerHTML = `
    <div class="card-head">
      <h2>${esc(f.name)} · v${f.revision}</h2>
      <span class="dim">更新于 ${fmtAgo(f.updated_at)}</span>
    </div>
    <div class="row">
      <label class="check"><input type="checkbox" id="fnEnabled" ${editor.enabled ? 'checked' : ''}> 启用</label>
      <label class="check"><input type="checkbox" id="fnPublic" ${editor.public ? 'checked' : ''}> 免登录可调</label>
      <label class="field compact" style="width:170px">
        <input id="fnCron" type="number" placeholder=" " value="${editor.cron_secs}">
        <span class="label">定时(秒,0=关)</span></label>
    </div>
    <label class="field" style="margin-top:16px">
      <textarea id="fnCode" placeholder=" " style="min-height:300px">${esc(editor.code)}</textarea>
      <span class="label">函数代码</span></label>
    <div class="row" style="margin-top:16px">
      <button class="btn filled" data-write onclick="saveFn('${esc(f.name)}')">发布当前草稿</button>
      <button class="btn outlined" data-write onclick="saveFnDraft('${esc(f.name)}')">保存草稿</button>
      <button class="btn text" data-write onclick="discardFnDraft('${esc(f.name)}')">丢弃草稿</button>
      <button class="btn outlined" data-write onclick="testFn('${esc(f.name)}')">测试草稿</button>
      <button class="btn outlined" onclick="showFnHistory('${esc(f.name)}')">版本历史</button>
      <button class="btn text danger" data-write onclick="deleteFn('${esc(f.name)}')">删除</button>
    </div>
    <p class="dim">${draft ? '已载入共享草稿；线上版本保持不变。' : '尚无已保存草稿。'}测试草稿不会发布代码，但会读写真实游戏数据。仅最近 50 个版本保留。</p>
    <label class="field"><input id="fnTestPlayer" placeholder=" "><span class="label">测试玩家 ID（可选，仅当前游戏）</span></label>
    <label class="field"><textarea id="fnTestBody">{}</textarea><span class="label">测试请求 JSON</span></label>
    <p class="dim">${schedule ? `下次计划 ${esc(fmtTime(schedule.next_run_at))} · 已调度 ${fmtNum(schedule.dispatch_count)} 次${schedule.last_dispatched_at ? ` · 最近调度 ${esc(fmtTime(schedule.last_dispatched_at))}` : ''}` : '定时调度未开启'}</p>
    <div id="fnHistory"></div><div id="fnOut"></div>
    <p class="dim" style="margin-top:20px">
      调用地址:<code>POST /v1/functions/${esc(f.name)}</code>${f.public
        ? `,或免登录 <code>POST /v1/apps/${esc(state.game.app_id)}/functions/${esc(f.name)}</code>` : ''}
    </p>`;
  applySurfaceAccess(host);
}

function fnPayload() {
  return {
    expected_revision: Number(document.getElementById('fnDetail').dataset.revision),
    code: document.getElementById('fnCode').value,
    enabled: document.getElementById('fnEnabled').checked,
    public: document.getElementById('fnPublic').checked,
    cron_secs: parseInt(document.getElementById('fnCron').value) || 0,
  };
}

async function persistFnDraft(name, ctx) {
  const host = document.getElementById('fnDetail');
  const payload = fnPayload();
  payload.expected_revision = Number(host.dataset.draftRevision);
  requireCurrentContext(ctx);
  const draft = await api('PUT', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/draft`, payload);
  requireCurrentContext(ctx);
  if (window.__fn !== name || !host.isConnected) throw new Error('函数编辑器已切换');
  host.dataset.draftRevision = String(draft.revision);
  return draft;
}

async function saveFnDraft(name) {
  const ctx = operationContext();
  try { await persistFnDraft(name,ctx); toast('草稿已保存，尚未发布'); }
  catch (e) { if (contextCurrent(ctx)) toast(e.message); }
}

async function saveFn(name) {
  const ctx = operationContext();
  const expected = Number(document.getElementById('fnDetail').dataset.revision);
  try {
    const draft = await persistFnDraft(name,ctx);
    await api('POST', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/publish`,
      { expected_revision: expected, draft_revision: draft.revision });
    requireCurrentContext(ctx);
    toast('已发布为新版本');
    await route();
  } catch (e) { if (contextCurrent(ctx)) toast('发布失败:' + e.message); }
}

function discardFnDraft(name) {
  const ctx = operationContext();
  const expected = Number(document.getElementById('fnDetail').dataset.draftRevision);
  if (!expected) { toast('没有已保存的草稿'); return; }
  openDialog({
    title: '丢弃共享草稿', body: '<p>已保存的草稿将被删除，线上函数保持不变。</p>', confirmText: '丢弃',
    onConfirm: async () => {
      requireCurrentContext(ctx);
      await api('DELETE', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/draft`, { expected_revision: expected });
      requireCurrentContext(ctx);
      await openFn(name);
    },
  });
}

async function testFn(name) {
  const ctx = operationContext();
  const out = document.getElementById('fnOut');
  out.innerHTML = `<div class="dim" style="margin-top:16px">运行中…</div>`;
  try {
    const body = JSON.parse(document.getElementById('fnTestBody').value);
    const code = document.getElementById('fnCode').value;
    const player_id = document.getElementById('fnTestPlayer').value.trim();
    requireCurrentContext(ctx);
    const res = await api('POST', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/preview`, { code, body, player_id });
    requireCurrentContext(ctx);
    if (window.__fn !== name || !out.isConnected) return;
    out.innerHTML = `
      <div class="run-result ok">
        <div class="run-head">返回值</div>
        <pre>${esc(JSON.stringify(res.result, null, 2))}</pre>
        ${res.logs?.length ? `<div class="run-head">console 输出</div><pre>${esc(res.logs.join('\n'))}</pre>` : ''}
      </div>`;
  } catch (e) {
    if (!contextCurrent(ctx) || window.__fn !== name || !out.isConnected) return;
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
        { code: SAMPLE_FN, enabled: false, public: false, cron_secs: 0, expected_revision: 0 });
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


async function showFnHistory(name) {
  const ctx = operationContext();
  const host = document.getElementById('fnHistory');
  try {
    const { versions } = await api('GET', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/versions`);
    if (!contextCurrent(ctx) || window.__fn !== name || !host.isConnected) return;
    host.innerHTML = versions.map(v => `<div class="row">
      <span>v${v.revision} · ${esc(fmtTime(v.created_at))}</span>
      <button class="btn text" onclick="loadFnVersion('${esc(name)}', ${v.revision})">载入草稿</button>
      <button class="btn text" data-write onclick="restoreFnVersion('${esc(name)}', ${v.revision})">恢复发布</button>
    </div>`).join('');
    applySurfaceAccess(host);
  } catch (e) { if (contextCurrent(ctx)) toast(e.message); }
}

async function loadFnVersion(name, revision) {
  const ctx = operationContext();
  const host = document.getElementById('fnDetail');
  try {
    const v = await api('GET', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/versions/${revision}`);
    if (!contextCurrent(ctx) || window.__fn !== name || !host.isConnected) return;
    document.getElementById('fnCode').value = v.code || '';
    document.getElementById('fnEnabled').checked = v.enabled;
    document.getElementById('fnPublic').checked = v.public;
    document.getElementById('fnCron').value = v.cron_secs;
    toast('历史版本已载入草稿，尚未发布');
  } catch (e) { if (contextCurrent(ctx)) toast(e.message); }
}

function restoreFnVersion(name, revision) {
  const ctx = operationContext();
  const expected = Number(document.getElementById('fnDetail').dataset.revision);
  openDialog({
    title: `恢复版本 v${revision}`,
    body: '<p>将立即发布此版本的代码、启用状态、公开访问和定时设置。当前代码保留在版本历史中。</p>',
    confirmText: '恢复发布',
    onConfirm: async () => {
      requireCurrentContext(ctx);
      await api('POST', `/admin/api/games/${ctx.gameId}/functions/${encodeURIComponent(name)}/versions/${revision}/restore`,
        { expected_revision: expected });
      requireCurrentContext(ctx);
      toast('已恢复为新版本');
      await route();
    },
  });
}
