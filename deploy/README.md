# 面板部署 · MiniCloud

目标链路:

```
玩家 / 浏览器 ──HTTPS──▶ Cloudflare ──HTTP──▶ 面板 Nginx ──▶ 127.0.0.1:8080 (容器)
                                                              ├─ postgres (仅内网)
                                                              └─ redis    (仅内网)
```

本目录已经把密钥生成好了,`deploy/.env` 可以直接用,不需要你再填什么。

---

## 一、上传

在面板的文件管理里建 `/opt/minicloud`,传成这个结构:

```
/opt/minicloud/
├── docker-compose.yml      ← 本目录的 docker-compose.panel.yml,改名
├── bootstrap.sh            ← 本目录的同名文件(首次部署生成 .env)
├── lock-origin.sh          ← 本目录的同名文件
├── deploy.sh               ← 本目录的自动部署脚本
└── src/                    ← 项目源码(见下方两种方式)
```

> **不要上传本目录的 `.env`**。密钥应该在服务器上生成、只存在于服务器。
> 本目录的 `.env` 只是一份示例,首次部署用 `bootstrap.sh` 在服务器上现场生成。

`src/` 放源码是因为镜像要在服务器上构建 —— 你没有私有镜像仓库。
不用传的:`.git/`、`backups/`、`*.exe`、`*.log`。

传完设权限(面板的终端里执行):

```bash
cd /opt/minicloud && chmod +x bootstrap.sh lock-origin.sh deploy.sh
```

### 首次部署:生成配置(只做一次)

```bash
cd /opt/minicloud && ./bootstrap.sh
```

它会现场随机生成 `JWT_SECRET`、`POSTGRES_PASSWORD`、`ADMIN_PASSWORD`,写入 `.env`(权限 600)。
交互提示输入 `BASE_URL`(对外域名);自动化可 `BASE_URL=https://panel.mn1.top ./bootstrap.sh` 直接传。
幂等:重复执行只补缺失项,已生成的值不会变。结尾会打印一次控制台密码,记好。

### src/ 的两种维护方式

**方式 A(压缩包,推荐):** 你没有 git,就在本机跑打包脚本生成源码包,
上传后部署脚本会自动解压进 `src/`:

```powershell
# 本机(项目根目录):
.\deploy\package.ps1            # 生成 deploy\dist\minicloud-src-<时间戳>.tar.gz
```

把 `minicloud-src-*.tar.gz` 上传到 `/opt/minicloud/`(和 deploy.sh 同目录),
执行 `./deploy.sh` 时它会自动解压进 `src/` 再构建。
以后每次改完代码,重新打包上传、再跑 deploy.sh 就是一次新部署。

**方式 B(直接传文件):** 第一次部署也可以手动把源码传到 `src/`,
和上面的目录结构一致即可(包内就是项目根目录的文件,Dockerfile、cmd/、internal/、web/ 都在)。

## 二、锁定源站(**在开放域名之前做**)

```bash
cd /opt/minicloud && ./lock-origin.sh
```

只放行 22 端口和 Cloudflare 官方 IP 段的 80/443,其余入站一律拒绝。

**为什么这步不能跳过**:源站 IP 只要泄露,别人就能绕过 Cloudflare 直连。
而配置里开了 `TRUST_PROXY=true`(应用信任 `X-Forwarded-For` 来判断来源 IP),
源站不锁的话,任何人都能伪造这个头,把按 IP 限流整个绕过去。
两者必须成对存在。

> 到这里首次部署的顺序是:`./bootstrap.sh`(生成 .env)→ `./lock-origin.sh`(锁源站)
> → `./deploy.sh`(构建并启动)。deploy.sh 找不到必填凭据时会提示先跑 bootstrap。

## 三、启动

面板 →「容器」→「编排 / Compose」→ 新建,路径选 `/opt/minicloud`,
或者直接在终端里:

```bash
cd /opt/minicloud && docker compose up -d --build
```

首次构建要几分钟(拉 Go 镜像 + 编译)。看进度:

```bash
docker compose logs -f server
```

### 以后每次更新代码(自动部署)

```bash
cd /opt/minicloud
# 1. 本机打包:.\deploy\package.ps1
# 2. 把新的 minicloud-src-<版本>.tar.gz 上传到 /opt/minicloud/
./deploy.sh
```

`deploy.sh` 自动完成:部署前备份(可选)→ 检测到新源码包就解压进 `src/`
→ 构建新版本镜像 → 平滑重启 → 健康检查(`/readyz` 确认 Postgres/Redis 都通)
→ 不健康自动回滚到上一个版本 → 清理旧镜像。
版本号默认取源码包名里的版本,也可 `VERSION=1.1.0 ./deploy.sh` 手动指定。
日志在 `/var/log/minicloud-deploy.log`。

看到这一行就是起来了:

```
minicloud 1.0.0 listening on [::]:8080 (env: production, storage: local, console at /admin/)
```

