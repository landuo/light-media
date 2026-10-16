# CleanInvalidSeed：宿主协议与实际 App journal

正式 `internal/plugins/cleaninvalidseed/host` 实现原算法所需的下载器、文件、配置、通知及调度宿主契约；`internal/app/plugin_clean_invalid_seed_journal.go` 使用本 App 的 SQLite 和加密存储。本文保留首次宿主/journal迁入及后续 admission 的分层证据；后续 App owner/Core/barrier、调度和注册进展见 [App 宿主记录](plugin-clean-invalid-seed-core.md)。用户配置与运行数据迁移仍未执行，没有清理用户种子、原文件或媒体库。

## 当前实现

- 固定原生下载器对象和七种 RPC；每个副作用分别记录 intent、确认或 unknown。种子删除使用 `delete_data=false`，文件检测/删除是另一条受控路径，下载器列表失败不能作为空列表授权删除。
- 文件和目录按原授权根、快照及叶节点证明执行；使用本机 no-replace 原语，实际文件事实和 Core 索引提交分开记录。SQL 提交失败可用原文件事实修复索引，不能重新删除后来替换的文件。
- 保留原配置修改、通知和失败前缀，一次性标志与其计划在同事务发布。暖对象/冷对象/重载语义分别核对，不在命令失败后擅自重置原临时标志。
- 计划保留一次性三秒触发及原 cron、coalesce、单实例和一秒 misfire grace；入队返回绑定真实 RunID/Input 的收据。内部配置 revision 只可重新绑定本 owner 未开始的任务，原 Invocation 与有效配置收据分开，拒绝修改已经开始的执行。
- 可信控制接口可读取暖配置、重新绑定和关联真实 occurrence；同一 Core/Journal 必须延续。接口本身不是 SDK 任意网络、文件或 SQL 权限。
- App journal 按 runs/effects/triggers/notices/config_history 五类分行加密，meta/行和集合完整性均校验，可在同一 SQL 事务投影配置、切换和效果。删除行、篡改、跨实例/代次和非法字符串拒绝；不另建执行队列。纯 host 的独立 SQL 实现仅保留为测试夹具。
- 私有 `clean_seed_journal` 解密上限为 32 MiB，配置及未知 kind 仍为 1 MiB。真实超过 1 MiB 的合成库存可往返；此上限不等于长期内存或全部原用户负载已验收。

## 对照与验证

原插件 2.1 主源码 SHA256 `fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda`，模块 AST SHA256 `0e8b6823865834f207ba1480bf2f73b91c46a2b7f51ac41c934f3cb7333af40a`。正式 host 清单 SHA256 `55c3c1361c77bcd391827b98afd871a759aa3cff5f3a55c00aa239fce056a69f`；保留原失败前缀的离线源码对照。该清单只覆盖 host，App journal 另外验证，不把夹具 SQL 说成生产后端。

正式 host **26 个顶层测试**：本机普通 3.326s、race 12.757s，vet exit 0。包括下载器协议、未知结果重开后不重放、受控文件树、失败列表、配置/SQL 回滚、丢失确认、暖/冷生命周期、命令失败前缀、原 occurrence 与 grace 等。

NAS linux/amd64、CGO_ENABLED=0 的同 26 项全部通过，0 跳过、0 失败、exit 0、无 OOM，二进制 SHA256 `b789bafea74da236e5d62baba8cfb5689fb3c88370807fb0985f51bf1f96d692` 与远端一致。使用 256 MiB/GOMEMLIMIT 64 MiB、网络 none、只读根、UID 1000:1001、drop ALL/no-new-privileges 的独占容器，只访问自建临时文件和回环服务。容器和远端目录已清理，原服务状态不变，见 [host NAS 记录](plugin-clean-invalid-seed-host-nas-verification.json)。

实际 App journal 的 4 项测试和私有上限的 1 项测试另在 [统一 24 项 NAS 记录](plugin-auto-transition-clean-journal-nas-verification.json) 中通过；其余 19 项是 AutoSignIn SQL/transition。这两份报告不覆盖随后编写的 BeforeRun lease、App owner/Core/barrier 或真实插件注册。

首次迁入时尚待接通的实际租约、共享 scheduler/队列、终态、HTTP与生命周期已在后续 [App 宿主记录](plugin-clean-invalid-seed-core.md) 中分别计证。通知消费者与可信命令入口仍需接线，并在原系统只读的前提下迁移配置、数据和引用。纯包测试、空配置安装和原 JSON 复制都不算迁移完成。

## 后续原生 admission 扩展

宿主现支持可信 `BeforeRun` 回调：先取得内部 action gate，再刷新真实下载器和目录权限；回调保留的 action 租约直到最终 checkpoint 完成才释放，所有 admission 失败路径也释放。只允许延续相同 Core/Journal，worker 不能传入回调或构造租约。新增手动服务 trigger 直接执行原 Clean，没有自动 date/cron 的一秒过期限制，也不消耗原单次日期计划。

更新后的 host 清单 SHA256 为 `e4366611818dda698107b5730f149b297b3fe70dab13515683162818fb24d3fe`。NAS 以相同隔离条件通过全部 **29 项**、零失败/跳过且无 OOM；远端二进制 SHA256 与本机一致，容器和临时目录已清理，原服务不变，见 [admission NAS 记录](plugin-clean-seed-admission-nas-verification.json)。上文26项报告保留为首次迁入证据；这份29项报告仍不代替 App 生命周期、正式配置迁移或实际用户种子验证。
