import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

const baseURL = process.env.ZEKUMO_URL || 'http://127.0.0.1:8080';
const outputDir = process.env.CONSOLE_SCREENSHOT_DIR || 'artifacts/console-e2e';
const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
const password = 'Console-e2e-42';

async function json(path, { method = 'GET', token, workspaceId, body } = {}) {
  const response = await fetch(baseURL + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(workspaceId ? { 'X-Zekumo-Workspace-ID': workspaceId } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(`${method} ${path}: ${response.status} ${data.error?.code || ''} ${data.error?.message || ''}`);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

async function expectStatus(status, work) {
  try {
    await work();
    assert.fail(`expected HTTP ${status}`);
  } catch (error) {
    assert.equal(error.status, status, error.message);
    return error.data;
  }
}

async function register(username) {
  return json('/sso/api/register', {
    method: 'POST', body: { username, password, nickname: username },
  });
}

async function loginPage(page, username, pass) {
  await page.goto(`${baseURL}/admin/`, { waitUntil: 'networkidle' });
  await page.locator('#loginUser').fill(username);
  await page.locator('#loginPass').fill(pass);
  await page.locator('#loginSubmit').click();
  await page.locator('.workspace-switcher select').waitFor({ state: 'visible' });
}

await mkdir(outputDir, { recursive: true });

// Build the fixture through the public API against the same Postgres/Redis
// server the browser will use.
const admin = await json('/admin/api/login', {
  method: 'POST', body: { username: 'admin', password: 'admin123' },
});
const workspaceA = admin.workspaces.find(item => item.id === admin.default_workspace_id) || admin.workspaces[0];
const created = await json('/admin/api/workspaces', {
  method: 'POST', token: admin.token,
  body: { name: `Console E2E ${stamp}`, slug: `console-e2e-${stamp}`.toLowerCase() },
});
const workspaceB = created.workspace;
const mascotWorkspace = (await json('/admin/api/workspaces', {
  method: 'POST', token: admin.token,
  body: { name: `Mascot preview ${stamp}`, slug: `mascot-e2e-${stamp}`.toLowerCase() },
})).workspace;
const gameA = await json('/admin/api/games', {
  method: 'POST', token: admin.token, workspaceId: workspaceA.id,
  body: { name: `Alpha only ${stamp}` },
});
const gameB = await json('/admin/api/games', {
  method: 'POST', token: admin.token, workspaceId: workspaceB.id,
  body: { name: `Beta only ${stamp}` },
});

const viewerName = `viewer-${stamp}`.slice(0, 60);
const uiMemberName = `member-${stamp}`.slice(0, 60);
await register(viewerName);
await register(uiMemberName);
await json(`/admin/api/workspaces/${workspaceB.id}/members`, {
  method: 'POST', token: admin.token, workspaceId: workspaceB.id,
  body: { account_username: viewerName, role: 'viewer' },
});

const browser = await chromium.launch({ headless: true });
const ownerContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const ownerPage = await ownerContext.newPage();
const ownerErrors = [];
ownerPage.on('pageerror', error => ownerErrors.push(error));

try {
  await loginPage(ownerPage, 'admin', 'admin123');

  // The actual generated mascot must load in the served console, rather than
  // merely existing in the repository.
  await ownerPage.locator('.workspace-switcher select').selectOption(mascotWorkspace.id);
  await ownerPage.waitForFunction(
    id => location.hash.includes(id) && document.querySelector('.workspace-switcher select')?.value === id,
    mascotWorkspace.id,
  );
  await ownerPage.getByRole('button', { name: '游戏', exact: true }).click();
  const emptyMascot = ownerPage.locator('.mascot-state.empty img');
  await emptyMascot.waitFor();
  assert.equal(await emptyMascot.evaluate(image => image.complete && image.naturalWidth > 0), true);
  await ownerPage.screenshot({ path: `${outputDir}/mascot-empty.png`, fullPage: true });

  // Membership creation and role update use the real server, not a DOM stub.
  await ownerPage.locator('.workspace-switcher select').selectOption(workspaceB.id);
  await ownerPage.waitForFunction(
    id => location.hash.includes(id) && document.querySelector('.workspace-switcher select')?.value === id,
    workspaceB.id,
  );
  await ownerPage.getByRole('button', { name: '成员与权限' }).click();
  await ownerPage.getByRole('heading', { name: '成员与权限' }).waitFor();
  await ownerPage.getByRole('button', { name: '添加成员' }).click();
  await ownerPage.locator('#memberUsername').fill(uiMemberName);
  await ownerPage.locator('#memberRole').selectOption('editor');
  await ownerPage.screenshot({ path: `${outputDir}/member-add-dialog.png`, fullPage: true });
  await ownerPage.locator('#dlgConfirm').click();
  const memberCard = ownerPage.locator('.member-card').filter({ hasText: uiMemberName });
  await memberCard.waitFor();
  await memberCard.locator('select').selectOption('viewer');
  await ownerPage.waitForFunction(
    name => [...document.querySelectorAll('.member-card')].some(card =>
      card.textContent.includes(name) && card.querySelector('select')?.value === 'viewer'),
    uiMemberName,
  );
  await ownerPage.screenshot({ path: `${outputDir}/workspace-members.png`, fullPage: true });

  // Delay A after the request has started, then switch back to B. The older A
  // response must not repaint B or overwrite B's game cache.
  let delayAlpha = true;
  await ownerPage.route('**/admin/api/games', async route => {
    const workspace = route.request().headers()['x-zekumo-workspace-id'];
    if (delayAlpha && workspace === workspaceA.id) await new Promise(resolve => setTimeout(resolve, 700));
    await route.continue();
  });
  await ownerPage.locator('.workspace-switcher select').selectOption(workspaceA.id);
  await ownerPage.waitForFunction(
    id => location.hash.includes(id) && document.querySelector('.workspace-switcher select')?.value === id,
    workspaceA.id,
  );
  await ownerPage.locator('.workspace-switcher select').selectOption(workspaceB.id);
  await ownerPage.waitForFunction(
    id => location.hash.includes(id) && document.querySelector('.workspace-switcher select')?.value === id,
    workspaceB.id,
  );
  await ownerPage.waitForTimeout(900);
  delayAlpha = false;
  assert.match(ownerPage.url(), new RegExp(workspaceB.id));
  assert.equal(await ownerPage.locator('.workspace-switcher select').inputValue(), workspaceB.id);
  await ownerPage.getByRole('button', { name: '游戏', exact: true }).click();
  await ownerPage.getByText(gameB.name, { exact: true }).waitFor();
  assert.equal(await ownerPage.getByText(gameA.name, { exact: true }).count(), 0);
  await ownerPage.screenshot({ path: `${outputDir}/stale-switch.png`, fullPage: true });

  // Viewer role: server rejects writes and redacts secrets; UI disables them
  // and never stores the secret value in the rendered document.
  const viewer = await json('/admin/api/login', {
    method: 'POST', body: { username: viewerName, password },
  });
  const viewerGames = await json('/admin/api/games', {
    token: viewer.token, workspaceId: workspaceB.id,
  });
  assert.equal(Object.hasOwn(viewerGames.games[0], 'app_secret'), false);
  await expectStatus(403, () => json('/admin/api/games', {
    method: 'POST', token: viewer.token, workspaceId: workspaceB.id, body: { name: 'forbidden' },
  }));

  const viewerContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const viewerPage = await viewerContext.newPage();
  const viewerErrors = [];
  viewerPage.on('pageerror', error => viewerErrors.push(error));
  await loginPage(viewerPage, viewerName, password);
  await viewerPage.getByRole('button', { name: '游戏', exact: true }).click();
  const createButton = viewerPage.getByRole('button', { name: '创建游戏' });
  await createButton.waitFor();
  assert.equal(await createButton.isDisabled(), true);
  await viewerPage.getByText('密钥已隐藏', { exact: true }).waitFor();
  assert.equal((await viewerPage.content()).includes(gameB.app_secret), false);
  await viewerPage.screenshot({ path: `${outputDir}/viewer-games.png`, fullPage: true });
  assert.deepEqual(viewerErrors, []);
  await viewerContext.close();

  assert.deepEqual(ownerErrors, []);
  console.log(`console e2e passed; screenshots: ${outputDir}`);
} finally {
  await ownerContext.close();
  await browser.close();
}
