# 轻量 MMO 房间

参考 [ShangCloud 官方文档](https://doc.yearnstudio.cn/) 的实时房间体验，在 Zekumo 已有 WebSocket 房间功能上补齐房主治理、大厅和运营可见性。这里的 MMO 指轻量多人房间，不代表完整 MMORPG，也不声称与 ShangCloud 协议兼容。

## 接入

复用玩家 JWT 和 `/v1/ws`；所有房间操作使用令牌中的游戏身份，客户端不能通过请求字段切换游戏。控制台监控使用工作区成员权限，最低 viewer，并校验游戏归属。

- `room.create` / `room.join` / `room.leave`：建立、加入、离开房间
- `room.update`：房主部分更新 `name`、`max_players`、`meta`、`locked`；广播 `room.updated` 完整快照
- `room.kick`：房主传 `player_id` 移除其他成员；被移除者收到 `room.kicked`，房主收到 `room.kick_ok`
- `room.transfer`：房主传当前成员 `player_id`；广播 `room.owner_changed`，原房主收到 `room.transferred`
- `room.get`：当前成员读取完整快照，回复 `room.info`
- `room.list`：`offset` 和 `limit` 分页，返回 `{rooms,total,offset,limit}`；摘要包含公开房间 metadata，不含成员和玩家状态
- `room.state` / `room.msg`：原有状态同步和房间广播

`GET /admin/api/games/{id}/rooms?offset=0&limit=20` 返回房间摘要、总数和在线连接数；管理监控不包含任意 metadata 或玩家状态。控制台有分页与手动刷新，不是持久化统计。

SDK 示例及精确类型参见 [TypeScript SDK](../sdk/README.md) 和 [Go SDK](../sdk-go/README.md)。TypeScript SDK 已支持请求关联 ID，独立读取可并发执行；有先后依赖的创建、加入和修改操作仍应依次 await。SDK 与服务端请一起升级，旧服务端不会回传 ID。

## 资源保护

- 房间默认 20 人，上限 200 人；每游戏最多 128 个房间
- 服务进程最多 128 个活跃游戏空间、1024 个连接（包括正在升级的连接）
- 房间名最多 128 字节，metadata 最多 8 KiB，状态和消息最多 16 KiB
- 消息预算每连接 60 条/秒，突发 120 条；WebSocket 帧最多 64 KiB
- 待发送和正在发送的消息合计每连接最多 4 MiB、进程最多 64 MiB，发送队列最多 256 条；慢客户端会断开
- 大厅默认每页 20 条、最多 50 条；管理端非法分页参数返回 400

这些是保护性上限，不是经过负载测试的 2C2G 承载承诺。实际容量取决于消息频率、状态体积、房间人数及数据库负载。

## 生命周期与迁移

房间只保存在当前进程内存；重启丢失，断线离房，空房间自动移除。房主离开时选取剩余成员接任。同一玩家的新连接替换旧连接。SDK 重连只恢复连接，不自动恢复原房间成员身份，应用应重新查询并加入。

本次没有持久化世界、AOI、服务器权威物理或跨节点调度，也没有增加数据库迁移。房间 metadata 对大厅访问者可见，勿放入秘密。

兼容性变化：`room.list` / TypeScript `listRooms()` 现在返回分页摘要，不再返回成员状态；需要完整成员信息时使用 `room.get` / `getRoom()`。创建房间的 `max_players:0` 仍代表默认值；非法类型、未知字段和超限输入会被拒绝。

## 请求关联与连接中断

请求信封可携带 `request_id`（最多 64 个 ASCII 字母、数字、下划线或连字符）：

```json
{"type":"room.get","data":{},"request_id":"request_17"}
```

成功的直接响应和请求错误原样返回该 ID。房间成员广播、聊天广播及其他客户端收到的事件不带 ID；`room.update` 的请求者收到带 ID 的快照，其他成员收到不带 ID 的快照。没有 ID 的旧客户端保持原协议行为。非法 ID 会被拒绝且不会反射到响应中。

ID 仅用于匹配响应，不是幂等键、鉴权信息或重放保护。创建、踢人等操作成功后若连接断开，客户端仍可能没有收到响应；不要据此自动重试。先重新连接并查询状态，由游戏业务决定恢复方式。客户端在断开时拒绝待处理请求，不自动重放。

TypeScript SDK 限制最多 64 个待处理请求，默认响应超时 10 秒。旧版 Go SDK 的事件式方法仍兼容，不要求发送 ID。Go SDK 同时修复并发写入序列化、WebSocket ping/pong 和 `GetRoom` 空对象请求。
