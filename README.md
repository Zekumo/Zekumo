# Zekumo — 小游戏后端即服务(BaaS)

面向小游戏(H5 / 微信小游戏 / 独立小品游戏)的多租户后端平台,用 Go 实现。
一个服务同时给多款游戏提供:玩家认证、玩家数据(存档)、排行榜、剧情对话数据、
玩家聊天、基于 WebSocket 的房间实时同步(MMO 轻量版),以及**版本发布与更新分发**
(控制台发版本,客户端检查更新并下载)。
还内置**通行证 SSO**:一个平台账号通行所有游戏,支持票据换登录、托管授权页和存量玩家绑定。
在此之上还有:**OAuth2 授权体系**(第三方应用接入通行证登录,PKCE + refresh 轮换)、
**云函数**(服务端 JS,HTTP/定时触发)、**WebHook 事件推送**(HMAC 签名 + 重试)和
**访问统计**(DAU/登录/新增/留存/漏斗,控制台图表)。
运营侧还有:**虚拟货币**(幂等账本)、**游戏内邮件**(附件奖励)、**玩家封禁**(即时生效)、
**游戏级 KV**(活动开关热更新)和**数据导出**(GDPR 数据可携带)。
客户端接入有**官方 JS/TS SDK**(`sdk/`)和**Go SDK**(`sdk-go/`,Windows / Linux / macOS)。

```
游戏客户端 ──HTTP──▶  /v1/*         玩家 API(JWT 鉴权,按游戏隔离)
           ──WS───▶  /v1/ws        实时网关(房间同步 + 聊天)
更新器    ──HTTP──▶  /v1/apps/{app_id}/updates/check   检查更新(无需登录)
开发者    ──浏览器─▶  /admin/       Web 控制台(建游戏、看玩家、编剧情、发版本)
                     │
              Go 单体服务 ── PostgreSQL(持久数据) + Redis(排行榜/缓存)
                          └─ 产物存储(本地磁盘 或 S3/MinIO,预签名直传)
```

## 快速开始

### 本机开发(Windows,无 Docker Desktop)

```powershell
# 1. 启动依赖(WSL 内 Docker 跑 Postgres + Redis,含 WSL 保活)
.\scripts\dev-deps.ps1

# 2. 启动服务(8080 被占用时换端口)
$env:ZEKUMO_ADDR = ":8090"
$env:BANNED_WORDS_FILE = "$PWD\configs\banned_words.txt"
go run ./cmd/server

# 3. 跑端到端冒烟测试(覆盖全部 API + WebSocket)
$env:ZEKUMO_URL = "http://localhost:8090"
$env:ZEKUMO_WS  = "ws://localhost:8090"
go run ./cmd/smoketest
```

打开 <http://localhost:8090/> 查看官网，进入 <http://localhost:8090/admin/> 使用控制台，默认账号 `admin` / `admin123`,
创建游戏后拿到 `app_id`,客户端就可以登录了。

### 服务器部署(Docker Compose)

```bash
cp .env.example .env
# 生成密钥并填进 .env:openssl rand -hex 32
docker compose -f docker-compose.prod.yml --env-file .env up -d --build
```

`docker-compose.yml` 是本地开发用的(有默认口令、端口直接暴露)。
生产用 `docker-compose.prod.yml`,区别是:机密没有默认值、缺失即启动失败;
`ZEKUMO_ENV=production`;应用端口只绑 `127.0.0.1`(TLS 由你前面的
Nginx / Caddy / CDN 终止);数据库和 Redis 不对外暴露;带重启策略、
健康检查、内存上限和日志轮转。

## 上线清单

**必须做完再开公网:**

- [ ] `.env` 里设好 `JWT_SECRET`(≥32 字符随机值)、`ADMIN_PASSWORD`、`POSTGRES_PASSWORD`
- [ ] `BASE_URL` 填真实域名(填 localhost 会让客户端下载不到更新产物)
- [ ] `ZEKUMO_ENV=production` —— 上面几项没改好时它会**拒绝启动**,而不是打个日志继续跑
- [ ] 反代终止 TLS,并且**覆写** `X-Forwarded-For`;确认 `TRUST_PROXY=true`
      (不开的话所有请求的来源 IP 都是反代 IP,按 IP 限流形同虚设;
      反代不覆写头就开,任何人都能伪造来源绕过限流)
- [ ] `FUNC_HTTP_ALLOW_PRIVATE` 保持 `false`(production 模式下开着会拒绝启动)
- [ ] 多实例部署时把 `STORAGE_DRIVER` 改成 `s3`;`local` 驱动的产物只在单机磁盘上
- [ ] 挂上备份:`scripts/backup.sh` 加进 crontab,并**实际演练一次恢复**
- [ ] 确认 `STATS_TZ` 是你要的时区(上线后再改会让历史数据的分日归属发生变化)

