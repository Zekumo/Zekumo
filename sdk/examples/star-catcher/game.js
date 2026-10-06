import { defaults, makeClient, loadSave, syncSave, cleanSave, BOARD } from './cloud.js';

const $ = id => document.getElementById(id);
const storage = {
  get(key, fallback) { try { return JSON.parse(localStorage.getItem(key)) ?? fallback; } catch { return fallback; } },
  set(key, value) { try { localStorage.setItem(key, JSON.stringify(value)); return true; } catch { return false; } },
};
const config = { ...defaults, ...storage.get('star-catcher-config', {}) };
let deviceId = storage.get('star-catcher-device', null);
if (!deviceId) { deviceId = crypto.randomUUID(); storage.set('star-catcher-device', deviceId); }
let client, playerId, connected = false, busy = false, localKey, save = cleanSave(null), pending = false;
let phase = 'ready', score = 0, lives = 3, remaining = 45, chain = 0, x = 400, targetX = 400;
let items = [], particles = [], spawnIn = 0, invincible = 0, last = performance.now(), worldTime = 0;
const keys = new Set();
const canvas = $('game'), ctx = canvas.getContext('2d');
$('api').value = config.baseUrl; $('app-id').value = config.appId; $('nickname').value = config.nickname;

function errorText(error) { return `${error.code ? `${error.code} · ` : ''}${error.message || '网络连接失败'}`; }
function renderSave() { $('best').textContent = save.best; $('rounds').textContent = save.rounds; }
function persist() {
  if (!storage.set(localKey, { ...save, pending })) $('save-status').textContent = '浏览器存储不可用，请保持页面打开。';
  renderSave();
}
function controlsBusy(value) {
  busy = value;
  $('start').disabled = value; $('connect').disabled = value || phase === 'playing' || phase === 'paused';
  if (phase === 'ready') $('start').textContent = value ? '正在连接…' : '开始拾星 →';
}
async function refreshBoard() {
  if (!connected) return;
  $('refresh').disabled = true;
  try {
    const entries = await client.leaderboards.top(BOARD, { limit: 5 });
    $('board').replaceChildren();
    if (!entries?.length) { const li = document.createElement('li'); li.className = 'empty'; li.textContent = '这里还很安静。来点亮第一颗星吧！'; $('board').append(li); }
    for (const entry of entries || []) {
      const li = document.createElement('li');
      if (entry.player_id === playerId) li.className = 'me';
      for (const [className, text] of [['rank', String(entry.rank).padStart(2, '0')], ['name', entry.nickname || '拾星旅人'], ['points', entry.score]]) {
        const span = document.createElement('span'); span.className = className; span.textContent = text; li.append(span);
      }
      $('board').append(li);
    }
  } catch (error) { $('board').replaceChildren(); const li = document.createElement('li'); li.className = 'empty'; li.textContent = `暂时读不到星光榜：${errorText(error)}`; $('board').append(li); }
  finally { $('refresh').disabled = false; }
}
async function pushSave() {
  if (!connected || !pending) return;
  $('save-status').textContent = '正在把这次旅程写入云端…';
  try {
    await syncSave(client, save); pending = false; persist();
    $('save-status').textContent = '✓ 云端手记已同步 · 刷新后可继续';
  } catch (error) { $('save-status').textContent = `已暂存本机，重新连接可重试：${errorText(error)}`; }
}
async function connect() {
  controlsBusy(true); connected = false;
  $('connection').textContent = '正在连接云端…';
  localKey = `star-catcher-save:${config.baseUrl}:${config.appId}:${deviceId}`;
  const cached = storage.get(localKey, {}); save = cleanSave(cached); pending = Boolean(cached.pending); renderSave();
  try {
    client = makeClient(config);
    const login = await client.auth.loginAsGuest({ deviceId, nickname: config.nickname });
    playerId = login.player.id;
    if (login.player.nickname !== config.nickname) await client.player.updateProfile({ nickname: config.nickname });
    // Never overwrite a cloud save when its initial read fails.
    const cloudSave = await loadSave(client);
    save = { best: Math.max(save.best, cloudSave.best), rounds: Math.max(save.rounds, cloudSave.rounds) };
    connected = true; persist();
    $('connection').textContent = '● Zekumo 云端已连接';
    $('player').textContent = `游客 ${playerId.slice(0, 8)} · 本浏览器下次自动登录`;
    $('save-status').textContent = '✓ 云端手记已读取';
    await pushSave(); await refreshBoard();
  } catch (error) {
    $('connection').textContent = '○ 本地模式';
    $('save-status').textContent = `连接未完成，可先本地游玩：${errorText(error)}`;
    $('board').textContent = '连接后可查看云端排行榜';
  } finally { controlsBusy(false); }
}
$('settings').addEventListener('submit', event => {
  event.preventDefault(); if (busy || phase === 'playing' || phase === 'paused') return;
  const url = new URL($('api').value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) { $('save-status').textContent = '请输入不含账号密码的 HTTP(S) API 地址。'; return; }
  Object.assign(config, { baseUrl: url.href.replace(/\/+$/, ''), appId: $('app-id').value.trim(), nickname: $('nickname').value.trim() || '拾星旅人' });
  storage.set('star-catcher-config', config); void connect();
});
$('refresh').onclick = () => void refreshBoard();

