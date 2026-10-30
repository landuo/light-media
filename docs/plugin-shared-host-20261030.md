# 插件共享宿主迁移：本轮执行约定

状态只在 [唯一对照表](plugin-module-parity-matrix.md) 和 `migration/ledger.json` 维护。本文件说明执行方式，不另设完成清单。

- 固定源：MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`，前端 `2829e3b08214348f05ff2f5beae644882eccad58`。
- 当前目标基线：`97476582910dc4b696f7ccd5010f28d823812d40`；固定交付分支 `plugins-refactor`。仅助手云端 Linux，全 Go、单用户、本机存储；不部署、不合并 main、不强推。
- 不迁移 AI、工作流、网盘/外部存储及专用扩展点；普通事件、命令、调度继续纳入。
- 先闭合 S05 查询/更新切片：get、list（可选 state）、list_by_tmdbid、update。其余订阅调用组合只按实际普通插件宿主链发现，不据四方法认领整个 DAO 或订阅业务已完成。
- 重新执行固定原程序与目标实际 SDK→宿主→SQLite，覆盖数据/副作用、失败、竞争、冷恢复和分身；独立审查与实际浏览器验收不得由编译或 mock 代替。无法运行的边界明确标未验。
- 上一环境丢失的源码及其旧验收不沿用；历史通过记录只供发现线索。新证据记录准确源版本、实现/输入哈希、实际命令和边界。
- 完整切片验收后立即只选该功能源码、必要测试、精简无敏感说明提交并推送；实际读回同一远端 SHA，查询对应 CI。大批原始材料不是代码交付的前置。
- 默认采用标准 `git add`（精确选取已验相关文件）→ `git commit` → `git push` → 远端读回，不再逐 blob 上传，也不重复请求已授权常规 Git 操作的批准。单项审批等待期间继续无依赖开发/测试；只有明确的整体中止要求才停止整个任务。
- 被拒的历史 README、final-review、截图和大历史正文不改名、不换传输重传。新原始证据另行保留并按授权备份；区分已提交证据、已备份材料与仅本地材料，不承诺云端临时目录永久存在。
- 每次发布保存路径→Git blob SHA 与 SHA256 绑定及准确 commit/ref。源码、依赖、证据和恢复包不当缓存清理；凭据与持久测试账户数据不进入仓库或证据包。

旧业务插件账本完整保存在基线提交的 `migration/ledger.json`，本轮账本用不可变 commit、blob 与 SHA256 引用。旧业务状态不计为本轮平台通过，TorrentTransfer 没有因此被交付。

## S05 四方法契约（验收状态以唯一表为准）

`light-media.sdk-resources/v1` 原生制品按需声明 `subscriptions.get`、`subscriptions.list`、`subscriptions.list_by_tmdbid`、`subscriptions.update`，通过 `internal/plugins.SharedSubscriptions` 使用当前实际调用上下文；声明本身不授予持久回调或其他实例身份。

- 返回 `{id, revision, row}`：`row` 是原 46 列形状，45 个非主键字段保持 SQLite 类型亲和性、NULL 与 JSON 容器。宿主订阅 ID 为稳定 opaque string；不接受通过列 patch 更改主键。这是明确的 Go 身份适配，不声称原 ORM 主键不可改。
- `list.state` 按源逗号拆分，不额外 trim；`list_by_tmdbid` 保留 NULL 与可选 season。无显式排序的结果只按本轮真实 SQLite 计划对照，不承诺跨 SQLite 版本排序。
- `update` 返回提交后的新投影，不是原 Python 返回的 expired ORM 对象；未知瞬态属性不持久化。省略 `expected_revision` 保留 last-write 行为，提供时执行宿主 CAS。这里只更新普通 DAO 数据，不冒充完整订阅新增、搜索、刷新或业务事件链。
- 数值与 JSON 边界不得把失败等同回滚：原数据库允许的无限值提交后，原始行不能作为标准 JSON 返回；对应响应报错但已提交事实保留，后续有限值更新可恢复。数据与私有识别词写入仍处于同一事务；普通数据库失败应整笔回滚。
- 复用现有 SDK 的 16 MiB 帧边界；不是无限大小对象/任意 SQL 通道。SDK 及目标 runtime 全 Go，测试所用 Python 仅独立运行固定原程序。

首片交付只认领这四个方法及其实际已验链，不认领 S05 总组、所有插件或整个平台完成。完整原始证据的私有备份与 Git 中精简证据分别记录；之前被拒的历史材料仍不上传。
