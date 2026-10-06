// Real API integration check. Uses a separate board and deletes its save slot.
import assert from 'node:assert/strict';
import { defaults, makeClient } from './cloud.js';
const client = makeClient({ ...defaults, baseUrl: process.env.ZEKUMO_URL || defaults.baseUrl, appId: process.env.ZEKUMO_APP_ID || defaults.appId });
const deviceId = 'star-catcher-sdk-smoke-v1';
const key = `star-catcher-smoke-${Date.now()}`;
const first = await client.auth.loginAsGuest({ deviceId, nickname: 'SDK 联调' });
assert.ok(first.player.id); console.log('PASS 游客登录');
try {
  await client.playerData.set(key, { best: 30, rounds: 1 });
  assert.deepEqual(await client.playerData.get(key), { best: 30, rounds: 1 }); console.log('PASS 云存档写入、回读');
  const second = await client.auth.loginAsGuest({ deviceId });
  assert.equal(second.player.id, first.player.id); console.log('PASS 同设备登录恢复身份');
  await client.leaderboards.submit('star-catcher-sdk-smoke', 30, 'max');
  await client.leaderboards.submit('star-catcher-sdk-smoke', 10, 'max');
  const rank = await client.leaderboards.me('star-catcher-sdk-smoke');
  assert.equal(rank.score, 30);
  const top = await client.leaderboards.top('star-catcher-sdk-smoke', { limit: 10 });
  assert.ok(top.some(row => row.player_id === first.player.id && row.score === 30)); console.log('PASS 排行榜提交、max 保留高分、排名读取');
} finally { await client.playerData.delete(key); console.log('CLEANUP 测试存档已删除（保留 SDK 联调玩家及独立测试榜）'); }