**探针怎么接:**

| 端点 | 用途 | 说明 |
|---|---|---|
| `/healthz` | 存活(liveness) | 只表示进程活着,不查依赖 —— 数据库抖一下就重启会把故障变成崩溃循环 |
| `/readyz` | 就绪(readiness) | 实查 Postgres 与 Redis,任一不可用返回 503 并带上原因,让反代把这个实例摘掉 |

Docker 的 `HEALTHCHECK` 用的是 `/readyz`。K8s 建议 liveness 指 `/healthz`、
readiness 指 `/readyz`。

**上线后确认:**

- [ ] 控制台仪表盘顶部**没有**红色的「部署尚未加固」横幅(有就说明还在用默认凭据)
- [ ] 「平台状态」卡片里 Postgres / Redis 都是正常、版本号是你部署的那个
- [ ] 跑一遍冒烟测试打到线上环境(会创建并删除一个临时游戏):
      `ZEKUMO_URL=https://api.example.com go run ./cmd/smoketest`
      注意:出站 HTTP 那几步依赖 `FUNC_HTTP_ALLOW_PRIVATE`,生产环境下会失败,属预期

**版本号**:构建时用 ldflags 打进二进制,`/readyz` 和控制台都会显示,
用来确认某个实例究竟跑的是哪个构建:

```bash
docker build --build-arg VERSION=$(git describe --tags --always) .
# 或直接 go build
go build -ldflags "-X zekumo/internal/server.Version=$(git describe --tags --always)" ./cmd/server
```

**关于优雅关闭**:收到 SIGTERM 后先停止接受新连接、等在途请求结束,
再等后台 worker 把缓冲的日志落库,最后才关连接池 —— 所以滚动更新不会丢最后一批日志。

### 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ZEKUMO_ENV` | `development` | 设为 `production` 时,不安全的配置会让服务**拒绝启动**而不是仅告警 |
| `ZEKUMO_ADDR` | `:8080` | 监听地址 |
| `DATABASE_URL` | `postgres://zekumo:zekumo@localhost:5432/zekumo` | Postgres 连接串(启动时自动跑迁移) |
| `REDIS_ADDR` | `localhost:6379` | Redis 地址 |
| `JWT_SECRET` | `dev-secret-change-me` | 令牌签名密钥,**生产必须改** |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / `admin123` | 控制台账号,**生产必须改** |
| `TOKEN_TTL` | `168h` | 玩家令牌有效期 |
| `BANNED_WORDS_FILE` | (空) | 聊天敏感词文件,一行一个词 |
| `STATS_TZ` | `Asia/Shanghai` | 统计日界时区(写入与查询共用同一时区,不会出现统计错天) |
| `LOG_RETENTION_DAYS` | `14` | 结构化日志保留天数(清理任务启动即运行、每日执行) |
| `FUNC_HTTP_ALLOW_PRIVATE` | `false` | 允许云函数与 WebHook 访问内网地址(仅本地开发用,生产必须保持 false) |
| `LOG_HTTP_ALL` | `false` | 记录每个 API 请求;默认只记 4xx/5xx 和超过 1 秒的慢请求 |
| `TRUST_PROXY` | `false` | 限流时采信 `X-Forwarded-For`。**只有在反代会覆写该头时才可开启**,否则任何调用方都能伪造来源绕过按 IP 限流 |
| `BASE_URL` | `http://localhost:8080` | 对外基础 URL(本地存储驱动用它生成上传/下载链接;改端口时同步改) |
| `STORAGE_DRIVER` | `local` | 产物存储:`local`(本地磁盘)或 `s3`(S3/MinIO/R2/OSS) |
| `LOCAL_DATA_DIR` | `data` | local 驱动的存储目录 |
| `S3_ENDPOINT` / `S3_BUCKET` / `S3_ACCESS_KEY` / `S3_SECRET_KEY` / `S3_REGION` / `S3_USE_SSL` | (空) | s3 驱动配置 |
| `MAX_ARTIFACT_SIZE` | `2147483648` | 单个产物大小上限(字节) |

## 概念

- **游戏(Game)**:租户单位。控制台创建后获得 `app_id`(可放进客户端)和
  `app_secret`(保密,留给将来服务器对服务器接口用)。所有数据按游戏隔离。
  好友数上限可在控制台「设置」中调整(默认 200)，也可调用
  `PUT /admin/api/games/{id}/friend-limit` 设置为 1–10000。
- **玩家(Player)**:属于某个游戏。登录方式可插拔,当前内置:
  - `guest`:设备号登录,首次即注册;
  - `password`:账号密码(先 `/v1/auth/register`)。
  接新渠道(微信等)只需实现 `auth.Provider` 接口并注册,其余代码不动。
