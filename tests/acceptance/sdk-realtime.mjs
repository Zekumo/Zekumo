import assert from "node:assert/strict";
import {MiniCloud} from "../../sdk/dist/esm/index.js";

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

class FakeWebSocket {
  static instances = [];

  readyState = 0;
  sent = [];
  listeners = new Map();

  constructor(url) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  emit(type, event = {}) {
    if (type === "open") this.readyState = 1;
    if (type === "close") this.readyState = 3;
    for (const listener of this.listeners.get(type) || []) listener(event);
  }

  send(payload) {
    if (this.readyState !== 1) throw new Error("socket is not open");
    this.sent.push(JSON.parse(payload));
  }

  close(code = 1000, reason = "") {
    if (this.readyState >= 2) return;
    this.emit("close", {code, reason});
  }
}

function once(realtime, type, timeoutMs = 2000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error(`timed out waiting for ${type}`));
    }, timeoutMs);
    const unsubscribe = realtime.on(type, (value) => {
      clearTimeout(timer);
      unsubscribe();
      resolve(value);
    });
  });
}

async function waitForInstance(count, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs;
  while (FakeWebSocket.instances.length < count && Date.now() < deadline) await sleep(10);
  assert.equal(FakeWebSocket.instances.length, count, `expected ${count} WebSocket instances`);
  return FakeWebSocket.instances[count - 1];
}

const originalRandom = Math.random;
Math.random = () => 0; // first retry is deterministically 500 ms

try {
  const mc = new MiniCloud({
    appId: "mc_realtime_contract",
    baseUrl: "https://api.example.test",
    token: "jwt-contract",
    webSocket: FakeWebSocket,
  });

  const firstConnect = mc.realtime.connect();
  const first = await waitForInstance(1);
  first.emit("open");
  await firstConnect;

  const subscribed = mc.realtime.subscribeChat("world");
  assert.deepEqual(first.sent.at(-1), {type: "chat.sub", data: {channel: "world"}});
  first.emit("message", {data: JSON.stringify({type: "chat.subbed", data: {channel: "world"}})});
  await subscribed;

  const retrying = once(mc.realtime, "reconnecting");
  first.emit("close", {code: 1006, reason: "temporary dependency outage"});
  const retry = await retrying;
  assert.equal(retry.attempt, 1);
  assert.equal(retry.delay, 500);

  const reopened = once(mc.realtime, "open");
  const second = await waitForInstance(2);
  second.emit("open");
  assert.deepEqual(await reopened, {reconnected: true});
  assert.deepEqual(second.sent.at(-1), {type: "chat.sub", data: {channel: "world"}},
    "a restored connection must resubscribe saved chat channels");

  const retryingAgain = once(mc.realtime, "reconnecting");
  second.emit("close", {code: 1006, reason: "outage again"});
  await retryingAgain;
  mc.realtime.close();
  await sleep(650);
  assert.equal(FakeWebSocket.instances.length, 2,
    "manual close during backoff must cancel the pending reconnect");

  console.log("SDK realtime acceptance OK: bounded retry, resubscribe, and backoff cancellation");
} finally {
  Math.random = originalRandom;
}
