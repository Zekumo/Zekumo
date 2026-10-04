// Dialogue scripts: a list on the left, the editor on the right, so you can
// see what exists while editing one of them.

async function renderContent(host) {
  const { scripts } = await api('GET', `/admin/api/games/${state.gameId}/dialogues`);
  // Keep the previously opened script selected if it still exists.
  const current = scripts.find(s => s.script_key === window.__script) || null;
  const key = current?.script_key || null;

  pageShell(host, {
    title: '剧情对话',
    subtitle: '控制台编辑,客户端按 key 拉取。内容是任意 JSON,结构由游戏自定。',
    actions: `<button class="btn filled" onclick="editScript(null)">新建脚本</button>`,
    body: scripts.length ? `
      <div class="master-detail">
        <div class="card list-card">
          ${scripts.map(s => `
            <button class="list-item ${s.script_key === key ? 'on' : ''}" onclick="openScript('${esc(s.script_key)}')">
              <div class="list-title">${esc(s.script_key)}</div>
              <div class="dim">${esc(s.title) || '无标题'} · v${s.version} · ${fmtAgo(s.updated_at)}</div>
            </button>`).join('')}
        </div>
        <div class="card" id="scriptDetail">
          ${current ? '' : emptyState('📖', '选择一个脚本', '从左侧列表里挑一个,或新建。')}
        </div>
      </div>`
      : emptyState('📖', '还没有剧情脚本', '新建一个脚本,客户端就能用 <code>GET /v1/dialogues/{key}</code> 拉取。'),
  });

  if (current) await openScript(current.script_key);
}

async function openScript(key) {
  window.__script = key;
  const s = await api('GET', `/admin/api/games/${state.gameId}/dialogues/${encodeURIComponent(key)}`);
  document.querySelectorAll('.list-item').forEach(el =>
    el.classList.toggle('on', el.textContent.trim().startsWith(key)));
  const host = document.getElementById('scriptDetail');
  if (!host) return;
  host.innerHTML = `
    <div class="card-head">
      <h2>${esc(s.script_key)}</h2>
      <span class="badge">v${s.version}</span>
    </div>
    <label class="field"><input id="scTitle" placeholder=" " value="${esc(s.title)}">
      <span class="label">标题</span></label>
    <label class="field" style="margin-top:16px">
      <textarea id="scContent" placeholder=" " style="min-height:320px">${esc(JSON.stringify(s.content, null, 2))}</textarea>
      <span class="label">内容 JSON</span></label>
    <div class="row" style="margin-top:16px">
      <button class="btn filled" onclick="saveScript('${esc(s.script_key)}')">保存</button>
      <button class="btn text danger" onclick="deleteScript('${esc(s.script_key)}')">删除</button>
      <span class="dim">更新于 ${fmtTime(s.updated_at)}</span>
    </div>`;
}

function editScript() {
  openDialog({
    title: '新建剧情脚本',
    body: dlgField('nsKey', '脚本 key(如 chapter1.intro)') + dlgField('nsTitle', '标题'),
    confirmText: '创建',
    onConfirm: async () => {
      const key = dlgVal('nsKey');
      if (!key) throw new Error('请输入脚本 key');
      await api('PUT', `/admin/api/games/${state.gameId}/dialogues/${encodeURIComponent(key)}`,
        { title: dlgVal('nsTitle'), content: [] });
      window.__script = key;
      toast('已创建');
      await route();
    },
  });
}

async function saveScript(key) {
  let content;
  try { content = JSON.parse(document.getElementById('scContent').value); }
  catch { return toast('内容不是合法的 JSON'); }
  try {
    const s = await api('PUT', `/admin/api/games/${state.gameId}/dialogues/${encodeURIComponent(key)}`,
      { title: dlgVal('scTitle'), content });
    toast(`已保存 v${s.version}`);
    await route();
  } catch (e) { toast('保存失败:' + e.message); }
}

function deleteScript(key) {
  confirmDelete(`脚本「${key}」`, async () => {
    await api('DELETE', `/admin/api/games/${state.gameId}/dialogues/${encodeURIComponent(key)}`);
    window.__script = null;
    toast('已删除');
    await route();
  });
}
