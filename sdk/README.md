# minicloud-sdk

Official JavaScript / TypeScript SDK for MiniCloud. Zero dependencies, ESM + CJS, works in browsers and Node.js 18+ (Node < 22 needs a WebSocket implementation passed in for realtime).

```bash
npm install minicloud-sdk
```

## Quick start

```ts
import MiniCloud from "minicloud-sdk";

const mc = new MiniCloud({ appId: "mc_xxx", baseUrl: "https://api.example.com" });

await mc.auth.loginAsGuest({ deviceId: "device-123" });

await mc.playerData.set("save1", { level: 3 });
const save = await mc.playerData.get("save1");

await mc.leaderboards.submit("weekly", 4200);
const top = await mc.leaderboards.top("weekly", { limit: 10 });

const notices = await mc.announcements.list({ platform: "web", channel: "stable" });

const { mail, unread } = await mc.mailbox.list();
if (mail[0]?.rewards.length) await mc.mailbox.claim(mail[0].id);

const balances = await mc.currency.balances();
await mc.currency.spend(balances[0].currency_id, 100, crypto.randomUUID());
```

## Realtime

```ts
await mc.realtime.connect(); // auto-reconnects with exponential backoff

mc.realtime.on("room.state", ({ player_id, state }) => updatePlayer(player_id, state));
mc.realtime.on("chat.msg", (m) => showMessage(m));

const room = await mc.realtime.createRoom({ name: "lobby", maxPlayers: 8 });
mc.realtime.sendState({ x: 10, y: 20 });

await mc.realtime.subscribeChat("global");
mc.realtime.sendChat("global", "hello!");
```

Node < 22:

```ts
import WebSocket from "ws";
const mc = new MiniCloud({ appId, baseUrl, webSocket: WebSocket as never });
```

## Covered APIs

`auth` (guest / password / SSO ticket) · `player` profile/bind · `playerData` saves · `leaderboards` (global/friends scope) · `achievements` · `friends` · `currency` · `mailbox` · `announcements` · `functions` (cloud functions) · `updates` check/history · `logs` · `dialogues` · `chat` history · `kv` (public namespaces) · `realtime` (rooms + chat over WebSocket).

Errors are thrown as `MiniCloudError { status, code, message }`.

## Build

```bash
npm install && npm run build
```