- **令牌**:登录返回 JWT,之后所有请求带 `Authorization: Bearer <token>`
  (WebSocket 用 `?token=` 查询参数)。

## 玩家 HTTP API(`/v1`)

错误统一为 `{"error": {"code": "...", "message": "..."}}`。

### 认证

```
POST /v1/auth/login     {"app_id", "provider": "guest",    "device_id", "nickname"?}
POST /v1/auth/login     {"app_id", "provider": "password", "username", "password"}
POST /v1/auth/login     {"app_id", "provider": "sso", "ticket"}          一次性票据换本游戏登录
POST /v1/auth/login     {"app_id", "provider": "sso", "username", "password"}  通行证账密直登
POST /v1/player/bind    {"ticket"} 或 {"username","password"}   把当前玩家绑定到通行证(需玩家令牌)
POST /v1/auth/register  {"app_id", "username", "password", "nickname"?}
→ {"token": "...", "player": {...}}
```

### 玩家档案与存档

```
GET    /v1/player/profile                 玩家信息
PUT    /v1/player/profile                 {"nickname"?, "profile"?: 任意JSON}
GET    /v1/player/data                    存档 key 列表
GET    /v1/player/data/{key}              读一个存档槽
PUT    /v1/player/data/{key}              写存档,body 就是任意 JSON 值(≤256KB)
DELETE /v1/player/data/{key}              删存档
```

### 排行榜(Redis 排名 + Postgres 持久化,Redis 丢失自动重建)

```
POST /v1/leaderboards/{board}/score       {"score": 300, "mode": "max"|"incr"|"replace"}
GET  /v1/leaderboards/{board}?offset=&limit=&scope=friends   TopN(带昵称; scope=friends 仅好友范围)
GET  /v1/leaderboards/{board}/me          我的排名
```

### 好友与成就

```
POST /v1/friends/request       {"target_player_id"} 发起请求
POST /v1/friends/accept        {"request_id"} 接受请求
POST /v1/friends/decline      {"request_id"} 拒绝或撤回
GET  /v1/friends               好友列表(含在线状态)
GET  /v1/friends/requests      待处理请求
DELETE /v1/friends/{player_id} 删除好友
POST /v1/friends/block         {"target_player_id"} 屏蔽
GET  /v1/achievements          全部成就及当前进度
GET  /v1/achievements/unlocked 已解锁成就
```

### 剧情对话(控制台编辑,客户端只读)

```
GET /v1/dialogues                         脚本列表(key/标题/版本,可用于增量更新)
GET /v1/dialogues/{key}                   完整脚本内容(存什么取什么,结构由游戏自定)
```

### 聊天历史

```
GET /v1/chat/history?channel=world&before_id=&limit=    翻页拉历史(最新在前)
```

### 服务器公告

客户端无需登录即可轮询公告；空的 `platform` / `channel` 表示全服。

```
GET /v1/apps/{app_id}/announcements?after_id=&platform=&channel=
POST /admin/api/games/{id}/announcements
PUT  /admin/api/announcements/{aid}
DELETE /admin/api/announcements/{aid}                   下架(软删除)
```

### 虚拟货币与游戏内邮件

每个游戏最多定义 8 种货币。发放与消费都要带 `idempotency_key`:同一流水号
重复提交不会重复记账(返回 `duplicate: true`);同一流水号配不同金额返回 409。
余额不足时消费返回 402。邮件奖励在玩家领取时一次性原子发放。

```
GET  /v1/currency                       我的各货币余额
GET  /v1/currency/{cid}/ledger?limit=&offset=   我的流水明细
POST /v1/currency/{cid}/spend           {"amount","idempotency_key","note"?}
GET  /v1/mailbox?unread_only=true       收件箱(含未读数)
POST /v1/mailbox/{mid}/read             标记已读
POST /v1/mailbox/{mid}/claim            领取附件奖励(一次性)
```

### 游戏级 KV(活动开关、全局配置)

游戏级配置不属于任何玩家,读写权限是分开的:**玩家令牌只能读** `public`
或 `public_` 前缀的命名空间(适合下发活动开关、门槛值);**写入必须由服务端
签名**,因为客户端持有的 App ID 是公开的,可签名的密钥只有 `app_secret`。
云函数里的 `mc.kv` 读写的是同一份数据,不受公开命名空间限制。

```
GET    /v1/kv/{namespace}?limit=&offset=     列出 key(玩家令牌,仅公开命名空间)
GET    /v1/kv/{namespace}/{key}              读取(玩家令牌,仅公开命名空间)
PUT    /v1/kv/{namespace}/{key}?ttl=         写入(app_secret 签名,body = 任意 JSON)
DELETE /v1/kv/{namespace}/{key}              删除(app_secret 签名)
```

