# Zekumo 控制台(macOS)

`/admin` 的原生 SwiftUI 版本,直接调用 `/admin/api`,和网页控制台共用账号、工作区与角色权限。

| 页面 | 能做什么 |
| --- | --- |
| 仪表盘 | 在线/活跃/新增/留存、7/30 天趋势、各游戏今日数据、服务状态(所有者)、最近错误 |
| 游戏 | 列表、App ID / Secret 复制、新建(管理员)、删除(所有者) |
| 玩家 | 搜索、查看存档 JSON、封禁/解封 |
| 版本更新 | 新建草稿、上传产物(本地计算 SHA-256)、发布、灰度、下架、删除草稿 |
| 公告 | 新建/编辑(带预览)、级别、平台/渠道定向、到期时间、下架 |
| 封禁 | 生效中/历史封禁、按玩家 ID 封禁、解封 |

按角色禁用无权限的操作;会话令牌存放在钥匙串里,按服务器区分。

## 运行

需要 macOS 14+ 与 Xcode 15+(Swift 5.10)。

```sh
cd macos
swift run ZekumoConsole          # 开发运行
./scripts/build-app.sh           # 打包 build/ZekumoConsole.app(通用二进制,ad-hoc 签名)
open Package.swift               # 用 Xcode 打开
```

登录页填服务器地址(如 `http://localhost:8090`)和控制台账号。

## 测试

```sh
swift test                                                   # 解码、错误映射、请求构造
ZEKUMO_LIVE_URL=http://localhost:8090 swift test             # 另外对真实服务端跑一遍完整流程
```

在线测试会新建一个临时游戏,走一遍版本发布与公告流程,结束后删除。账号默认 `admin` / `admin123`,
可用 `ZEKUMO_LIVE_USER` / `ZEKUMO_LIVE_PASSWORD` 覆盖。
