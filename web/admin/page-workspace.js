// Workspace membership is intentionally compact: a readable member list,
// clear roles, and only the actions this operator can actually perform.

function memberInitial(name) { return initials(name || '?'); }

function memberActions(member) {
  if (!canManageMembers()) return '';
  const ownerLocked = state.activeRole !== 'owner' && member.role === 'owner';
  if (ownerLocked) return '<span class="dim">仅所有者可管理</span>';
  const roles = state.activeRole === 'owner'
    ? ['owner', 'admin', 'editor', 'viewer'] : ['admin', 'editor', 'viewer'];
  return `<div class="member-actions">
    <label class="sr-only" for="role-${esc(member.user_id)}">修改 ${esc(member.username)} 的角色</label>
    <select id="role-${esc(member.user_id)}" data-write data-min-role="admin"
      onchange="changeMemberRole(${inlineJSON(member.user_id)}, this)">
      ${roles.map(role => `<option value="${role}" ${role === member.role ? 'selected' : ''}>${ROLE_LABEL[role]}</option>`).join('')}
    </select>
    <button class="icon-btn" data-write data-min-role="admin" title="移除成员"
      onclick="removeMember(${inlineJSON(member.user_id)},${inlineJSON(member.username)})">
      <svg viewBox="0 0 24 24"><path d="M19 13H5v-2h14v2Z"/></svg>
    </button>
  </div>`;
}

async function renderWorkspace(host) {
  const workspaceId = state.workspaceId;
  const workspaceEpoch = state.workspaceEpoch;
  const { members } = await api('GET', `/admin/api/workspaces/${workspaceId}/members`, undefined, { workspaceId });
  if (workspaceId !== state.workspaceId || workspaceEpoch !== state.workspaceEpoch) return;

  const actions = `<button class="btn outlined" onclick="newWorkspaceDialog()">新建工作区</button>
    ${canManageMembers() ? '<button class="btn filled" data-write data-min-role="admin" onclick="addMemberDialog()">添加成员</button>' : ''}`;
  pageShell(host, {
    title: '成员与权限',
    subtitle: `${esc(state.workspace.name)} · ${esc(state.workspace.organization_name || '工作区')}`,
    actions,
    body: `<div class="workspace-summary card">
        <div><span class="eyebrow">工作区</span><h2>${esc(state.workspace.name)}</h2></div>
        <div class="workspace-meta">
          <span><b>${members.length}</b> 位成员</span>
          <span class="badge">${esc(roleLabel(state.activeRole))}</span>
        </div>
      </div>
      ${members.length ? `<div class="member-list" aria-label="工作区成员">
        ${members.map(member => `<article class="member-card">
          <span class="member-avatar" style="--avatar-hue:${avatarColor(member.username)}">${esc(memberInitial(member.username))}</span>
          <div class="member-main">
            <div class="member-name">${esc(member.username)}</div>
            <div class="dim">加入于 ${fmtTime(member.created_at)}</div>
          </div>
          <span class="badge role-${esc(member.role)}">${esc(roleLabel(member.role))}</span>
          ${memberActions(member)}
        </article>`).join('')}
      </div>` : mascotState('empty', '还没有成员', '添加一位已有 Zekumo 通行证账号的成员。')}`,
  });
}

function newWorkspaceDialog() {
  openDialog({
    title: '新建工作区',
    body: `${dlgField('workspaceName', '工作区名称')}
      ${dlgField('workspaceSlug', '英文标识（可选）')}
      <p class="dim dialog-note">你将成为新工作区的所有者。</p>`,
    confirmText: '创建',
    onConfirm: async () => {
      const name = dlgVal('workspaceName');
      if (!name) throw new Error('请输入工作区名称');
      const slug = dlgVal('workspaceSlug');
      const created = await api('POST', '/admin/api/workspaces', { name, ...(slug ? { slug } : {}) }, { workspaceId: null });
      const data = await api('GET', '/admin/api/workspaces', undefined, { workspaceId: null });
      state.workspaces = data.workspaces || [];
      const id = created.id || created.workspace?.id;
      if (!id) throw new Error('工作区已创建，但无法自动切换');
      forceCloseDialog();
      selectWorkspace(id);
      toast('工作区已创建');
    },
  });
}

function addMemberDialog() {
  if (!canManageMembers()) return toast('需要管理员或所有者权限');
  const choices = state.activeRole === 'owner'
    ? [['viewer', '查看者'], ['editor', '编辑者'], ['admin', '管理员'], ['owner', '所有者']]
    : [['viewer', '查看者'], ['editor', '编辑者'], ['admin', '管理员']];
  openDialog({
    title: '添加成员',
    body: `${dlgField('memberUsername', '通行证用户名')}
      <label class="field"><select id="memberRole">
        ${choices.map(([value, label]) => `<option value="${value}">${label}</option>`).join('')}
      </select><span class="label">角色</span></label>
      <p class="dim dialog-note">对方需先拥有 Zekumo 通行证账号，之后可用现有账号登录控制台。</p>`,
    confirmText: '添加',
    onConfirm: async () => {
      const accountUsername = dlgVal('memberUsername');
      if (!accountUsername) throw new Error('请输入通行证用户名');
      const workspaceId = state.workspaceId;
      await api('POST', `/admin/api/workspaces/${workspaceId}/members`, {
        account_username: accountUsername,
        role: dlgVal('memberRole'),
      }, { workspaceId });
      if (workspaceId === state.workspaceId) await route();
      toast('成员已添加');
    },
  });
}

async function changeMemberRole(userId, select) {
  if (select.disabled || !canManageMembers()) return;
  const workspaceId = state.workspaceId;
  const previous = [...select.options].find(o => o.defaultSelected)?.value || select.dataset.previous;
  select.dataset.previous = previous || select.value;
  select.disabled = true;
  try {
    await api('PUT', `/admin/api/workspaces/${workspaceId}/members/${encodeURIComponent(userId)}`,
      { role: select.value }, { workspaceId });
    if (workspaceId === state.workspaceId) await route();
    toast('角色已更新');
  } catch (e) {
    if (previous) select.value = previous;
    select.disabled = false;
    toast(e.message);
  }
}

function removeMember(userId, username) {
  if (!canManageMembers()) return;
  const workspaceId = state.workspaceId;
  openDialog({
    title: `移除 ${username}？`,
    body: '<p class="dim">对方将立即失去此工作区及其游戏的访问权。</p>',
    confirmText: '移除', danger: true,
    onConfirm: async () => {
      await api('DELETE', `/admin/api/workspaces/${workspaceId}/members/${encodeURIComponent(userId)}`,
        undefined, { workspaceId });
      if (workspaceId === state.workspaceId) await route();
      toast('成员已移除');
    },
  });
}

function renderWorkspaceWelcome() {
  document.getElementById('navRail').replaceChildren();
  document.getElementById('workspaceSwitcher').replaceChildren();
  document.getElementById('gameSwitcher').replaceChildren();
  const host = document.getElementById('page');
  host.innerHTML = mascotState('welcome', '创建你的第一个工作区',
    '工作区把游戏、数据与团队成员安全地分开。',
    '<button class="btn filled" onclick="newWorkspaceDialog()">创建工作区</button>');
}