- 命名空间与 key 都要匹配 `[a-zA-Z0-9_.-]{1,64}`;值上限 64 KB,必须是合法 JSON。
- `ttl` 单位为秒(1–31536000),到期后读不到,后台每小时清理一次过期行。
- 读私有命名空间返回 `403 private_namespace`。

写入要带三个请求头(时间戳防重放,窗口 ±5 分钟):

```
X-Zekumo-App-Id:     zk_xxx
X-Zekumo-Timestamp:  1758880000                  unix 秒
X-Zekumo-Signature:  sha256=<hex>                HMAC-SHA256(app_secret, "{ts}.{method}.{path}.{body}")
```

签名串是**时间戳、方法、路径、body 用 `.` 连接**(DELETE 的 body 为空):

```bash
TS=$(date +%s); BODY='{"double_drop":true}'; PATH_='/v1/kv/public/event'
SIG=$(printf '%s.PUT.%s.%s' "$TS" "$PATH_" "$BODY" | openssl dgst -sha256 -hmac "$APP_SECRET" -hex | awk '{print $2}')
curl -X PUT "$BASE$PATH_" -H "X-Zekumo-App-Id: $APP_ID" -H "X-Zekumo-Timestamp: $TS" \
     -H "X-Zekumo-Signature: sha256=$SIG" -d "$BODY"
```

### 检查更新(无需令牌 —— 更新器在登录之前运行)

```
GET /v1/apps/{app_id}/updates/check?version=1.0.0&platform=windows&arch=amd64&channel=stable&device_id=xxx
GET /v1/apps/{app_id}/releases              已发布版本历史
```

有更新时返回:

```json
{
  "update_available": true,
  "mandatory": false,
  "release":  { "version": "1.1.0", "changelog": "...", "published_at": "..." },
  "artifact": { "url": "预签名下载地址", "filename": "game-1.1.0.zip", "size": 123, "sha256": "..." }
}
```

- `platform`:`windows|macos|linux|android|ios|web|any`;`arch`:`amd64|arm64|any`。
  产物按精确匹配优先、`any` 兜底。
- `device_id` 用于灰度:版本可按百分比放量,同一设备命中结果稳定。
- `mandatory: true` 表示强制更新(版本标记了强制,或客户端低于 `min_supported_version`)。
- 下载后**务必校验 sha256** 再安装。
- 下架(revoke)某版本后,检查接口自动回退到上一个已发布版本。

## 通行证 SSO(`/sso`)

平台级玩家账号:注册一次,登录平台上所有游戏;各游戏内玩家通过 `account_id`
关联到通行证,同一通行证在同一游戏只有一个玩家。

```
POST /sso/api/register        {"username","password","nickname"?} → {"token","account"}
POST /sso/api/login           {"username","password"}             → {"token","account"}
GET  /sso/api/me              (通行证令牌) 账号信息 + 已关联的各游戏玩家
POST /sso/api/tickets         (通行证令牌) {"app_id"} → {"ticket","expires_in"}
GET  /sso/api/authorize/info  ?app_id=&redirect_uri=  授权页信息 + 回跳白名单校验
GET  /sso/authorize           托管登录页(浏览器打开)
```

三种接入方式:

1. **票据换登录(推荐,启动器场景)**:启动器持有通行证令牌,启动某游戏前调
   `/sso/api/tickets` 拿一次性票据(5 分钟有效、只对该游戏生效、用一次即废),
   传给游戏进程;游戏用 `provider=sso, ticket=...` 调 `/v1/auth/login` 换本游戏玩家令牌。
   配合更新分发接口,天然适合"启动器 + 多小游戏"的形态。
2. **托管授权页(H5 场景)**:游戏把浏览器跳到
   `/sso/authorize?app_id=...&redirect_uri=...&state=...`,玩家在平台页面登录后,
   带着 `?ticket=...&state=...` 回跳到 `redirect_uri`。回跳地址必须命中该游戏在
   控制台配置的白名单,否则页面拒绝登录(防钓鱼)。匹配规则:**scheme 与 host 必须完全相同**,
   只有路径按段前缀匹配 —— 白名单填 `https://good.com` 不会放行
   `https://good.com.evil.com` 或 `https://good.com@evil.com`。
3. **游戏内直登**:游戏内直接收集通行证账密,`provider=sso, username, password`。
   实现最简单,但游戏能碰到密码,信任要求最高。

**存量玩家绑定**:已有的游客/账密玩家调 `POST /v1/player/bind`(带票据或通行证账密)
绑定到通行证;之后用该通行证 SSO 登录会直接回到这个玩家,换设备存档不丢。
玩家已绑定、或通行证在该游戏已有别的玩家时返回 409。

## OAuth2 授权(`/oauth`)

