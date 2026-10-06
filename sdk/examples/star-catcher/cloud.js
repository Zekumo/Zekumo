import Zekumo from '../../dist/esm/index.js';

export const SAVE_KEY = 'star-catcher-v1';
export const BOARD = 'star-catcher-v1';
export const defaults = { baseUrl: 'https://api.zekumo.com', appId: 'zk_9c49b160c312', nickname: '拾星旅人' };
export function cleanSave(value) {
  const integer = n => Number.isSafeInteger(n) && n >= 0 ? n : 0;
  return { best: integer(value?.best), rounds: integer(value?.rounds) };
}
export function makeClient(config) {
  return new Zekumo({ ...config, fetch: (url, options) => fetch(url, { ...options, signal: AbortSignal.timeout(10000) }) });
}
export async function loadSave(client) {
  try { return cleanSave(await client.playerData.get(SAVE_KEY)); }
  catch (error) { if (error.status === 404) return cleanSave(null); throw error; }
}
export async function syncSave(client, save) {
  // "max" makes retrying the same best score safe.
  await client.leaderboards.submit(BOARD, save.best, 'max');
  await client.playerData.set(SAVE_KEY, save);
}
