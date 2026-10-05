// Server announcements: publish, target, preview, edit, and deactivate.

const ANN_IMPORTANCE = { info: '通知', warning: '提醒', critical: '重要' };

async function renderAnnouncements(host) {
  const { announcements } = await api('GET', `/admin/api/games/${state.gameId}/announcements`);
  const rows = announcements || [];
  pageShell(host, {
    title: '公告',
    subtitle: '客户端通过轮询增量获取公告；可按平台和渠道定向发布。',
    actions: `<button class="btn filled" data-write onclick="announcementDialog()">发布公告</button>`,
    body: rows.length ? `<div class="card table-card"><table>
      <thead><tr><th>公告</th><th>重要性</th><th>定向</th><th>有效期</th><th>状态</th><th></th></tr></thead>
      <tbody>${rows.map(announcementRow).join('')}</tbody>
    </table></div>` : emptyState('!', '还没有公告', '发布第一条公告，客户端即可在更新或活动时展示。'),
  });
}

function announcementRow(a) {
  const expired = a.expires_at && new Date(a.expires_at) <= new Date();
  const stateLabel = !a.active ? '已下架' : expired ? '已过期' : '发布中';
  return `<tr>
    <td><b>${esc(a.title)}</b><div class="dim">${fmtAgo(a.created_at)}</div></td>
    <td><span class="badge ${a.importance === 'critical' ? 'error' : a.importance === 'warning' ? 'ok' : ''}">${ANN_IMPORTANCE[a.importance] || esc(a.importance)}</span></td>
    <td class="dim">${esc([a.platform, a.channel].filter(Boolean).join(' / ') || '全服')}</td>
    <td class="dim">${a.expires_at ? `至 ${fmtTime(a.expires_at)}` : '永久'}</td>
    <td><span class="badge ${a.active && !expired ? 'ok' : ''}">${stateLabel}</span></td>
    <td><div class="row">
      <button class="btn text" onclick="previewAnnouncement(${inlineJSON(a)})">预览</button>
      <button class="btn text" data-write onclick="announcementDialog(${inlineJSON(a)})">编辑</button>
      ${a.active ? `<button class="btn text danger" data-write onclick="deactivateAnnouncement(${inlineJSON(a.id)})">下架</button>` : ''}
    </div></td>
  </tr>`;
}

function announcementDialog(a = null) {
  const editing = Boolean(a);
  const expirySeconds = a?.expires_at ? Math.max(1, Math.ceil((new Date(a.expires_at) - Date.now()) / 1000)) : '';
  openDialog({
    title: editing ? `编辑公告「${a.title}」` : '发布公告',
    body: `${dlgField('annTitle', '标题', a?.title || '')}
      <label class="field"><textarea id="annBody" placeholder=" " oninput="updateAnnouncementPreview()">${esc(a?.body || '')}</textarea><span class="label">正文 Markdown</span></label>
      <div id="annPreview" class="announcement-preview dim">预览区域</div>
      <div class="filters" style="margin-top:16px">
        <label class="field"><select id="annImportance"><option value="info" ${a?.importance === 'info' || !a ? 'selected' : ''}>通知</option><option value="warning" ${a?.importance === 'warning' ? 'selected' : ''}>提醒</option><option value="critical" ${a?.importance === 'critical' ? 'selected' : ''}>重要</option></select><span class="label">重要性</span></label>
        ${dlgField('annPlatform', '平台(留空=全部)', a?.platform || '')}
        ${dlgField('annChannel', '渠道(留空=全部)', a?.channel || '')}
        ${dlgField('annExpires', '有效期秒数(留空=永久)', expirySeconds, 'number')}
      </div>
      <label class="check"><input id="annPermanent" type="checkbox" ${editing && !a.expires_at ? 'checked' : ''}> 永久有效</label>`,
    confirmText: editing ? '保存' : '发布',
    onConfirm: async () => {
      const payload = {
        title: dlgVal('annTitle'),
        body: document.getElementById('annBody').value,
        importance: document.getElementById('annImportance').value,
        platform: dlgVal('annPlatform'),
        channel: dlgVal('annChannel'),
      };
      if (!payload.title) throw new Error('请输入公告标题');
      const expiry = Number(dlgVal('annExpires'));
      const permanent = document.getElementById('annPermanent').checked;
      if (permanent) {
        if (editing) payload.clear_expires = true;
      } else if (dlgVal('annExpires')) {
        if (!Number.isInteger(expiry) || expiry <= 0) throw new Error('有效期必须是正整数秒数');
        payload.expires_in_secs = expiry;
      } else if (editing && a.expires_at) {
        payload.expires_at = a.expires_at;
      }
      await api(editing ? 'PUT' : 'POST', editing ? `/admin/api/announcements/${a.id}` : `/admin/api/games/${state.gameId}/announcements`, payload);
      toast(editing ? '已保存' : '已发布');
      await route();
    },
  });
  updateAnnouncementPreview();
}

function markdownPreview(value) {
  return esc(value || '').replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>').replace(/`(.+?)`/g, '<code>$1</code>').replace(/\n/g, '<br>');
}

function updateAnnouncementPreview() {
  const input = document.getElementById('annBody');
  const preview = document.getElementById('annPreview');
  if (input && preview) preview.innerHTML = markdownPreview(input.value) || '预览区域';
}

function previewAnnouncement(a) {
  openDialog({
    title: `公告预览 · ${a.title}`,
    body: `<div class="announcement-preview"><h3>${esc(a.title)}</h3><p>${markdownPreview(a.body)}</p></div>`,
    confirmText: '关闭',
    onConfirm: async () => {},
  });
}

function deactivateAnnouncement(id) {
  confirmDelete('这条公告(下架)', async () => {
    await api('DELETE', `/admin/api/announcements/${id}`);
    toast('已下架');
    await route();
  });
}