让第三方应用"用 Zekumo 通行证登录"。控制台创建 OAuth 应用得到 `client_id`
(机密客户端另有 `client_secret`;公开客户端强制 PKCE)。标准授权码流程:

```
GET  /oauth/authorize     ?client_id=&redirect_uri=&response_type=code&scope=profile
                          &state=&code_challenge=&code_challenge_method=S256
                          托管同意页:登录通行证 → 同意 → 携 code 回跳(白名单校验)
POST /oauth/token         grant_type=authorization_code|refresh_token(form 或 JSON)
GET  /oauth/userinfo      Bearer 访问令牌 → {sub, username, nickname, scope}
GET    /sso/api/authorizations                (通行证令牌)已授权应用列表
DELETE /sso/api/authorizations/{client_id}    撤销授权(该应用全部 refresh token 失效)
```

安全设计:访问令牌 1 小时,但 `userinfo` 每次都复查授权是否仍然存在,**撤销即时生效**;
refresh token 30 天、**每次使用即轮换**,旧 token 被重放时判定为泄露并吊销整条链;
PKCE 只接受 `S256`(`plain` 不提供任何保护,已拒绝);公开客户端必须带 PKCE;
授权码一次性、5 分钟有效;`redirect_uri` 与 SSO 用同一套 scheme+host 精确匹配规则;
scope 只接受白名单内的值。

## 云函数(`/v1/functions`)

控制台里直接写服务端 JS(goja 沙箱,10 秒硬超时),给游戏做发奖、防作弊校验、
定时结算等服务端逻辑,不用自己部署服务:

```
POST/GET /v1/functions/{name}                 玩家令牌调用(request.player 是调用者)
POST/GET /v1/apps/{app_id}/functions/{name}   免登录调用(仅"公开"函数)
```

脚本全局对象:`request` = {method, body, query, player};`mc.kv`(游戏级 KV)、
`mc.playerdata.get/set(playerID, key)`、`mc.players.get(playerID)`、
`mc.leaderboard.submit/top`、`mc.http.fetch(url, {method, headers, body}?)`;
`console.log` 进日志;**最后一个表达式的值**作为响应返回(执行上限 10 秒)。

出站 HTTP 受控:目标域名必须在控制台"云函数"页配置的白名单里(支持 `*.example.com`),
重定向逐跳复检;内网/环回/CGNAT/云元数据地址始终被拦截。IP 校验发生在**建立连接时**
对实际解析出的地址进行,而不是先查 DNS 再连接 —— 后者存在 DNS rebinding 窗口。
每次调用最多 5 个请求、单请求 5 秒超时、响应截断 1MB。

资源上限:全平台同时最多 4 个函数实例(超出返回 503),整个调用共享一个 10 秒 deadline
(包括数据库和出站请求,不只是 JS 执行),进程堆超过 512MB 时正在运行的脚本会被中断。
goja 当前版本没有单 VM 内存上限,因此单次巨量分配(如 `'x'.repeat(5e8)`)仍可能突破上限一次,
限制并发是防止其累积成 OOM 的主要手段。
定时触发:函数可设 `cron_secs`(≥10 秒),平台按间隔自动执行(request.method = "CRON")。

示例(每日签到发奖):

```js
const uid = request.player.id;
const today = new Date().toISOString().slice(0, 10);
const last = mc.playerdata.get(uid, 'last_signin');
if (last === today) ({ ok: false, reason: 'already' })
else {
  mc.playerdata.set(uid, 'last_signin', today);
  const gold = (mc.playerdata.get(uid, 'gold') || 0) + 100;
  mc.playerdata.set(uid, 'gold', gold);
  ({ ok: true, gold: gold })
}
```

## WebHook 事件推送

控制台按游戏配置接收地址,平台事件实时 POST 过去(失败重试 3 次,间隔 1s/5s):

- 事件:`player.registered`、`player.login`、`player.banned`、`player.unbanned`、`leaderboard.score`、`achievement.unlocked`、`release.published`
  (可订阅子集,`*` 表示全部)
- 请求体:`{event, game_id, timestamp, data}`;头部 `X-Zekumo-Event` 和
  `X-Zekumo-Signature: sha256=<HMAC-SHA256(secret, body)>`,**收到后必须验签**
- 接收地址与云函数出站共用同一套地址校验,内网地址不可达(否则 WebHook 会变成内网探测器)
- 控制台可发测试事件、查看最近 50 条投递记录(状态码/尝试次数)

## 访问统计

自动采集,无需接入:登录即计入"登录次数/新增",任何带玩家令牌的请求即计入当日活跃
(Redis 去重)。每 10 分钟聚合到 Postgres 日表;控制台"统计"页展示 30 天 DAU 柱状图、
登录/新增明细和当前在线人数。日界时区由 `STATS_TZ` 统一控制。

