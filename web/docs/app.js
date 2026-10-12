const endpointHost = document.getElementById('endpoints');
const count = document.getElementById('count');
const search = document.getElementById('search');
const identity = document.getElementById('identity');
let endpoints = [];

function render() {
  const words = search.value.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const selected = endpoints.filter(e => (!identity.value || e.auth === identity.value) && words.every(w => `${e.method} ${e.path} ${e.auth} ${e.minimum_role}`.toLowerCase().includes(w)));
  endpointHost.replaceChildren();
  count.textContent = `${selected.length} / ${endpoints.length} 个已注册接口`;
  for (const e of selected) {
    const details = document.createElement('details');
    const summary = document.createElement('summary');
    const method = document.createElement('span'); method.className = 'method'; method.textContent = e.method;
    const path = document.createElement('code'); path.textContent = e.path;
    const auth = document.createElement('span'); auth.className = 'auth'; auth.textContent = e.auth + (e.minimum_role ? ` · ${e.minimum_role}+` : '');
    summary.append(method, path, auth);
    const note = document.createElement('p'); note.className = 'route-info';
    note.textContent = e.workspace_header ? '必须携带管理员 JWT 和 X-Zekumo-Workspace-ID；服务端校验最低角色及目标资源归属。' : e.auth === 'public' ? '入口无需令牌，但具体业务仍可能检查账号凭据、函数 public 标记或应用状态。' : `需要 ${e.auth} 身份；不是公开匿名接口。`;
    const pre = document.createElement('pre');
    const headers = [];
    if (!['public', 'server-hmac'].includes(e.auth)) headers.push("  -H 'Authorization: Bearer YOUR_TOKEN'");
    if (e.workspace_header) headers.push("  -H 'X-Zekumo-Workspace-ID: YOUR_WORKSPACE_UUID'");
    if (e.auth === 'server-hmac') headers.push("  -H 'X-Zekumo-App-Id: YOUR_APP_ID'", "  -H 'X-Zekumo-Timestamp: UNIX_SECONDS'", "  -H 'X-Zekumo-Signature: COMPUTED_HMAC'");
    if (['POST', 'PUT', 'PATCH'].includes(e.method)) headers.push("  -H 'Content-Type: application/json'", "  --data 'REPLACE_WITH_DOCUMENTED_JSON_BODY'");
    pre.textContent = e.auth === 'player-websocket' ? '使用 SDK realtime.connect() 或兼容 WebSocket 客户端；普通 curl 模板不完成 WebSocket 升级。' : [`curl -X ${e.method} 'https://YOUR_SERVER${e.path}'`, ...headers].join(' \\\n');
    details.append(summary, note, pre); endpointHost.append(details);
  }
  if (!selected.length) endpointHost.textContent = '没有匹配的接口。试试更短的路径或切换身份筛选。';
}
search.addEventListener('input', render);
identity.addEventListener('change', render);
fetch('endpoints.json', { credentials: 'omit' }).then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); }).then(data => {
  endpoints = data.endpoints; render();
}).catch(() => { count.textContent = '接口目录暂时无法加载，请刷新或查看 JSON 目录。'; });
