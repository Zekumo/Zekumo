// Release management: create a version, attach artifacts, publish, roll out,
// revoke. The lifecycle is draft → published → revoked.

const REL_STATUS = {
  draft:      { text: '草稿',   badge: '' },
  published:  { text: '已发布', badge: 'ok' },
  deprecated: { text: '已弃用', badge: '' },
  revoked:    { text: '已下架', badge: 'error' },
};

async function renderReleases(host) {
  const { releases } = await api('GET', `/admin/api/games/${state.gameId}/releases`);
  pageShell(host, {
    title: '版本更新',
    subtitle: '更新器无需登录即可查询 <code>/v1/apps/{app_id}/updates/check</code>。下架某版本后会自动回退到上一个已发布版本。',
    actions: `<button class="btn filled" onclick="newReleaseDialog()">创建版本</button>`,
    body: releases.length ? `<div class="card table-card"><table>
        <thead><tr><th>版本</th><th>渠道</th><th>状态</th><th>灰度</th><th>发布时间</th><th></th></tr></thead>
        <tbody>${releases.map(relRow).join('')}</tbody></table></div>`
      : emptyState('📦', '还没有版本', '创建一个版本、上传产物并发布,客户端就能检查到更新。'),
  });
}

function relRow(r) {
  const st = REL_STATUS[r.status] || { text: r.status, badge: '' };
  const q = `'${esc(r.version)}','${esc(r.channel)}'`;
  const acts = [`<button class="btn text" onclick="manageArtifacts(${q})">产物</button>`];
  if (r.status === 'draft') {
    acts.push(`<button class="btn text" onclick="publishRelease(${q})">发布</button>`,
              `<button class="btn text danger" onclick="deleteRelease(${q})">删除</button>`);
  } else if (r.status === 'published') {
    acts.push(`<button class="btn text" onclick="rolloutDialog(${q},${r.rollout_percent})">灰度</button>`,
              `<button class="btn text danger" onclick="revokeRelease(${q})">下架</button>`);
  }
  return `<tr>
    <td><b>${esc(r.version)}</b>${r.mandatory ? ' <span class="badge error">强制</span>' : ''}</td>
    <td><span class="badge">${esc(r.channel)}</span></td>
    <td><span class="badge ${st.badge}">${esc(st.text)}</span></td>
    <td>${r.status === 'published' ? rolloutBar(r.rollout_percent) : '—'}</td>
    <td class="dim">${r.published_at ? fmtAgo(r.published_at) : '—'}</td>
    <td><div class="row" style="gap:0">${acts.join('')}</div></td>
  </tr>`;
}

function rolloutBar(pct) {
  return `<div class="rollout"><div class="rollout-fill" style="width:${pct}%"></div><span>${pct}%</span></div>`;
}

function relPath(version, channel) {
  return `/admin/api/games/${state.gameId}/releases/${encodeURIComponent(version)}?channel=${encodeURIComponent(channel)}`;
}

function newReleaseDialog() {
  openDialog({
    title: '创建版本',
    body: dlgField('nrVersion', '版本号(如 1.2.0)') +
      dlgField('nrChannel', '渠道', 'stable') +
      dlgField('nrLog', '更新日志') +
      dlgField('nrMin', '最低支持版本(低于它强制更新,可留空)') +
      `<label class="check" style="margin-top:8px"><input type="checkbox" id="nrForce"> 强制更新</label>`,
    confirmText: '创建',
    onConfirm: async () => {
      const version = dlgVal('nrVersion');
      if (!version) throw new Error('请输入版本号');
      await api('POST', `/admin/api/games/${state.gameId}/releases`, {
        version, channel: dlgVal('nrChannel') || 'stable',
        changelog: dlgVal('nrLog'), min_supported_version: dlgVal('nrMin'),
        mandatory: document.getElementById('nrForce').checked,
      });
      toast('已创建草稿');
      await route();
    },
  });
}

async function publishRelease(version, channel) {
  try {
    await api('POST', relPath(version, channel).replace('?', '/publish?'), {});
    toast(`${version} 已发布`);
    await route();
  } catch (e) { toast('发布失败:' + e.message); }
}

