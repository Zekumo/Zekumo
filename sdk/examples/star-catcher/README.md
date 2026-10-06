# 云间拾星 · Star Catcher

使用仓库内 Zekumo JS SDK 的可玩示例。纯 HTML、CSS 和 Canvas，无游戏引擎或额外前端依赖。

## 启动

在仓库根目录运行：

```bash
cd sdk
npm install
npm run demo
```

打开 <http://127.0.0.1:5178/>。已安装 SDK 开发依赖时可跳过 `npm install`。
需要 Node.js 18+。可通过 `PORT=5179 npm run demo` 更换端口。

默认连接 `https://api.zekumo.com`，App ID 为 `zk_9c49b160c312`。
对应控制台游戏 ID：`adc7debd-e22a-4910-ad0e-2776e4ec8008`。
可在页面「Zekumo 连接设置」修改地址、App ID 和昵称；客户端不需要 app_secret。
访客打开页面会自动创建或恢复一个真实游客，完成游戏后会写入真实存档和榜单。

## 玩法

- 每局 45 秒，3 点能量；用左右方向键、A / D、鼠标或触屏拖动云朵。
- 星星基础分 10 分，每连续接住 5 颗增加 5 分奖励，每颗最多 30 分。
- 漏接会清空连击；撞上紫色陨石扣一点能量，并获得 1.3 秒保护。
- 空格或右上角按钮暂停，切换标签页或窗口自动暂停。

## SDK 调用

| 功能 | SDK 方法 |
| --- | --- |
| 恢复游客身份 | `auth.loginAsGuest({ deviceId, nickname })` |
| 修改昵称 | `player.updateProfile({ nickname })` |
| 读写手记 | `playerData.get/set('star-catcher-v1', ...)` |
| 上传最高分 | `leaderboards.submit('star-catcher-v1', best, 'max')` |
| 读取前五名 | `leaderboards.top('star-catcher-v1', { limit: 5 })` |

`cloud.js` 封装 SDK 调用，`game.js` 负责玩法和 UI。API 请求有 10 秒超时。
设备号及待同步成绩保存在 localStorage，令牌只保留在内存。
断网时可以继续游玩；重新连接后重试同步。初次读取云存档失败时不会覆盖云端数据。
本地存档按 API、App ID、设备号隔离。清除浏览器数据后会产生新的游客，示例不提供跨设备身份恢复。

这是客户端计分的 SDK 演示，正式竞技玩法需要服务端校验分数。
最高分提交采用 `max` 可安全重试；完成局数是简单快照，不保证多标签页同时游玩的精确合并。

## 验证

构建 SDK 后，在仓库根目录运行真实 API 冒烟检查：

```bash
node sdk/examples/star-catcher/smoke.mjs
```

脚本检查游客身份恢复、存档回读、排行榜 `max` 模式及个人排名。
测试存档结束后删除，`SDK 联调` 游客和独立的 `star-catcher-sdk-smoke` 测试榜会保留。
可用 `ZEKUMO_URL`、`ZEKUMO_APP_ID` 环境变量覆盖测试连接配置。

本次已通过真实 API 联调，并用 Chromium 验证整局结算、刷新恢复、暂停、指针输入、390px 手机布局，以及断网游玩后重新连接同步。
