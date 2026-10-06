# zekumo-sdk

Official JavaScript / TypeScript SDK for Zekumo. Zero dependencies, ESM + CJS, works in browsers and Node.js 18+ (Node < 22 needs a WebSocket implementation passed in for realtime).

```bash
npm install zekumo-sdk
```

## Quick start

```ts
import Zekumo from "zekumo-sdk";

const mc = new Zekumo({ appId: "zk_xxx", baseUrl: "https://api.example.com" });

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
const mc = new Zekumo({ appId, baseUrl, webSocket: WebSocket as never });
```

## Covered APIs

`auth` (guest / password / SSO ticket) · `player` profile/bind · `playerData` saves · `leaderboards` (global/friends scope) · `achievements` · `friends` · `currency` · `mailbox` · `announcements` · `functions` (cloud functions) · `updates` check/history · `logs` · `dialogues` · `chat` history · `kv` (public namespaces) · `realtime` (rooms + chat over WebSocket).

Errors are thrown as `ZekumoError { status, code, message }`.

## Build

```bash
npm install && npm run build
```

## Playable SDK example

Run `npm run demo` from `sdk/`, then open <http://127.0.0.1:5178/>.
[云间拾星 / Star Catcher](examples/star-catcher/README.md) is a Canvas game using
the actual SDK for guest login, cloud saves, and leaderboards. Its connection
settings are editable in the game; no app secret or admin credentials are needed.

## Room lobby and owner controls

```ts
await mc.realtime.connect();
const { rooms, total } = await mc.realtime.listRoomsPage({ offset: 0, limit: 20 });
const room = await mc.realtime.createRoom({ name: "Co-op", maxPlayers: 8 });
await mc.realtime.updateRoom({ locked: true });
const current = await mc.realtime.getRoom();
// Owner-only actions; target must already be a member of this room:
// await mc.realtime.kickRoomMember(playerId);
// await mc.realtime.transferRoom(playerId);
mc.realtime.on("room.updated", snapshot => console.log(snapshot));
mc.realtime.on("room.kicked", ({ reason }) => console.log(reason));
mc.realtime.on("room.owner_changed", ({ owner_id }) => console.log(owner_id));
```

`listRooms()` now returns lobby summaries, not full member snapshots. Use
`listRoomsPage()` for `total`, `offset`, and `limit`; pages default to 20 and are
capped at 50. Only room members can call `getRoom()` to obtain member state.
Await each realtime request before issuing another: the wire protocol does not
have request IDs. Room update events are also broadcast to members.

Room names are limited to 128 UTF-8 bytes, metadata to 8 KiB, and each player
state/message to 16 KiB. Capacity is 1–200 (default 20); an owner cannot shrink
below the current membership. Omitted update fields stay unchanged; `meta: null`
clears metadata. Locked rooms reject new joins. Kicking does not ban an account;
use game moderation separately when appropriate.

Rooms and their state are process-local and ephemeral. Reconnect does not
restore room membership automatically. This is lightweight room synchronization,
not a persistent MMO world or an authoritative simulation. The admin console's
实时房间 page shows a manually refreshed, tenant-scoped snapshot of room counts,
connections, occupancy, and locks without player state or room metadata.
