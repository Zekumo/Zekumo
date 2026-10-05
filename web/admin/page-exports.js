// Data exports: player data packaged as JSON or a CSV zip for download.
//
// Exports run asynchronously — submitting queues a job the server works
// through in the background — so this page tracks a job through
// pending → running → done / failed. While any job is unfinished the page
// refreshes itself; nobody should have to hit F5 to find out if it finished.
//
// Flow: renderExports lists history → newExportDialog submits → pollExports
// watches. Download links are presigned by the storage driver and last 24
// hours; revisiting this page after that mints fresh ones.

// The four states the backend reports, each with wording an operator can act
// on. running is warn rather than ok: "being generated" is not yet a file.
const EXPORT_STATUS = {
  pending: { text: '排队中',  badge: 'warn'  },       // queued, worker has not started
  running: { text: '生成中',  badge: 'warn'  },       // querying and packing right now
  done:    { text: '已完成',  badge: 'ok'    },       // written to storage, downloadable
  failed:  { text: '失败',    badge: 'error' },       // query or storage write failed; see error
};

const EXPORT_SCOPE  = { all: '全部玩家', player: '单个玩家' };  // what gets exported
const EXPORT_FORMAT = { json: 'JSON', csv: 'CSV (zip)' };       // file layout

// 3s: exports of a small game finish in seconds. Polling faster just burns
// requests; slower and the page looks stuck.
const EXPORT_POLL_MS = 3000;

let exportTimer = null;                                // this page's poll timer, cleared on leave

// ---------- page ----------

async function renderExports(host) {
  const { exports: rows } = await api('GET',
    `/admin/api/games/${state.gameId}/exports`);       // each row is {job, download_url?}
  const list = rows || [];                             // server sends [] when empty; guard anyway

  pageShell(host, {
    title: '数据导出',
    subtitle: '导出玩家档案、存档、货币流水与成就,满足数据可携带(GDPR)要求。下载链接有效期 24 小时。',
    actions: `<button class="btn filled" data-write onclick="newExportDialog()">新建导出</button>`,
    body: list.length ? `<div class="card table-card"><table>
        <thead><tr><th>范围</th><th>格式</th><th>状态</th><th>大小</th><th>提交时间</th><th>耗时</th><th></th></tr></thead>
        <tbody>${list.map(exportRow).join('')}</tbody>
      </table></div>`
      : emptyState('📦', '还没有导出记录',
                   '新建一个导出任务,生成完成后这里会出现下载链接。'),
  });

  pollExports(list);                                   // no-op unless something is still running
}

// ---------- one row ----------

// A row answers two questions at once: what was asked for, and can I download
// it yet. A failed job shows the server's own error text — seeing "storage
// write failed" is what tells you to go look at the storage config.
function exportRow({ job, download_url }) {
  const st = EXPORT_STATUS[job.status] || { text: job.status, badge: '' };
  return `<tr>
    <td><b>${EXPORT_SCOPE[job.scope] || esc(job.scope)}</b>
        ${job.player_id ? `<div class="dim"><code>${esc(job.player_id)}</code></div>` : ''}</td>
    <td><span class="badge">${EXPORT_FORMAT[job.format] || esc(job.format)}</span></td>
    <td><span class="badge ${st.badge}">${esc(st.text)}</span>
        ${job.error ? `<div class="dim">${esc(job.error)}</div>` : ''}</td>
    <td class="dim">${job.size ? fmtBytes(job.size) : '—'}</td>
    <td class="dim">${fmtAgo(job.created_at)}</td>
    <td class="dim">${exportDuration(job)}</td>
    <td>${download_url
      ? `<a class="btn text" href="${esc(download_url)}" download>下载</a>`
      : ''}</td>
  </tr>`;
}

// Duration only means something once the job has ended: until then there is
// no end point, and any number shown would be invented.
function exportDuration(job) {
  if (!job.finished_at) return '—';                    // still queued or running
  const secs = (new Date(job.finished_at) - new Date(job.created_at)) / 1000;
  if (secs < 1) return '<1 秒';                        // small games export in well under a second
  if (secs < 60) return `${Math.round(secs)} 秒`;
  return `${Math.floor(secs / 60)} 分 ${Math.round(secs % 60)} 秒`;
}

// ---------- auto refresh ----------

// Jobs advance on the server, so a static page sits on 「生成中」 forever. The
// timer is armed only while something is actually running — a settled page
// should generate no traffic.
function pollExports(list) {
  clearTimeout(exportTimer);                           // drop the previous render's timer, never stack them
  const busy = list.some(r => r.job.status === 'pending' || r.job.status === 'running');
  if (!busy) return;                                   // nothing in flight
  const ctx = operationContext();

  exportTimer = setTimeout(() => {
    if (!contextCurrent(ctx) || currentRoute().page !== 'exports') return;
    route();                                            // re-render, which re-evaluates whether to keep polling
  }, EXPORT_POLL_MS);
}

// ---------- new export ----------

// The player field appears only for scope=player: leaving a required-looking
// box on screen that does not apply is the fastest way to get it filled wrong.
function newExportDialog() {
  openDialog({
    title: '新建导出',
    body: `<label class="field"><select id="exScope" onchange="toggleExportPlayer()">
        ${Object.entries(EXPORT_SCOPE).map(([v, label]) =>
          `<option value="${v}">${label}</option>`).join('')}
      </select><span class="label">导出范围</span></label>
      <div id="exPlayerWrap" class="hidden">${dlgField('exPlayer', '玩家 ID')}</div>
      <label class="field"><select id="exFormat">
        ${Object.entries(EXPORT_FORMAT).map(([v, label]) =>
          `<option value="${v}">${label}</option>`).join('')}
      </select><span class="label">文件格式</span></label>
      <p class="dim">CSV 会按表拆成多个文件打包成 zip;JSON 是单文件嵌套结构。</p>`,
    confirmText: '开始导出',
    onConfirm: async () => {
      const scope = document.getElementById('exScope').value;
      const playerId = dlgVal('exPlayer');
      if (scope === 'player' && !playerId) throw new Error('请输入要导出的玩家 ID');
      await api('POST', `/admin/api/games/${state.gameId}/exports`, {
        format: document.getElementById('exFormat').value,
        scope,
        player_id: scope === 'player' ? playerId : undefined,
      });
      toast('导出任务已提交');
      await route();                                   // new job shows as 排队中 and polling starts
    },
  });
}

function toggleExportPlayer() {
  const scope = document.getElementById('exScope').value;
  document.getElementById('exPlayerWrap').classList.toggle('hidden', scope !== 'player');
}
