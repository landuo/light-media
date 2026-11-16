# 整理历史严格单表重构（2026-11-16）

## 结果

- 唯一历史表：`transferhistory`（MP 风格字段与索引）。
- 已删除：`transfer_history_attempts`、`transfer_history_dirty`、`transfer_history_dirty_sources`、`transfer_history_index_version`、`transfer_history_current` 及全部历史触发器。
- 启动迁移：若检测到旧 `transfer_history_current`，先按源把非 pending 行迁移到单表，再删除旧表/触发器；全新库直接只建单表。
- 写入：成功/失败完成时按 `src` 删除旧行并插入新行，自增新 ID；预览与运行中任务不写历史。
- 读取：列表/详情/多选/计划/执行/回调/重试/软链/宿主历史均只读单表；`pending` 状态返回空页。
- 删除：文件删除必须携带当前 `file_id`，否则 `HISTORY_CHANGED`；旧页面/旧 ID 不能删除新执行文件。
- 队列：取消按计划文件 ID 定位活动任务，不依赖历史行；队列仍是独立任务视图。
- 历史季集使用源编号（`GroupNumbering`），不使用目标分组编号。

## 验证

- 历史/手动/目录/队列/upsert/剧集组/provider 身份/移动删除安全/辅助历史/下载准备失败/软链/推荐等套件通过。
- 迁移测试：旧 current 行迁入单表、字段正确、旧表全部删除、`transferhistory%` 仅剩一张表。
- `go test ./...` 通过（`internal/app` 无夹具时按 TestMain 跳过；带夹具的目标套件单独通过）。
- 剩余 CategoryEditor 集成测试需要 `CATEGORYEDITOR_WORKER_BINARY`，属于既有环境夹具要求，与历史重构无关。

## 生产迁移

1. 停 light-media，备份 DB 与 compose。
2. 部署新二进制；启动时自动迁移并删除旧表。
3. 校验：`transferhistory` 行数、抽样 src/dest/status/media、`sqlite_master` 仅一张历史表、健康检查。
4. 回滚：恢复 DB 备份与旧二进制/compose。

## 生产库副本演练（2026-11-16）

- 来源：NAS 当前 `light-media.db`（462 MB）gzip 副本。
- 发现生产库同时存在旧世代 `transfer_history`（1577 行）与 `transfer_history_files/changed_*` 触发器；因此单表严格采用 MP 原名 `transferhistory`，避免命名冲突。
- 迁移后：`transferhistory` 1577 行；`transfer_history%` 表 0 张、触发器 0 个；旧 `transfer_history` 表被删除。
- 应用随后因副本缺少配置加密密钥停止，属于演练副本不完整，不影响数据库迁移结果。

## NAS 部署（2026-11-16 01:25）

- 二进制：`releases/history-mp-single-20261116-0115/light-media-server`，SHA256 `a45d90fc…d79bf`。
- 生产库在线迁移完成：`transferhistory` 1577 行，旧历史表 0、旧历史触发器 0。
- 健康检查 `{"data":{"status":"ok","version":"0.4.19"}}`；容器 running。
- 备份：`deployment-backups/compose.yaml.before-history-single-20261116-0115`、`light-media-db-before-history-single-20261116-0115.db`。
- 旧二进制 `releases/history-mp-b40839bc/light-media-server` 保留用于回滚。
