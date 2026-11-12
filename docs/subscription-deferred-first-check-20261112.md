# 新订阅等待正常调度（2026-11-12）

用户要求：创建订阅后不立即检查订阅内容，由定时任务检查；显式手动检查保留。

## 实现边界

新记录在 `saveSubscription` 内直接持久化正常的下一次检查时间，不再写 `NextCheck=now()`。避免仅删除直接入队后，15 秒的调度器又把新订阅视为立即到期。未改动已有记录、编辑/状态切换/重置的既有调度语义。

首次检查复用 `nextScheduleCheck`，而不是自创固定延迟：

- fixed：创建保存时间 + `IntervalMinutes`（默认 360 分钟）。
- follow：活跃更新窗口内 + `ActiveMinutes`（默认 30 分钟）；窗口外 + `FallbackMinutes`（默认 360 分钟）；若下一个窗口开始更早，则使用窗口开始时间。
- 元数据日期复用保存校验已经取得的季信息，并应用起始集数、总集数、用户日期覆盖及 skip/postpone 例外；不额外查询资源或提供方。只提供集号的来源没有日期时，与正常检查一样使用 fallback；有用户日期覆盖则使用覆盖。
- custom：仍由现有时区、周期、例外规则决定窗口。
- manual、paused、completed：不增加自动检查时间。
- `LastCheck` 不伪造为已执行；新增通知、源统计和 Added 事件保持原流程。

调度器提取了单次 tick，15 秒调用频率、暂停/手动/状态过滤、旧预算延迟兼容逻辑不变。测试以传入时钟验证 due 前不入队、恰好 due 时入队，无需真实等待。

## 创建入口及事件审计

- `Subscriptions.vue` 普通创建 → `POST /api/subscriptions`。
- `ExploreActions.vue` 单作品/批量按季，以及 `MultiSeasonSubscribe.vue` 的季批次 → 每项调用同一 POST。前端没有创建后自动 `/check` 请求。
- POST → `createUISubscription` → `executeNewSubscriptionAdd` → durable add 工作流 → `saveSubscription`。源兼容字段同样经过此入口，不存在另一个 `/api/v1/subscribe` 创建处理器。
- Lark 已验证选择及自动下载后的补充订阅 → `saveOriginalChatSubscription` → 同一 durable add/save 流程。显式聊天搜索/刷新不是隐式新增检查，未禁用。
- 原版插件 host 的订阅能力仅 `subscriptions.read`、`subscriptions.update`，没有另一条直接创建能力；更新保留 `NextCheck`。
- `SubscribeGroup` 的 Added 消费者继续按分类更新规则、保留历史及下一次检查时间；未屏蔽 Added 广播，也未删除通知/统计。
- 导入完成后的检查和显式 HTTP `/subscriptions/:id/check` 保留；它们不是新建订阅的隐式检查。

## 验证

新增 `subscription_creation_schedule_test.go`：fixed、follow fallback、custom active、手动、暂停、元数据窗口/例外、due 前/恰好 due、后续正常调度；真实认证 HTTP 按季创建、重复创建不改变已有时间、Added/通知保留、显式手动检查立即入队。

现有 Linux amd64 包集成测试增加断言：真实 Lark 创建后仍未检查；真实 SubscribeGroup Added 更新后仍保持延迟时间。macOS focused 运行不计这两项 Linux 测试为通过，需在对应包 fixture 验证环境运行。

此处均为独占测试数据库、合成元数据/本机 HTTP 协议 fixture，不是 NAS/生产或真实站点验收。

验证命令与边界：

- `go test ./internal/app -run 'TestNewSubscription|TestSubscriptionPersistenceRevision|TestUISubscription|TestSchedule' -count=1`。
- `go test -race ./internal/app -run 'TestNewSubscription|TestSubscriptionPersistenceRevision|TestUISubscriptionDisabledStatistics' -count=1`：通过。
- `node --experimental-transform-types --disable-warning=ExperimentalWarning --test web/tests/subscriptions.test.mjs web/tests/explore-actions.test.mjs web/tests/subscription-dialog.test.mjs`：21/21 通过。
- 额外运行旧 `TestOriginalHostBusinessSubscription*`：提供已构建的 fixture 后，3 个测试在插件制品获取阶段因 `unsafe root ancestor` 被拒绝，未进入订阅断言；没有绕过该安全检查。这不计为业务验证通过。
- 最终本机日志保存在 `work/subscription-deferred-checks/`。普通/race focused 不是全仓库测试或 Linux 包验收。
