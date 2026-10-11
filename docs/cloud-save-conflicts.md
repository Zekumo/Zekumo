# 云存档冲突保护

同一玩家在多台设备上读到同一份旧存档时，直接覆盖会丢掉其中一台的进度。
云存档现在提供可选的乐观并发控制：读取获得不透明 `revision`，写入或删除时
带上该修订号。数据库在一条语句里检查条件并执行操作，竞争请求最多一个成功。

## HTTP

`GET /v1/player/data/{key}`、成功的 PUT 响应和存档列表均包含 `revision`。
GET/PUT 还返回强 ETag：`ETag: "<revision>"`。列表仍不包含存档值。

- 不带条件的 PUT/DELETE：保留原有行为，旧客户端仍能使用。
- `If-Match: "<revision>"`：仅在当前修订号相符时更新或删除。
- `If-Match: *`：仅在存档存在时更新或删除；不提供丢失更新保护。
- PUT 的 `If-None-Match: *`：仅创建，不覆盖已有存档。
- 条件不符（包括带 If-Match 的目标已删除）：HTTP 412，错误码 `save_conflict`。
- 没有条件的删除仍对不存在的存档返回 404。
- 同时使用两种条件、弱 ETag、多个 ETag、空值或非法修订号：400 `bad_precondition`，不会退化为无条件写入。

当前接口接受单个规范小写 UUID 强 ETag 或 `*`，不接受 ETag 列表。
跨域预检允许这两个条件头，并向浏览器暴露 ETag。请求体仍是存档 JSON 值本身，
不是 `{ "value": ... }` 包装。每次写入都会产生新修订号，即使 JSON 值没有改变。
删除后重建不会复用旧修订号，避免旧设备意外覆盖重建的存档。

## TypeScript

```ts
const saved = await sdk.playerData.getEntry<{ level: number }>("main");
try {
  await sdk.playerData.set("main", { level: saved.value.level + 1 }, {
    expectedRevision: saved.revision,
  });
} catch (error) {
  // ZekumoError: status === 412, code === "save_conflict"
  // 重新读取并由游戏决定合并、让玩家选择或放弃本地更改。
}
await sdk.playerData.set("new-slot", { level: 1 }, { createOnly: true });
const latest = await sdk.playerData.getEntry("main");
await sdk.playerData.delete("main", { expectedRevision: latest.revision });
```

原来的 `get()` 仍只返回值；`getEntry()` 返回值及修订号。
删除时同样使用最近一次成功写入或读取返回的修订号。

## Go

```go
saved, err := client.Data.GetEntry(ctx, "main")
if err != nil { return err }
updated, err := client.Data.CompareAndSet(ctx, "main", nextValue, saved.Revision)
if zekumo.IsCode(err, "save_conflict") {
    // 重新加载并显式合并，不要盲目重试旧进度。
}
_ = updated
```

还有 `Data.Create` 和 `Data.DeleteIfRevision`。原来的 `Data.Set` 继续无条件写入，
现在正确发送原始 JSON；`Data.List` 正确返回读取后的列表。如果应用曾依赖旧 Go SDK
错误地多包装一层 `value` 的行为，升级时需检查已有数据形状，服务端不会自动改写它。

## 部署与边界

迁移 `0021_save_revisions.sql` 给现有存档补充 UUID 修订号，保留值与更新时间。
数据库触发器确保旧版本服务或管理 SQL 修改存档时也会刷新修订号。
先迁移/升级服务端，再发布使用条件写入的新 SDK。旧服务端会忽略条件头，不能保证冲突保护。
新增非空随机默认值可能重写表并持有表锁；请在正式发布前备份，并按实际存档表规模
安排维护窗口和验证迁移耗时。本次只在临时本地测试数据库运行，没有修改线上数据库。

这不是自动合并、离线同步、历史版本恢复或幂等重试。网络超时可能发生在写入成功之后，
必须重新读取后判断；SDK 不会自动重试条件写入。旧客户端仍可无条件覆盖，若需要所有设备
都受到保护，应让所有写入方使用修订号。存档大小上限仍为 256 KiB。