**如果它没起来而是退出了**,大概率是配置校验拦下了 —— 日志会明确列出哪几项不合格,
按提示改 `.env` 再 `docker compose up -d`。这是故意设计的:生产模式下宁可不启动,
也不要带着默认密钥裸奔。

本机自检:

```bash
curl -s localhost:8080/readyz
# {"checks":{"postgres":"ok","redis":"ok"},"ready":true,"version":"1.0.0"}
```

## 四、面板反代 + 域名

新建站点 `panel.mn1.top`,反代到 `http://127.0.0.1:8080`,
然后把 `nginx-panel.conf` 里的内容补进站点的「自定义配置」。

**四处不能省**,面板默认的反代模板通常都不带,漏了会出真故障:

| 配置 | 漏了会怎样 |
|---|---|
| `/v1/ws` 的 WebSocket 升级 | 实时房间和聊天**完全连不上** |
| `client_max_body_size 2048m` | 上传游戏产物直接 413 |
| `/storage/` 的长超时 | 大文件传一半被掐断 |
| `X-Forwarded-For $http_cf_connecting_ip` | 拿不到真实玩家 IP,所有人算同一来源,限流失效 |

## 五、Cloudflare

- DNS:`panel.mn1.top` → `103.79.185.53`,**开小黄云**(代理)
- SSL/TLS 模式:见下方说明
- 建议开:Always Use HTTPS、Brotli
- **不要开**「Rocket Loader」或任何 JS 优化 —— 控制台是多文件按序加载的原生 JS,
  被改写执行顺序会白屏

### 关于 SSL 模式

你说暂时不需要证书,那就是 **Flexible** 模式:浏览器到 Cloudflare 是 HTTPS,
但 **Cloudflare 到你源站是明文 HTTP**。这一段里管理员密码、玩家令牌都是裸的。
经过的是公网,不是你的内网。

要消掉这个,在源站加一张自签证书,Cloudflare 切到 **Full** 模式即可
(Full 不校验证书链,自签就够):

```bash
openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /etc/ssl/private/minicloud.key \
  -out /etc/ssl/certs/minicloud.crt \
  -subj "/CN=panel.mn1.top"
```

然后在面板站点里启用 HTTPS、指向这两个文件。**建议做**,成本只有这一条命令。

## 六、验证

```bash
curl -s https://panel.mn1.top/readyz
```

浏览器打开 `https://panel.mn1.top/admin/`,用户名 `admin`,
密码在 `.env` 里(`grep ADMIN_PASSWORD /opt/minicloud/.env`)。

登录后确认两件事:

- 仪表盘顶部**没有**红色的「部署尚未加固」横幅 —— 有就说明密钥没生效
- 「平台状态」卡片里 Postgres / Redis 都正常,版本号是 `1.0.0`

完整功能验证(会创建并删除一个临时游戏):

```bash
cd /opt/minicloud/src
MINICLOUD_URL=https://panel.mn1.top MINICLOUD_WS=wss://panel.mn1.top go run ./cmd/smoketest
```

服务器上没装 Go 的话,在你本机跑同样的命令即可 —— 它是纯客户端。
预期 135 步全过;**只有出站 HTTP 那几步会失败**,因为生产环境按设计禁止云函数访问内网,
这是正确行为。

## 七、备份

```bash
cp /opt/minicloud/src/scripts/backup.sh /opt/minicloud/
chmod +x /opt/minicloud/backup.sh
crontab -e
```

加一行(3:17 而不是整点,避开所有人都在跑备份的时刻):

```
17 3 * * * cd /opt/minicloud && COMPOSE_FILE=docker-compose.yml ./backup.sh >> /var/log/mc-backup.log 2>&1
```

**装完当天就演练一次恢复。** 没验证过的备份等于没有备份:

```bash
gunzip -c backups/minicloud-*.sql.gz | \
  docker compose exec -T postgres psql -U minicloud -d minicloud
```

## 八、日常操作

```bash
cd /opt/minicloud

docker compose logs -f server        # 看日志
docker compose restart server        # 重启(优雅关闭,不丢缓冲的日志)
docker compose up -d --build         # 更新源码后重新构建
docker compose ps                    # 状态
docker compose down                  # 停止(数据在具名卷里,不会丢)
```

## 还没解决的两件事

**1. 控制台和玩家 API 同端口。** `/admin/` 目前只靠一个管理员口令保护。
建议在面板的站点配置里给 `/admin/` 加 IP 白名单,或者用 Cloudflare Access
套一层。现在任何人都能访问登录页并尝试爆破 —— 虽然有每分钟 20 次的限流,
但少一层暴露面总是更好。

**2. 单实例。** 实时房间存在进程内存里,现在这套配置只能跑一个应用实例。
要扩容需要先做房间路由,并且把 `STORAGE_DRIVER` 换成 `s3`
(`local` 驱动的产物只在这台机的磁盘上)。
