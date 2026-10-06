import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('page-rooms.js', import.meta.url), 'utf8');
function runtime(api) {
  let current = true;
  const controls = new Map();
  const host = { innerHTML: '', isConnected: true, querySelector: selector => {
    if (!controls.has(selector)) controls.set(selector, {});
    return controls.get(selector);
  }};
  const sandbox = { api, operationContext: () => ({ gameId: 'g1', workspaceId: 'w1' }),
    requireCurrentContext: () => { if (!current) throw Error('stale'); }, contextCurrent: () => current,
    skeleton: () => 'loading', esc: value => String(value).replaceAll('<', '&lt;'), fmtNum: String,
    emptyState: (_icon, title) => title, errorState: (title, error) => title + error,
    pageShell: (h, config) => { h.innerHTML = config.actions + config.body; }, encodeURIComponent,
  };
  vm.createContext(sandbox); vm.runInContext(source, sandbox);
  return { host, controls, render: () => sandbox.renderRooms(host), stale: () => { current = false; } };
}
const page = { rooms: [], total: 0, offset: 0, limit: 20, online_players: 0, room_count: 0 };
test('room monitor uses tenant context and renders real empty snapshot', async () => {
  let call;
  const r = runtime(async (...args) => { call = args; return page; });
  await r.render();
  assert.equal(call[1], '/admin/api/games/g1/rooms?offset=0&limit=20');
  assert.equal(call[3].workspaceId, 'w1');
  assert.match(r.host.innerHTML, /没有可显示的房间/);
  assert.match(r.host.innerHTML, /0 位在线玩家/);
});
test('stale workspace response cannot paint room content', async () => {
  let resolve;
  const r = runtime(() => new Promise(ok => { resolve = ok; }));
  const pending = r.render(); r.stale(); resolve(page); await pending;
  assert.match(r.host.innerHTML, /loading/);
  assert.doesNotMatch(r.host.innerHTML, /在线玩家/);
});
test('load error offers retry and successful retry replaces error', async () => {
  let attempts = 0;
  const r = runtime(async () => { if (++attempts === 1) throw Error('unavailable'); return page; });
  await r.render(); assert.match(r.host.innerHTML, /unavailable/);
  await r.controls.get('[data-room-retry]').onclick();
  assert.match(r.host.innerHTML, /在线玩家/);
});
test('pagination is bounded and room names are escaped', async () => {
  const calls = [];
  const r = runtime(async (_method, path) => { calls.push(path); return { ...page, total: 25, rooms: [{ id:'r', name:'<img>',owner_id:'owner',member_count:1,max_players:2,locked:true }] }; });
  await r.render(); assert.match(r.host.innerHTML, /&lt;img>/); assert.match(r.host.innerHTML, /已锁定/);
  await r.controls.get('[data-room-next]').onclick(); assert.match(calls[1], /offset=20&limit=20$/);
});
