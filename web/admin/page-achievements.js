// Achievement catalogue management and server-side unlock testing.

const ACH_RARITIES = [
  ['common', '普通'], ['rare', '稀有'], ['epic', '史诗'], ['legendary', '传说'],
];

async function renderAchievements(host) {
  const { achievements } = await api('GET', `/admin/api/games/${state.gameId}/achievements`);
  const rows = achievements || [];
  pageShell(host, {
    title: '成就',
    subtitle: '定义成就目标，由服务端解锁；隐藏成就在玩家解锁前不会泄露详情。',
    actions: `<button class="btn filled" onclick="achievementDialog()">新建成就</button>`,
    body: rows.length ? `<div class="card table-card"><table>
      <thead><tr><th>成就</th><th>类型</th><th>稀有度</th><th>目标</th><th>状态</th><th></th></tr></thead>
      <tbody>${rows.map(achievementRow).join('')}</tbody>
    </table></div>` : emptyState('★', '还没有成就', '创建第一个成就后，服务端可以按玩家进度触发解锁。'),
  });
}

function achievementRow(a) {
  const rarity = ACH_RARITIES.find(([key]) => key === a.rarity)?.[1] || a.rarity;
  return `<tr>
    <td><b>${esc(a.name)}</b><div class="dim"><code>${esc(a.key)}</code>${a.hidden ? ' · 隐藏' : ''}</div></td>
    <td><span class="badge">${a.type === 'progress' ? '进度型' : '即时型'}</span></td>
    <td>${esc(rarity)}</td>
    <td>${a.type === 'progress' ? fmtNum(a.target) : '—'}</td>
    <td><span class="badge">已配置</span></td>
    <td><div class="row">
      <button class="btn text" onclick="achievementDialog(${inlineJSON(a)})">编辑</button>
      <button class="btn text" onclick="unlockAchievement(${inlineJSON(a.id)},${inlineJSON(a.name)})">解锁</button>
      <button class="btn text danger" onclick="deleteAchievement(${inlineJSON(a.id)},${inlineJSON(a.name)})">删除</button>
    </div></td>
  </tr>`;
}

function achievementDialog(a = null) {
  const editing = Boolean(a);
  const rarity = a?.rarity || 'common';
  const type = a?.type || 'instant';
  openDialog({
    title: editing ? `编辑成就「${a.name}」` : '新建成就',
    body: `${editing ? '' : dlgField('achKey', '唯一 key(如 first_login)')}
      ${dlgField('achName', '名称', a?.name || '')}
      <label class="field"><textarea id="achDescription" placeholder=" ">${esc(a?.description || '')}</textarea><span class="label">描述</span></label>
      ${dlgField('achIcon', '图标 URL', a?.icon_url || '')}
      <div class="filters" style="margin-top:16px">
        <label class="field"><select id="achRarity">${ACH_RARITIES.map(([key, label]) => `<option value="${key}" ${key === rarity ? 'selected' : ''}>${label}</option>`).join('')}</select><span class="label">稀有度</span></label>
        <label class="field"><select id="achType" onchange="toggleAchievementTarget()"><option value="instant" ${type === 'instant' ? 'selected' : ''}>即时型</option><option value="progress" ${type === 'progress' ? 'selected' : ''}>进度型</option></select><span class="label">类型</span></label>
        ${dlgField('achTarget', '目标值', a?.target || 1, 'number')}
        ${dlgField('achSort', '排序', a?.sort_order || 0, 'number')}
      </div>
      <label class="check"><input id="achHidden" type="checkbox" ${a?.hidden ? 'checked' : ''}> 隐藏成就</label>`,
    confirmText: editing ? '保存' : '创建',
    onConfirm: async () => {
      const typeValue = document.getElementById('achType').value;
      const payload = {
        ...(editing ? {} : { key: dlgVal('achKey') }),
        name: dlgVal('achName'),
        description: document.getElementById('achDescription').value,
        icon_url: dlgVal('achIcon'),
        rarity: document.getElementById('achRarity').value,
        hidden: document.getElementById('achHidden').checked,
        type: typeValue,
        target: typeValue === 'instant' ? 1 : Number(document.getElementById('achTarget').value),
        sort_order: Number(document.getElementById('achSort').value) || 0,
      };
      if (!payload.name) throw new Error('请输入成就名称');
      if (!editing && !payload.key) throw new Error('请输入成就 key');
      if (typeValue === 'progress' && (!Number.isInteger(payload.target) || payload.target < 1)) throw new Error('进度目标必须是正整数');
      await api(editing ? 'PUT' : 'POST', editing ? `/admin/api/achievements/${a.id}` : `/admin/api/games/${state.gameId}/achievements`, payload);
      toast(editing ? '已保存' : '已创建');
      await route();
    },
  });
  toggleAchievementTarget();
}

function toggleAchievementTarget() {
  const type = document.getElementById('achType');
  const target = document.getElementById('achTarget');
  if (type && target) target.disabled = type.value === 'instant';
}

function deleteAchievement(id, name) {
  confirmDelete(`成就「${name}」`, async () => {
    await api('DELETE', `/admin/api/achievements/${id}`);
    toast('已删除');
    await route();
  });
}

function unlockAchievement(id, name) {
  openDialog({
    title: `解锁「${name}」`,
    body: dlgField('unlockPlayer', '玩家 ID') + dlgField('unlockProgress', '进度(留空=完成)', '', 'number'),
    confirmText: '解锁',
    onConfirm: async () => {
      const playerId = dlgVal('unlockPlayer');
      if (!playerId) throw new Error('请输入玩家 ID');
      const raw = dlgVal('unlockProgress');
      const body = { player_id: playerId };
      if (raw) {
        body.progress = Number(raw);
        if (!Number.isInteger(body.progress) || body.progress < 0) throw new Error('进度必须是非负整数');
      }
      await api('POST', `/admin/api/achievements/${id}/unlock`, body);
      toast('已触发解锁');
    },
  });
}