## 限流与防爆破

两层防护,分工明确:

**按 IP 限流**(粗粒度洪水控制,超限返回 429 + `Retry-After`):

| 端点 | 限制 |
|---|---|
| `/v1/auth/login`、`/v1/auth/register`、`/sso/api/login`、`/sso/api/register` | 120 次/分钟 |
| `/oauth/token` | 120 次/分钟 |
| `/v1/apps/{app_id}/functions/{name}`(免登录云函数) | 120 次/分钟 |
| `/admin/api/login` | 20 次/分钟 |

按玩家限流(令牌里的玩家 ID,一个吵闹的客户端挤不掉其他人):
`/v1/logs` 60 次/分钟,`/v1/functions/{name}` 120 次/分钟。

> 按 IP 的额度**故意放宽**:运营商 CGNAT 和校园/办公网会让大量正常玩家共用一个出口 IP,
> 卡得太紧等于封禁真实用户。它只用来挡住最粗暴的洪水。

**按账号锁定**(真正的防爆破):同一账号连续失败 10 次即锁定 15 分钟,
每次新的失败都会续期,所以慢速撞库也熬不过去。锁定期内即使密码正确也拒绝。
这一层不受攻击者更换 IP 影响 —— 分布式撞库正是按 IP 限流拦不住的场景。
登录成功会立即清零计数。

限流依赖 Redis;Redis 故障时**放行**而不是拒绝(限流器挂掉不应该等于平台挂掉),
但按账号锁定同样会失效,这一点在依赖不可用时需要知悉。

## 结构化日志

四路来源统一入库(异步批量写 Postgres,不阻塞请求路径),控制台"日志"页可按
级别/来源/关键词筛选、翻页:

- `http`:API 访问日志(方法/路径/状态码/耗时;带玩家令牌的请求自动归属到游戏,
  4xx 记 warn、5xx 记 error)
- `funcs`:云函数执行错误 + 定时任务每次运行的输出(否则 CRON 的 console.log 无处可看)
- `hooks`:WebHook 投递失败
- `client`:游戏客户端主动上报 —— `POST /v1/logs`(玩家令牌)
  `{"level","event"?,"message","fields"?}`,自动附上 player_id,适合收集客户端崩溃/异常

保留期由 `LOG_RETENTION_DAYS` 控制,清理任务在启动时立即执行一次、之后每天一次
(同时清理 WebHook 投递记录)。access 日志默认只记录失败和慢请求,`LOG_HTTP_ALL=true` 可记录全部。
查询接口:`GET /admin/api/logs?game_id=&level=&source=&q=&before_id=&limit=`。

## WebSocket 实时网关(`/v1/ws?token=...`)

所有帧都是 JSON:`{"type": "...", "data": {...}}`。连接建立后服务器先发 `welcome`。

| 客户端发送 | 说明 | 服务器回应 / 广播 |
|---|---|---|
| `room.create` `{name?, max_players?, meta?}` | 建房(默认 20 人上限) | `room.created`(含房间快照) |
| `room.join` `{room_id}` | 加入 | 自己收 `room.joined`(含成员和各自最新状态);其他人收 `room.member_joined` |
| `room.leave` | 离开 | `room.left`;其他人收 `room.member_left`(含新房主) |
| `room.list` | 列出本游戏房间 | `room.list` |
| `room.state` `{任意状态}` | 上报自己的状态(位置/血量等) | 房间其他人收 `room.state {player_id, state}` |
| `room.msg` `{任意数据}` | 自定义消息中继 | 房间其他人收 `room.msg {player_id, data}` |
| `chat.sub` / `chat.unsub` `{channel}` | 订阅/退订频道(≤32 个) | `chat.subbed` / `chat.unsubbed` |
| `chat.send` `{channel, content}` | 发言(过敏感词,异步入库) | 订阅者收 `chat.msg`(完整消息) |
| `ping` | 心跳 | `pong` |

出错时收 `{"type":"error","data":{"code","message"}}`。
同一玩家重复连接会顶掉旧连接。房间在最后一人离开后自动销毁。

> 定位说明:服务器做的是"权威中继"——状态和消息由服务端分发,但不理解游戏规则。
> 房间数据在单实例内存中;水平扩展需要把房间路由到固定实例(或引入 Redis pub/sub),
> 这是后续版本的事。小游戏体量单实例足够。

### 客户端示例(JS,可直接用于 H5 / 小游戏)

