import assert from "node:assert/strict";
import net from "node:net";
import {execFile} from "node:child_process";
import {MiniCloud} from "../../sdk/dist/esm/index.js";

const base = required("MINICLOUD_A").replace(/\/+$/, "");
const redisHost = process.env.REDIS_HOST || "127.0.0.1";
const redisPort = Number(process.env.REDIS_PORT || 6379);
const timeoutMs = Number(process.env.CLUSTER_TEST_TIMEOUT_MS || 5000);
const redisStartScript = required("REDIS_START_SCRIPT");
const clients = new Set();
let adminToken = "";
let gameId = "";

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function api(method, path, token = "", body) {
  const headers = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const raw = await response.text();
  if (!response.ok) throw new Error(`${method} ${path} -> ${response.status}: ${raw}`);
  return raw ? JSON.parse(raw) : undefined;
}

function waitRealtime(realtime, type, predicate = () => true) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error(`timed out waiting for ${type}`));
    }, timeoutMs);
    const unsubscribe = realtime.on(type, (data) => {
      if (!predicate(data)) return;
      clearTimeout(timer);
      unsubscribe();
      resolve(data);
    });
  });
}

function runFile(path) {
  return new Promise((resolve, reject) => {
    execFile(path, {timeout: timeoutMs}, (error, stdout, stderr) => {
      if (error) reject(new Error(`${path} failed: ${stderr || stdout || error.message}`));
      else resolve();
    });
  });
}

async function waitForRecovery() {
  const deadline = Date.now() + timeoutMs * 3;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(base + "/readyz");
      if (response.ok) return;
    } catch {
      // Redis or the server is still reconnecting.
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("server readiness did not recover after Redis restart");
}

async function waitForOutage() {
  const deadline = Date.now() + timeoutMs * 3;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(base + "/readyz");
      if (!response.ok) return;
    } catch {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("server readiness stayed healthy after Redis shutdown");
}

function sdk(appId, token) {
  const client = new MiniCloud({appId, baseUrl: base, token});
  clients.add(client);
  return client;
}

async function stopRedis() {
  await new Promise((resolve, reject) => {
    const socket = net.createConnection({host: redisHost, port: redisPort});
    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error("timed out stopping Redis"));
    }, timeoutMs);
    let sent = false;
    const done = () => {
      clearTimeout(timer);
      resolve();
    };
    socket.once("connect", () => {
      sent = true;
      socket.write("*2\r\n$8\r\nSHUTDOWN\r\n$6\r\nNOSAVE\r\n");
    });
    socket.once("close", done);
    socket.once("error", (error) => sent ? done() : reject(error));
  });
}

try {
  ({token: adminToken} = await api("POST", "/admin/api/login", "", {
    username: process.env.ADMIN_USERNAME || "admin",
    password: process.env.ADMIN_PASSWORD || "admin123",
  }));
  const suffix = `${Date.now()}-${process.pid}`;
  const game = await api("POST", "/admin/api/games", adminToken, {name: `redis-outage-${suffix}`});
  gameId = game.id;
  const login = (name) => api("POST", "/v1/auth/login", "", {
    app_id: game.app_id,
    provider: "guest",
    device_id: `outage-${name}-${suffix}`,
    nickname: name,
  });
  const [alice, bob] = await Promise.all([login("Alice"), login("Bob")]);
  const aliceSDK = sdk(game.app_id, alice.token);
  const bobSDK = sdk(game.app_id, bob.token);
  await Promise.all([aliceSDK.realtime.connect(), bobSDK.realtime.connect()]);
  const room = await aliceSDK.realtime.createRoom({name: "local-fallback"});
  await bobSDK.realtime.joinRoom(room.id);

  await stopRedis();
  await waitForOutage();

  const localMessage = waitRealtime(
    aliceSDK.realtime,
    "room.msg",
    (data) => data.player_id === bob.player.id,
  );
  bobSDK.realtime.sendRoomMessage({during: "redis-outage"});
  assert.deepEqual((await localMessage).data, {during: "redis-outage"});

  const carol = await login("Carol");
  const carolSDK = sdk(game.app_id, carol.token);
  const carolRetrying = waitRealtime(carolSDK.realtime, "reconnecting");
  await assert.rejects(carolSDK.realtime.connect(), /connection closed|ws_closed/i);
  await carolRetrying;
  const carolRecovered = waitRealtime(carolSDK.realtime, "open");
  await runFile(redisStartScript);
  await waitForRecovery();
  await carolRecovered;
  assert.equal(carolSDK.realtime.connected, true,
    "SDK must reconnect after Redis and server readiness recover");

  console.log("Redis outage acceptance OK: local fallback, upgrade rejection, and SDK recovery passed");
} finally {
  for (const client of clients) client.realtime.close();
  if (adminToken && gameId) {
    try {
      await api("DELETE", `/admin/api/games/${gameId}`, adminToken);
    } catch (error) {
      console.error(`cleanup failed: ${error.message}`);
      process.exitCode = 1;
    }
  }
}