function revokeRelease(version, channel) {
  openDialog({
    title: `下架 ${version}?`,
    body: `<p class="dim">客户端的更新检查会回退到上一个已发布版本。</p>`,
    confirmText: '下架', danger: true,
    onConfirm: async () => {
      await api('POST', relPath(version, channel).replace('?', '/revoke?'), {});
      toast('已下架');
      await route();
    },
  });
}

function deleteRelease(version, channel) {
  confirmDelete(`草稿 ${version}`, async () => {
    await api('DELETE', relPath(version, channel));
    toast('已删除');
    await route();
  });
}

function rolloutDialog(version, channel, current) {
  openDialog({
    title: `${version} 灰度放量`,
    body: dlgField('roPct', '放量百分比(0-100)', String(current), 'number') +
      `<p class="dim" style="margin-top:12px">按 device_id 稳定分桶:同一台设备的命中结果不会来回变。</p>`,
    confirmText: '应用',
    onConfirm: async () => {
      const percent = parseInt(dlgVal('roPct'));
      if (isNaN(percent) || percent < 0 || percent > 100) throw new Error('百分比需在 0-100 之间');
      await api('POST', relPath(version, channel).replace('?', '/rollout?'), { percent });
      toast(`已放量到 ${percent}%`);
      await route();
    },
  });
}

// ---------- artifacts ----------
async function manageArtifacts(version, channel) {
  const { artifacts } = await api('GET', relPath(version, channel).replace('?', '/artifacts?'));
  openDialog({
    title: `${version} 的产物`,
    body: `
      ${artifacts.length ? `<table style="margin-bottom:16px">
        <thead><tr><th>平台</th><th>文件名</th><th>大小</th><th>状态</th></tr></thead>
        <tbody>${artifacts.map(a => `<tr>
          <td>${esc(a.platform)}${a.arch !== 'any' ? ' / ' + esc(a.arch) : ''}</td>
          <td>${esc(a.filename)}</td>
          <td class="dim">${fmtBytes(a.size)}</td>
          <td><span class="badge ${a.status === 'ready' ? 'ok' : ''}">${a.status === 'ready' ? '就绪' : '待上传'}</span></td>
        </tr>`).join('')}</tbody></table>`
        : `<p class="dim" style="margin-bottom:16px">还没有产物。发布前至少需要一个就绪的产物。</p>`}
      <div class="row">
        <label class="field compact" style="width:140px">
          <select id="apPlatform">
            <option>windows</option><option>macos</option><option>linux</option>
            <option>android</option><option>ios</option><option>web</option><option selected>any</option>
          </select><span class="label">平台</span></label>
        <label class="field compact" style="width:120px">
          <select id="apArch"><option selected>any</option><option>amd64</option><option>arm64</option></select>
          <span class="label">架构</span></label>
        <label class="field"><input type="file" id="apFile" placeholder=" "><span class="label">选择文件</span></label>
      </div>
      <div id="apStatus" class="dim" style="margin-top:12px"></div>`,
    confirmText: '上传',
    onConfirm: async () => {
      await uploadArtifact(version, channel);
      throw new Error(''); // keep the dialog open so the list can be re-read
    },
  });
}

async function uploadArtifact(version, channel) {
  const file = document.getElementById('apFile').files[0];
  const status = document.getElementById('apStatus');
  if (!file) { status.textContent = '请先选择文件'; return; }

  status.textContent = '计算校验和…';
  const buf = await file.arrayBuffer();
  const digest = await crypto.subtle.digest('SHA-256', buf);
  const sha256 = [...new Uint8Array(digest)].map(b => b.toString(16).padStart(2, '0')).join('');

  status.textContent = '登记产物…';
  const reg = await api('POST', relPath(version, channel).replace('?', '/artifacts?'), {
    platform: document.getElementById('apPlatform').value,
    arch: document.getElementById('apArch').value,
    filename: file.name, size: file.size, sha256,
  });

  status.textContent = `上传中(${fmtBytes(file.size)})…`;
  const put = await fetch(reg.upload_url, { method: 'PUT', body: buf });
  if (!put.ok) { status.textContent = '上传失败:HTTP ' + put.status; return; }

  await api('POST', relPath(version, channel)
    .replace('?', `/artifacts/${reg.artifact.id}/complete?`), {});
  status.textContent = '完成。';
  toast('产物已上传');
  await route();
}
