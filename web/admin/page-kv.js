// Tenant-authorized access to the same namespace/key store used by signed REST.
async function renderKV(host) {
  const ctx = operationContext();
  let namespace = '';
  let nsOffset = 0;
  let keyOffset = 0;
  let request = 0;
  async function load() {
    const seq = ++request;
    try {
      requireCurrentContext(ctx);
      const namespaces = await api('GET', `/admin/api/games/${ctx.gameId}/kv?limit=20&offset=${nsOffset}`);
      if (!contextCurrent(ctx) || request !== seq || !host.isConnected) return;
      if (!namespace && namespaces.namespaces.length) namespace = namespaces.namespaces[0].name;
      const data = namespace ? await api('GET', `/admin/api/games/${ctx.gameId}/kv/${encodeURIComponent(namespace)}?limit=20&offset=${keyOffset}`) : null;
      if (!contextCurrent(ctx) || request !== seq || !host.isConnected) return;
      pageShell(host, {
        title: '游戏配置 KV',
        subtitle: '持久 JSON 配置与 TTL。public 和 public_ 前缀命名空间对玩家开放；其他命名空间仅服务端和本工作区管理人员可读。',
        actions: `<button class="btn outlined" data-kv-refresh>刷新</button><button class="btn filled" data-write onclick="newKVDialog(${inlineJSON(namespace)})">新建配置</button>`,
        body: `<div class="master-detail"><div class="card list-card">
          ${namespaces.namespaces.map(n => `<button class="list-item ${n.name === namespace ? 'on' : ''}" data-kv-namespace="${esc(n.name)}"><b>${esc(n.name)}</b><span class="dim">${fmtNum(n.key_count)} 个键</span></button>`).join('') || '<p class="dim">尚无命名空间</p>'}
          <div class="row"><button class="btn text" data-ns-prev ${nsOffset === 0 ? 'disabled' : ''}>上一页</button><button class="btn text" data-ns-next ${!namespaces.has_more || nsOffset >= 10000 ? 'disabled' : ''}>下一页</button></div>
        </div><div class="card">
          ${data ? `<h2>${esc(namespace)} <span class="badge">${data.public ? '玩家可读' : '服务端配置'}</span></h2>
          ${data.keys.length ? `<table><thead><tr><th>键</th><th>过期时间</th><th>操作</th></tr></thead><tbody>${data.keys.map(e => `<tr>
            <td><b>${esc(e.key)}</b><div class="dim">更新 ${esc(fmtTime(e.updated_at))}</div></td><td>${e.expires_at ? esc(fmtTime(e.expires_at)) : '永久'}</td>
            <td><button class="btn text" onclick="openKVEntry(${inlineJSON(namespace)},${inlineJSON(e.key)})">查看 / 编辑</button><button class="btn text danger" data-write onclick="deleteKVEntry(${inlineJSON(namespace)},${inlineJSON(e.key)})">删除</button></td>
          </tr>`).join('')}</tbody></table>` : '<p class="dim">此页没有未过期的键</p>'}
          <div class="row"><button class="btn text" data-key-prev ${keyOffset === 0 ? 'disabled' : ''}>上一页</button><button class="btn text" data-key-next ${!data.has_more || keyOffset >= 10000 ? 'disabled' : ''}>下一页</button></div>` : emptyState('⚙️', '还没有配置', '创建命名空间和 JSON 键值后，游戏可以通过现有 KV SDK 查询公开配置。')}
        </div></div>`,
      });
      host.querySelectorAll('[data-kv-namespace]').forEach(el => { el.onclick = () => { namespace = el.dataset.kvNamespace; keyOffset = 0; load(); }; });
      host.querySelector('[data-kv-refresh]').onclick = load;
      host.querySelector('[data-ns-prev]').onclick = () => { nsOffset = Math.max(0, nsOffset - 20); load(); };
      host.querySelector('[data-ns-next]').onclick = () => { nsOffset += 20; load(); };
      if (data) {
        host.querySelector('[data-key-prev]').onclick = () => { keyOffset = Math.max(0, keyOffset - 20); load(); };
        host.querySelector('[data-key-next]').onclick = () => { keyOffset += 20; load(); };
      }
      applySurfaceAccess(host);
    } catch (e) { if (contextCurrent(ctx) && host.isConnected && seq === request) host.innerHTML = errorState('配置加载失败', e.message, false); }
  }
  await load();
}

function kvFields(value, ttl, readOnly = false) {
  return `<p class="dim">值最多 64 KiB。保存立即生效，并覆盖当前值。TTL 为 0 表示永久，正数为从现在起生效的秒数。</p>
    <label class="field"><textarea id="kvValue" style="min-height:240px" ${readOnly ? 'readonly' : ''}>${esc(JSON.stringify(value, null, 2))}</textarea><span class="label">JSON 值</span></label>
    <label class="field"><input id="kvTTL" type="number" min="0" max="31536000" value="${ttl}" ${readOnly ? 'readonly' : ''}><span class="label">TTL 秒数（0=永久）</span></label>`;
}
function kvPayload() {
  const ttl_seconds = Number(dlgVal('kvTTL'));
  if (!Number.isSafeInteger(ttl_seconds) || ttl_seconds < 0 || ttl_seconds > 31536000) throw new Error('TTL 必须为 0–31536000 的整数');
  return { value: JSON.parse(dlgVal('kvValue')), ttl_seconds };
}
function newKVDialog(namespace) {
  const ctx = operationContext();
  openDialog({
    title: '新建游戏配置',
    body: dlgField('kvNamespace', '命名空间', namespace || 'public') + dlgField('kvKey', '键') + kvFields({}, 0),
    confirmText: '保存（同名键会覆盖）',
    onConfirm: async () => {
      const ns = dlgVal('kvNamespace'); const key = dlgVal('kvKey'); const payload = kvPayload();
      requireCurrentContext(ctx);
      await api('PUT', `/admin/api/games/${ctx.gameId}/kv/${encodeURIComponent(ns)}/${encodeURIComponent(key)}`, payload);
      requireCurrentContext(ctx); toast('配置已保存'); await route();
    },
  });
}
async function openKVEntry(namespace, key) {
  const ctx = operationContext();
  try {
    const { entry } = await api('GET', `/admin/api/games/${ctx.gameId}/kv/${encodeURIComponent(namespace)}/${encodeURIComponent(key)}`);
    requireCurrentContext(ctx);
    const readOnly = !canWrite();
    const ttl = entry.expires_at ? Math.max(1, Math.ceil((Date.parse(entry.expires_at) - Date.now()) / 1000)) : 0;
    openDialog({
      title: `${namespace}/${key}`,
      body: kvFields(entry.value, ttl, readOnly), confirmText: readOnly ? '关闭' : '保存',
      onConfirm: async () => {
        if (readOnly) return;
        const payload = kvPayload(); requireCurrentContext(ctx);
        await api('PUT', `/admin/api/games/${ctx.gameId}/kv/${encodeURIComponent(namespace)}/${encodeURIComponent(key)}`, payload);
        requireCurrentContext(ctx); toast('配置已保存'); await route();
      },
    });
  } catch (e) { if (contextCurrent(ctx)) toast(e.message); }
}
function deleteKVEntry(namespace, key) {
  const ctx = operationContext();
  confirmDelete(`配置 ${namespace}/${key}`, async () => {
    requireCurrentContext(ctx);
    await api('DELETE', `/admin/api/games/${ctx.gameId}/kv/${encodeURIComponent(namespace)}/${encodeURIComponent(key)}`);
    requireCurrentContext(ctx); toast('配置已删除'); await route();
  });
}