function hud() { $('score').textContent = score; $('time').replaceChildren(document.createTextNode(Math.ceil(remaining)), Object.assign(document.createElement('small'), { textContent: 's' })); $('lives').textContent = '♥ '.repeat(lives) + '♡ '.repeat(3 - lives); }
function start() {
  if (busy) return;
  if (phase === 'paused') { pause(); return; }
  phase = 'playing'; score = 0; lives = 3; remaining = 45; chain = 0; x = targetX = 400;
  items = []; particles = []; spawnIn = .2; invincible = 0; keys.clear();
  $('combo').textContent = ''; $('overlay').hidden = true; $('pause').disabled = false; $('pause').textContent = 'Ⅱ'; $('connect').disabled = true;
  hud(); canvas.focus(); last = performance.now();
}
function pause() {
  if (!['playing', 'paused'].includes(phase)) return;
  const paused = phase === 'playing'; phase = paused ? 'paused' : 'playing'; keys.clear();
  $('overlay').hidden = !paused; $('pause').textContent = paused ? '▶' : 'Ⅱ'; $('pause').setAttribute('aria-label', paused ? '继续游戏' : '暂停游戏');
  if (paused) { $('overlay-label').textContent = 'TAKE A LITTLE BREATH'; $('overlay-title').textContent = '云朵歇一会儿'; $('overlay-copy').textContent = '星光会等你，准备好了就继续。'; $('start').textContent = '继续拾星 →'; }
  else { canvas.focus(); last = performance.now(); }
}
async function finish() {
  phase = 'ended'; keys.clear(); $('pause').disabled = true; $('overlay').hidden = false;
  const previous = save.best; save.best = Math.max(score, save.best); save.rounds++; pending = true; persist();
  $('overlay-label').textContent = score > previous ? 'A NEW PERSONAL BEST' : 'A POCKET FULL OF STARS';
  $('overlay-title').textContent = `${score} 点星光，收进口袋`;
  $('overlay-copy').textContent = score > previous ? '新的最好成绩！下一片云上，还有更多星星。' : '旅程结束啦，再借一朵云出发吧。';
  $('start').textContent = '再玩一局 →'; controlsBusy(true);
  if (connected) { await pushSave(); await refreshBoard(); }
  else $('save-status').textContent = '本局已暂存本机，连接 Zekumo 后同步。';
  controlsBusy(false);
}
$('start').onclick = start; $('pause').onclick = pause;
window.addEventListener('keydown', event => {
  if (['INPUT', 'TEXTAREA', 'BUTTON', 'SUMMARY'].includes(document.activeElement?.tagName)) return;
  const key = event.key.toLowerCase();
  if (['arrowleft', 'arrowright', 'a', 'd', ' '].includes(key)) { event.preventDefault(); if (key === ' ' && !event.repeat) pause(); else keys.add(key); }
});
window.addEventListener('keyup', event => keys.delete(event.key.toLowerCase()));
window.addEventListener('blur', () => { keys.clear(); if (phase === 'playing') pause(); });
document.addEventListener('visibilitychange', () => { if (document.hidden && phase === 'playing') pause(); });
function point(event) { const rect = canvas.getBoundingClientRect(); targetX = Math.max(42, Math.min(758, (event.clientX - rect.left) / rect.width * 800)); }
canvas.addEventListener('pointerdown', event => { canvas.setPointerCapture(event.pointerId); point(event); });
canvas.addEventListener('pointermove', event => { if (event.pointerType === 'mouse' || canvas.hasPointerCapture(event.pointerId)) point(event); });

