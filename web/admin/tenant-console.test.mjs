import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';

function deferred() {
  let resolve, reject;
  const promise = new Promise((ok, fail) => { resolve = ok; reject = fail; });
  return { promise, resolve, reject };
}

function element() {
  return {
    innerHTML: '', textContent: '', value: '', disabled: false, isConnected: true,
    style: {}, dataset: {}, children: [],
    classList: { add() {}, remove() {}, toggle() {} },
    setAttribute() {}, removeAttribute() {}, focus() {}, replaceChildren(...children) { this.children = children; },
    querySelector() { return null; }, querySelectorAll() { return []; },
    appendChild(child) { this.children.push(child); },
  };
}

async function consoleRuntime(fetchImpl) {
  const elements = new Map();
  const storage = new Map();
  const document = {
    body: { toggleAttribute() {}, removeAttribute() {} },
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, element());
      return elements.get(id);
    },
    createElement() { return element(); },
    addEventListener() {},
    querySelectorAll() { return []; },
  };
  const sandbox = {
    console, document,
    window: { addEventListener() {} },
    location: { hash: '' },
    history: { replaceState(_s, _t, hash) { sandbox.location.hash = hash; } },
    localStorage: {
      getItem(key) { return storage.get(key) || null; },
      setItem(key, value) { storage.set(key, value); },
      removeItem(key) { storage.delete(key); },
    },
    fetch: fetchImpl,
    themeLabel: () => '跟随系统',
    setTimeout, clearTimeout, URLSearchParams,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  for (const file of ['core.js', 'account.js', 'nav.js']) {
    const code = await readFile(new URL(file, import.meta.url), 'utf8');
    vm.runInContext(code, sandbox, { filename: file });
  }
  vm.runInContext(`globalThis.testAPI = {
    state, api, activateWorkspace, routeHash, currentRoute, logout, openDialog,
    secretCell, applySurfaceAccess,
    setToken(value) { token = value }, getToken() { return token }
  }`, sandbox);
  return { ...sandbox.testAPI, sandbox, elements, storage };
}

function response(status, data) {
  return { status, ok: status >= 200 && status < 300, statusText: String(status), json: async () => data };
}

test('workspace switching ignores stale responses and scopes every request header', async () => {
  const pending = [];
  const runtime = await consoleRuntime((path, init) => {
    const d = deferred();
    pending.push({ path, init, ...d });
    return d.promise;
  });
  runtime.setToken('session');
  runtime.state.sessionEpoch = 1;
  runtime.state.workspaces = [
    { id: 'A', name: 'Alpha', role: 'owner' },
    { id: 'B', name: 'Beta', role: 'viewer' },
  ];

  const first = runtime.activateWorkspace('A');
  const second = runtime.activateWorkspace('B');
  assert.deepEqual(pending.map(p => p.init.headers['X-Zekumo-Workspace-ID']), ['A', 'A', 'B', 'B']);

  pending[2].resolve(response(200, { games: [{ id: 'b-game', name: 'B' }] }));
  pending[3].resolve(response(200, { user: { username: 'beta' }, workspaces: runtime.state.workspaces, active_role: 'viewer' }));
  assert.equal(await second, true);
  pending[0].resolve(response(200, { games: [{ id: 'a-game', name: 'A' }] }));
  pending[1].resolve(response(200, { user: { username: 'alpha' }, workspaces: runtime.state.workspaces, active_role: 'owner' }));
  assert.equal(await first, false);

  assert.equal(runtime.state.workspaceId, 'B');
  assert.equal(runtime.state.activeRole, 'viewer');
  assert.equal(runtime.state.games[0].id, 'b-game');
});

test('stale 401 cannot log out a newer session', async () => {
  const old = deferred();
  const runtime = await consoleRuntime(() => old.promise);
  runtime.setToken('old-token');
  runtime.state.sessionEpoch = 4;
  const request = runtime.api('GET', '/admin/api/games').catch(error => error);
  runtime.setToken('new-token');
  runtime.state.sessionEpoch = 5;
  old.resolve(response(401, { error: { message: 'expired' } }));
  await request;
  assert.equal(runtime.getToken(), 'new-token');
  assert.equal(runtime.state.sessionEpoch, 5);
});

test('browser routes retain workspace context for games and platform pages', async () => {
  const runtime = await consoleRuntime(async () => response(200, {}));
  runtime.state.workspaceId = 'workspace-1';
  assert.equal(runtime.routeHash('overview', 'game-9'), '#/w/workspace-1/g/game-9/overview');
  assert.equal(runtime.routeHash('members', null), '#/w/workspace-1/members');
  runtime.sandbox.location.hash = '#/w/workspace-2/g/game-3/players';
  assert.deepEqual(
    JSON.parse(JSON.stringify(runtime.currentRoute())),
    { workspaceId: 'workspace-2', gameId: 'game-3', page: 'players' },
  );
});

test('dialog confirm is single-flight and cannot be cancelled mid-submit', async () => {
  const runtime = await consoleRuntime(async () => response(200, {}));
  const work = deferred();
  let calls = 0;
  runtime.openDialog({ title: 'Create', body: '', onConfirm: () => { calls += 1; return work.promise; } });
  const confirm = runtime.elements.get('dlgConfirm');
  confirm.onclick();
  confirm.onclick();
  assert.equal(calls, 1);
  assert.equal(confirm.disabled, true);
  work.resolve();
  await Promise.resolve();
});

test('viewer secrets stay out of DOM memory and role minimums disable writes', async () => {
  const runtime = await consoleRuntime(async () => response(200, {}));
  runtime.state.activeRole = 'viewer';
  assert.match(runtime.secretCell('never-store-me', 'secret'), /密钥已隐藏/);
  assert.equal(Object.keys(runtime.sandbox.window).some(key => key.startsWith('__sec_')), false);

  const editorButton = { dataset: {}, disabled: false, setAttribute() {} };
  const adminButton = { dataset: { minRole: 'admin' }, disabled: false, setAttribute() {} };
  const host = { querySelectorAll: () => [editorButton, adminButton] };
  runtime.applySurfaceAccess(host);
  assert.equal(editorButton.disabled, true);
  assert.equal(adminButton.disabled, true);

  runtime.state.activeRole = 'editor';
  editorButton.disabled = false;
  adminButton.disabled = false;
  runtime.applySurfaceAccess(host);
  assert.equal(editorButton.disabled, false);
  assert.equal(adminButton.disabled, true);
});

test('logout clears tenant caches, secrets, dialogs, and the password field', async () => {
  const runtime = await consoleRuntime(async () => response(200, {}));
  runtime.setToken('session');
  runtime.state.games = [{ id: 'old-game' }];
  runtime.state.gameId = 'old-game';
  runtime.state.workspaceId = 'old-workspace';
  runtime.state.activeRole = 'owner';
  runtime.sandbox.window.__sec_old = 'sensitive';
  runtime.sandbox.document.getElementById('loginPass').value = 'password';
  runtime.openDialog({ title: 'Old dialog', body: '', onConfirm: async () => {} });

  runtime.logout();

  assert.equal(runtime.getToken(), '');
  assert.deepEqual(JSON.parse(JSON.stringify(runtime.state.games)), []);
  assert.equal(runtime.state.workspaceId, null);
  assert.equal(runtime.state.activeRole, null);
  assert.equal(runtime.sandbox.window.__sec_old, undefined);
  assert.equal(runtime.elements.get('loginPass').value, '');
  assert.equal(runtime.elements.get('dialogHost').innerHTML, '');
});