```js
const BASE = "https://你的域名";

// 登录
const { token } = await fetch(`${BASE}/v1/auth/login`, {
  method: "POST",
  body: JSON.stringify({ app_id: "zk_xxx", provider: "guest", device_id: myDeviceId }),
}).then(r => r.json());

// 存档
await fetch(`${BASE}/v1/player/data/save1`, {
  method: "PUT",
  headers: { Authorization: `Bearer ${token}` },
  body: JSON.stringify({ level: 3, gold: 120 }),
});

// 实时:进房 + 同步位置 + 世界聊天
const ws = new WebSocket(`${BASE.replace("http", "ws")}/v1/ws?token=${token}`);
const send = (type, data) => ws.send(JSON.stringify({ type, data }));
ws.onopen = () => {
  send("room.create", { name: "我的房间" });
  send("chat.sub", { channel: "world" });
};
ws.onmessage = e => {
  const { type, data } = JSON.parse(e.data);
  if (type === "room.state") updatePlayer(data.player_id, data.state);
  if (type === "chat.msg") showChat(data.sender_name, data.content);
};
setInterval(() => send("room.state", { x: me.x, y: me.y }), 100);
```

## 管理 API(`/admin/api`,控制台也走这套)

```
POST   /admin/api/login                          {"username","password"} → {"token"}
GET    /admin/api/games                          游戏列表(含在线人数)
POST   /admin/api/games                          {"name"} → 含 app_id/app_secret
DELETE /admin/api/games/{id}                     删游戏(级联删除所有数据)
GET    /admin/api/games/{id}/players?search=     玩家列表
GET    /admin/api/players/{pid}/data             查看玩家全部存档
GET    /admin/api/accounts?search=               通行证账号列表
PUT    /admin/api/games/{id}/sso                 {"redirect_urls": "每行一个 URL 前缀"} 回跳白名单
GET/POST /admin/api/oauth/clients                OAuth 应用管理
PUT/DELETE /admin/api/oauth/clients/{client_id}
GET/PUT/DELETE /admin/api/games/{id}/functions/{name}   云函数 CRUD
POST   /admin/api/games/{id}/functions/{name}/test      测试运行
GET/POST /admin/api/games/{id}/webhooks          WebHook 配置
PUT/DELETE /admin/api/webhooks/{wid}
GET    /admin/api/webhooks/{wid}/deliveries      投递记录
POST   /admin/api/webhooks/{wid}/test            发测试事件
GET    /admin/api/games/{id}/stats?days=30       统计数据
GET    /admin/api/logs?game_id=&level=&source=&q= 结构化日志查询
PUT    /admin/api/games/{id}/http-allowlist      云函数出站域名白名单
GET/PUT/DELETE /admin/api/games/{id}/dialogues/{key}   剧情脚本 CRUD
GET    /admin/api/games/{id}/dialogues           脚本列表
```

### 运营工具(货币 / 邮件 / 留存 / 封禁)

封禁立即生效:黑名单写 Redis(TTL = 封禁时长),已签发的令牌随即失效,
封禁期内登录返回 `403 player_banned`。封禁与解禁会触发 WebHook 事件
`player.banned` / `player.unbanned`。留存按注册日分群,每小时计算一次;
`d1`/`d7`/`d30` 只在分群满 2/8/31 天、结果已确定后才填值,未确定时为 `null`。

```
GET/POST /admin/api/games/{id}/currencies        货币种类 CRUD(每游戏最多 8 种)
PUT/DELETE /admin/api/currencies/{cid}
POST   /admin/api/games/{id}/currency/{cid}/grant  {"player_id","amount","idempotency_key","note"?}
GET    /admin/api/players/{pid}/currency         任意玩家的余额
GET    /admin/api/players/{pid}/currency/{cid}/ledger   任意玩家的流水
POST   /admin/api/games/{id}/mail                {"title","body","recipients","rewards"?,"expires_in"?}
                                                 recipients 为 "all" 或最多 1000 个 player_id
GET    /admin/api/games/{id}/mail                邮件列表 + 已读/领取统计
GET    /admin/api/games/{id}/stats/retention?days=30    D1/D7/D30 留存时序
GET    /admin/api/games/{id}/stats/funnel?days=30       注册漏斗
POST   /admin/api/players/{pid}/ban               {"reason","duration_secs"?} 0 或省略=永久
POST   /admin/api/players/{pid}/unban             解封
GET    /admin/api/games/{id}/bans                 封禁记录
```

控制台对应「虚拟货币」「邮件」「封禁」三个页面,游戏「概览」页展示留存与漏斗。

### 数据导出(GDPR 数据可携带)

导出是异步任务:提交后返回 `job_id`,轮询状态,完成后 `download_url` 字段给出
预签名下载地址(有效期 24 小时,过期重新查询即可拿到新的)。导出内容含玩家
档案、存档、货币流水与成就。任务超过 10 分钟未完成会被标记失败;服务重启时
残留的 running 任务在启动时统一置为失败,不会永远停在「生成中」。

