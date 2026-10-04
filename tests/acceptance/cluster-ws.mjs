import assert from "node:assert/strict";
import {MiniCloud} from "../../sdk/dist/esm/index.js";

const httpA = required("MINICLOUD_A").replace(/\/+$/, "");
const httpB = required("MINICLOUD_B").replace(/\/+$/, "");
const adminUser = process.env.ADMIN_USERNAME || "admin";
const adminPassword = process.env.ADMIN_PASSWORD || "admin123";
const timeoutMs = Number(process.env.CLUSTER_TEST_TIMEOUT_MS || 5000);

let adminToken = "";
let gameId = "";
const clients = new Set();

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function api(base, method, path, token = "", body) {
  const headers = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const raw = await response.text();
  let parsed;
  try {
    parsed = raw ? JSON.parse(raw) : undefined;
  } catch {
    parsed = raw;
  }
  if (!response.ok) {
    throw new Error(`${method} ${base}${path} -> ${response.status}: ${raw}`);
  }
  return parsed;
}

function waitRealtime(realtime, type, predicate = () => true, label = type) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error(`timed out waiting for ${label}`));
    }, timeoutMs);
    const unsubscribe = realtime.on(type, (data) => {
      if (!predicate(data)) return;
      clearTimeout(timer);
      unsubscribe();
      resolve(data);
    });
  });
}

async function expectNoRealtime(realtime, type, action, waitMs = 800) {
  let seen = false;
  const unsubscribe = realtime.on(type, () => { seen = true; });
  try {
    action();
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  } finally {
    unsubscribe();
  }
  assert.equal(seen, false, `blocked recipient unexpectedly received ${type}`);
}

function sdk(baseUrl, appId, token) {
  const client = new MiniCloud({appId, baseUrl, token});
  clients.add(client);
  return client;
}

try {
  ({token: adminToken} = await api(httpA, "POST", "/admin/api/login", "", {
    username: adminUser,
    password: adminPassword,
  }));
  const suffix = `${Date.now()}-${process.pid}`;
  const game = await api(httpA, "POST", "/admin/api/games", adminToken, {name: `cluster-acceptance-${suffix}`});
  gameId = game.id;

  const alice = await api(httpA, "POST", "/v1/auth/login", "", {
    app_id: game.app_id,
    provider: "guest",
    device_id: `cluster-alice-${suffix}`,
    nickname: "Cluster Alice",
  });
  const bob = await api(httpB, "POST", "/v1/auth/login", "", {
    app_id: game.app_id,
    provider: "guest",
    device_id: `cluster-bob-${suffix}`,
    nickname: "Cluster Bob",
  });

  const aliceA = sdk(httpA, game.app_id, alice.token);
  const bobB = sdk(httpB, game.app_id, bob.token);
  await Promise.all([aliceA.realtime.connect(), bobB.realtime.connect()]);

  const room = await aliceA.realtime.createRoom({name: "cross-instance", maxPlayers: 4});
  assert.match(room.id, /^r_/);
  const joinedForAlice = waitRealtime(
    aliceA.realtime,
    "room.member_joined",
    (data) => data.player_id === bob.player.id,
  );
  const bobSnapshot = await bobB.realtime.joinRoom(room.id);
  await joinedForAlice;
  assert.deepEqual(
    new Set(bobSnapshot.members.map((member) => member.player_id)),
    new Set([alice.player.id, bob.player.id]),
  );

  const msgForAlice = waitRealtime(aliceA.realtime, "room.msg", (data) => data.player_id === bob.player.id);
  bobB.realtime.sendRoomMessage({round: 1});
  assert.deepEqual((await msgForAlice).data, {round: 1});

  const stateForBob = waitRealtime(bobB.realtime, "room.state", (data) => data.player_id === alice.player.id);
  aliceA.realtime.sendState({x: 7, y: 9});
  assert.deepEqual((await stateForBob).state, {x: 7, y: 9});

  await api(httpA, "POST", "/v1/friends/block", alice.token, {target_player_id: bob.player.id});
  await expectNoRealtime(aliceA.realtime, "room.msg", () => bobB.realtime.sendRoomMessage({blocked: true}));

  await bobB.realtime.leaveRoom();
  const blockedSnapshot = await bobB.realtime.joinRoom(room.id);
  const hiddenAlice = blockedSnapshot.members.find((member) => member.player_id === alice.player.id);
  assert(hiddenAlice, "blocked room member identity unexpectedly disappeared");
  assert.equal(hiddenAlice.state, undefined, "room.joined leaked a blocked player's latest state");

  const roomList = await bobB.realtime.listRooms();
  const listed = roomList.find((item) => item.id === room.id);
  assert(listed, "shared room is missing from the other instance's room.list");
  const listedAlice = listed.members.find((member) => member.player_id === alice.player.id);
  assert(listedAlice, "blocked room member identity unexpectedly disappeared from room.list");
  assert.equal(listedAlice.state, undefined, "room.list leaked a blocked player's latest state");

  let retriedAfterReplacement = false;
  const stopWatchingRetry = aliceA.realtime.on("reconnecting", () => { retriedAfterReplacement = true; });
  const replaced = waitRealtime(aliceA.realtime, "replaced");
  const aliceB = sdk(httpB, game.app_id, alice.token);
  await aliceB.realtime.connect();
  await replaced;
  await new Promise((resolve) => setTimeout(resolve, 1200));
  stopWatchingRetry();
  assert.equal(retriedAfterReplacement, false, "a replaced SDK connection tried to reconnect");

  console.log("Cluster SDK/WebSocket acceptance OK: join, broadcast, state, block privacy, room list, and kick");
} finally {
  for (const client of clients) client.realtime.close();
  if (adminToken && gameId) {
    try {
      await api(httpA, "DELETE", `/admin/api/games/${gameId}`, adminToken);
    } catch (error) {
      console.error(`cleanup failed: ${error.message}`);
      process.exitCode = 1;
    }
  }
}

