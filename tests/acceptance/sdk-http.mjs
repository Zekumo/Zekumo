import assert from "node:assert/strict";
import {createServer} from "node:http";
import {MiniCloud, MiniCloudError} from "../../sdk/dist/esm/index.js";

const requests = [];
const server = createServer(async (req, res) => {
  let body = "";
  for await (const chunk of req) body += chunk;
  requests.push({method: req.method, url: req.url, headers: req.headers, body});
  res.setHeader("Content-Type", "application/json");

  if (req.method === "POST" && req.url === "/v1/auth/login") {
    res.end(JSON.stringify({
      token: "jwt-contract",
      player: {id: "p1", nickname: "Alice"},
    }));
    return;
  }
  if (req.method === "PUT" && req.url === "/v1/player/data/a%2Fb") {
    res.end(JSON.stringify({key: "a/b", updated_at: "2026-10-04T00:00:00Z"}));
    return;
  }
  if (req.method === "GET" && req.url === "/v1/leaderboards/weekly?limit=10") {
    res.end(JSON.stringify({entries: []}));
    return;
  }
  if (req.method === "GET" && req.url === "/v1/apps/mc_test/announcements?platform=web") {
    res.end(JSON.stringify({announcements: []}));
    return;
  }
  if (req.method === "POST" && req.url === "/v1/currency/gold/spend") {
    res.statusCode = 402;
    res.end(JSON.stringify({error: {code: "insufficient_funds", message: "not enough gold"}}));
    return;
  }
  res.statusCode = 404;
  res.end(JSON.stringify({error: {code: "unexpected_request", message: `${req.method} ${req.url}`}}));
});

await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(0, "127.0.0.1", resolve);
});

try {
  const address = server.address();
  assert(address && typeof address !== "string");
  const mc = new MiniCloud({appId: "mc_test", baseUrl: `http://127.0.0.1:${address.port}/`});

  await assert.rejects(
    mc.achievements.list(),
    (error) => error instanceof MiniCloudError && error.code === "no_token" && error.status === 0,
  );
  assert.equal(requests.length, 0, "an authenticated call without a token must not hit the network");

  await mc.auth.loginAsGuest({deviceId: "device-1", nickname: "Alice"});
  assert.equal(mc.token, "jwt-contract");
  const login = requests.at(-1);
  assert.equal(login.headers.authorization, undefined, "login must be anonymous");
  assert.deepEqual(JSON.parse(login.body), {
    app_id: "mc_test",
    provider: "guest",
    device_id: "device-1",
    nickname: "Alice",
  });

  await mc.playerData.set("a/b", {level: 3});
  const save = requests.at(-1);
  assert.equal(save.url, "/v1/player/data/a%2Fb", "one key cannot escape into another route segment");
  assert.equal(save.headers.authorization, "Bearer jwt-contract");
  assert.deepEqual(JSON.parse(save.body), {level: 3});

  await mc.leaderboards.top("weekly", {limit: 10});
  assert.equal(
    requests.at(-1).url,
    "/v1/leaderboards/weekly?limit=10",
    "unset query options must be omitted rather than sent blank",
  );

  await mc.announcements.list({platform: "web"});
  const announcement = requests.at(-1);
  assert.equal(announcement.headers.authorization, undefined, "pre-login/public APIs must not leak a saved token");

  await assert.rejects(
    mc.currency.spend("gold", 100, "purchase-1"),
    (error) => error instanceof MiniCloudError &&
      error.status === 402 &&
      error.code === "insufficient_funds" &&
      error.message === "not enough gold",
  );

  console.log(`SDK HTTP acceptance OK: ${requests.length} real loopback requests`);
} finally {
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