```
POST /admin/api/games/{id}/exports      {"format":"json"|"csv","scope":"all"|"player","player_id"?}
GET  /admin/api/exports/{job_id}        查询进度 + 下载地址
GET  /admin/api/games/{id}/exports?limit=&offset=   导出历史
```

- `format`:`json` 为单文件嵌套结构;`csv` 按表拆成多个文件打包成 zip。
- `scope=player` 时 `player_id` 必填,且必须属于该游戏(否则 404)。
- 状态流转:`pending → running → done / failed`,失败时 `error` 字段带原因。
- 响应包裹为 `{"job": {...}, "download_url": "..."}`,仅 `done` 状态才有下载地址。

控制台「数据导出」页覆盖同样的流程:选范围与格式提交 → 任务列表自动刷新
状态 → 完成后点下载。

### 版本发布(更新分发)

版本生命周期:`draft →(publish)→ published →(revoke)→ revoked`,已发布也可直接下架。
以下接口中版本默认在 `stable` 渠道,其他渠道加 `?channel=beta`:

```
GET    /admin/api/games/{id}/releases                    全部版本
POST   /admin/api/games/{id}/releases                    {"version","channel"?,"changelog"?,"mandatory"?,"min_supported_version"?}
DELETE /admin/api/games/{id}/releases/{version}          删除草稿
POST   /admin/api/games/{id}/releases/{version}/artifacts    {"platform","arch"?,"filename","size","sha256"}
                                                         → {"artifact","upload_url"} 拿预签名地址直传文件
POST   .../artifacts/{aid}/complete                      直传完成后确认(平台校验对象与大小)
POST   /admin/api/games/{id}/releases/{version}/publish  发布(需至少一个就绪产物)
POST   /admin/api/games/{id}/releases/{version}/rollout  {"percent": 0-100} 灰度放量
POST   /admin/api/games/{id}/releases/{version}/revoke   紧急下架(客户端回退旧版)
```

控制台「版本更新」页覆盖同样的流程:创建版本 → 选文件上传(浏览器本地算
SHA-256 后直传存储)→ 发布 / 灰度 / 下架。

## 数据库迁移

`internal/store/migrations/` 下按编号排列的 SQL 文件,启动时按文件名顺序应用,
每个文件在独立事务里执行并记入 `schema_migrations` 表,已应用的不会重跑。
加新表/改字段就新建一个 `00NN_描述.sql`,不要改动已发布的迁移文件
(别人的库已经跑过了,改它不会重新生效)。

## 代码结构

```
cmd/server          入口
cmd/smoketest       端到端冒烟测试(go run ./cmd/smoketest)
internal/config     环境变量配置
internal/store      Postgres/Redis 连接 + 版本化迁移(migrations/*.sql,启动时按序应用)
internal/repo       数据模型与所有 SQL
internal/auth       JWT、登录 Provider(可插拔)、鉴权中间件
internal/player     档案 + KV 存档 API
internal/leaderboard 排行榜(Redis zset + PG 持久化)
internal/dialogue   剧情对话只读 API
internal/chat       聊天服务(敏感词过滤、异步入库、历史查询)
internal/realtime   WebSocket 网关(房间、状态同步、聊天分发)
internal/updates    版本发布与更新分发(semver 选版、灰度、产物匹配)
internal/storage    产物存储抽象(local 签名直传 / S3 预签名)
internal/sso        通行证账号、一次性票据、sso Provider、绑定、授权页接口
internal/oauth      OAuth2 提供方(授权码、PKCE、refresh 轮换、userinfo)
internal/funcs      云函数(goja 运行时、HTTP/定时触发)
internal/hooks      WebHook 事件总线(HMAC 签名、重试投递)
internal/currency   虚拟货币(幂等发放/消费、流水账本)
internal/mailbox    游戏内邮件(广播/定向投递、附件领取)
internal/bans       玩家封禁(Redis 黑名单,令牌即时失效)
internal/kv         游戏级 KV REST(公开命名空间读、签名写、TTL 清扫)
internal/exports    数据导出(异步任务、JSON/CSV zip、预签名下载)
internal/stats      访问统计(Redis 采集、PG 日聚合、留存与漏斗)
internal/logs       结构化日志(批量入库、保留清理、查询)
scripts/backup.sh   Postgres 备份(校验产物、按天轮转)
internal/ratelimit  限流(Redis 计数)与按账号锁定
internal/netsafe    出站请求的地址校验(SSRF 防护,连接时校验真实 IP)
internal/safego     带 recover 的后台 goroutine
internal/admin      管理 API
internal/server     路由拼装、CORS、日志
web/admin           内嵌 Web 控制台(Material Design 3,浅色/深色)
web/sso             托管 SSO 授权登录页
web/oauth           托管 OAuth2 同意授权页
```