function burst(px, py, color) { for (let i = 0; i < 12; i++) particles.push({ x: px, y: py, vx: (Math.random() - .5) * 180, vy: (Math.random() - .6) * 180, life: .65, color }); }
function update(dt) {
  remaining = Math.max(0, remaining - dt); invincible = Math.max(0, invincible - dt);
  const direction = Number(keys.has('arrowright') || keys.has('d')) - Number(keys.has('arrowleft') || keys.has('a'));
  if (direction) { x = Math.max(42, Math.min(758, x + direction * 480 * dt)); targetX = x; }
  else x += (targetX - x) * Math.min(1, dt * 15);
  spawnIn -= dt;
  if (spawnIn <= 0) { items.push({ x: 35 + Math.random() * 730, y: -25, speed: 140 + (45 - remaining) * 3 + Math.random() * 55, rock: Math.random() < .24, spin: Math.random() * 6 }); spawnIn = .42 + Math.random() * .25; }
  for (const item of items) {
    item.y += item.speed * dt; item.spin += dt;
    if (item.y > 465 && item.y < 514 && Math.abs(item.x - x) < (item.rock ? 43 : 53)) {
      item.dead = true;
      if (item.rock) { if (!invincible) { lives--; chain = 0; invincible = 1.3; burst(item.x, item.y, '#aa91c3'); $('combo').textContent = '哎呀！小心陨石'; } }
      else { chain++; const bonus = Math.min(20, Math.floor(chain / 5) * 5); score += 10 + bonus; burst(item.x, item.y, '#eac667'); $('combo').textContent = chain >= 5 ? `${chain} 连击 · 每颗 +${10 + bonus}` : ''; }
    }
    if (item.y > 590) { item.dead = true; if (!item.rock) { chain = 0; $('combo').textContent = ''; } }
  }
  items = items.filter(item => !item.dead);
  for (const p of particles) { p.x += p.vx * dt; p.y += p.vy * dt; p.vy += 130 * dt; p.life -= dt; }
  particles = particles.filter(p => p.life > 0); hud();
  if (lives <= 0 || remaining <= 0) void finish();
}
function cloud(cx, cy, scale, opacity = 1) {
  ctx.save(); ctx.translate(cx, cy); ctx.scale(scale, scale); ctx.globalAlpha = opacity; ctx.fillStyle = '#fffefa';
  ctx.beginPath(); ctx.ellipse(0, 5, 52, 22, 0, 0, Math.PI * 2); ctx.fill();
  for (const [a, b, r] of [[-24, -6, 21], [1, -18, 29], [29, -6, 21]]) { ctx.beginPath(); ctx.arc(a, b, r, 0, Math.PI * 2); ctx.fill(); } ctx.restore();
}
function star(cx, cy, radius, spin) {
  ctx.save(); ctx.translate(cx, cy); ctx.rotate(spin); ctx.beginPath();
  for (let i = 0; i < 10; i++) { const angle = i * Math.PI / 5 - Math.PI / 2, r = i % 2 ? radius * .46 : radius; ctx.lineTo(Math.cos(angle) * r, Math.sin(angle) * r); }
  ctx.closePath(); ctx.fillStyle = '#ecc86b'; ctx.fill(); ctx.strokeStyle = '#d8ad4b'; ctx.lineWidth = 1.5; ctx.stroke(); ctx.restore();
}
function draw() {
  const gradient = ctx.createLinearGradient(0, 0, 0, 560); gradient.addColorStop(0, '#dcece8'); gradient.addColorStop(1, '#f0f1da'); ctx.fillStyle = gradient; ctx.fillRect(0, 0, 800, 560);
  ctx.fillStyle = '#fff8db'; ctx.beginPath(); ctx.arc(640, 110, 44, 0, Math.PI * 2); ctx.fill();
  for (let i = 0; i < 5; i++) cloud(((i * 203 + worldTime * (4 + i)) % 1000) - 100, 100 + i % 3 * 112, .8 + i * .12, .42);
  ctx.fillStyle = '#d2e2cb'; ctx.beginPath(); ctx.moveTo(0, 520); ctx.bezierCurveTo(190, 430, 245, 565, 425, 510); ctx.bezierCurveTo(600, 435, 700, 515, 800, 480); ctx.lineTo(800, 560); ctx.lineTo(0, 560); ctx.fill();
  ctx.fillStyle = '#bed5b7'; ctx.beginPath(); ctx.moveTo(0, 555); ctx.bezierCurveTo(250, 480, 340, 585, 540, 535); ctx.bezierCurveTo(670, 495, 740, 540, 800, 530); ctx.lineTo(800, 560); ctx.lineTo(0, 560); ctx.fill();
  if (phase === 'ready') { star(175, 150, 17, -.2); star(555, 225, 12, .3); star(370, 75, 10, -.1); }
  for (const item of items) {
    if (!item.rock) star(item.x, item.y, 15, item.spin * .3);
    else { ctx.save(); ctx.translate(item.x, item.y); ctx.rotate(item.spin); ctx.fillStyle = '#9d8db2'; ctx.beginPath(); for (let i = 0; i < 7; i++) { const a = i * Math.PI * 2 / 7, r = i % 2 ? 15 : 19; ctx.lineTo(Math.cos(a) * r, Math.sin(a) * r); } ctx.closePath(); ctx.fill(); ctx.fillStyle = '#827198'; ctx.beginPath(); ctx.arc(-5, -4, 5, 0, 7); ctx.fill(); ctx.beginPath(); ctx.arc(7, 5, 3, 0, 7); ctx.fill(); ctx.restore(); }
  }
  for (const p of particles) { ctx.globalAlpha = Math.max(0, p.life / .65); ctx.fillStyle = p.color; ctx.fillRect(p.x, p.y, 5, 5); } ctx.globalAlpha = 1;
  ctx.save(); if (invincible && Math.floor(invincible * 12) % 2) ctx.globalAlpha = .4;
  const cy = 491 + Math.sin(worldTime * 3) * 2;
  cloud(x, cy, 1); ctx.fillStyle = '#526959';
  for (const dx of [-14, 14]) { ctx.beginPath(); ctx.ellipse(x + dx, cy, 2.4, 3.5, 0, 0, 7); ctx.fill(); }
  ctx.strokeStyle = '#526959'; ctx.lineWidth = 1.8; ctx.beginPath(); ctx.arc(x, cy + 4, 5, .1, Math.PI - .1); ctx.stroke();
  ctx.fillStyle = '#efbdaf'; for (const dx of [-25, 25]) { ctx.beginPath(); ctx.ellipse(x + dx, cy + 7, 6, 3, 0, 0, 7); ctx.fill(); } ctx.restore();
}
function frame(now) { const dt = Math.min((now - last) / 1000, .05); last = now; if (phase !== 'paused') worldTime += dt; if (phase === 'playing') update(dt); draw(); requestAnimationFrame(frame); }
requestAnimationFrame(frame); void connect();
