# fnOS Media 身份与共享缓存隔离：只读研究

2026-10-15。本次目标是确认标准来源 ID 实验是否会与既有库共享条目/演员/元数据缓存；只检查应用目录、数据库文件名和有限 schema，不执行 Media API，不读取业务行、账户、授权、license 或内置 provider 密钥。

**结论：目前没有取得 DDL，数据库隔离边界仍未知。** 新建 UUID 媒体库、目录或条目 GUID，只能帮助识别本次拥有的对象，不能证明标准 provider ID、演员关系、图片或刮削缓存按库隔离。当前证据不足以把“真实标准来源 ID NFO 实验不会影响既有共享缓存”记为已证明。

## 已实际定位的来源

- `/var/apps` 中存在应用 `trim.media`，当前 mediasrv 进程 PID 为 1599。应用 `var` 指向 `/usr/local/apps/@appdata/trim.media`，`target` 指向 `/usr/local/apps/@appcenter/trim.media`。
- SSH 使用既有默认密钥。admin 对应用数据目录和 `/proc/1599/fd` 没有读取权限；`sudo -n ls` 不可用，NOPASSWD 只包含 Docker。没有读取进程环境或命令行中的账户/密钥。
- 经主任务明确限定，使用 NAS 已有 `alpine:3.22`，一次性 `--rm --pull never --network none --read-only --user 0:0 --cap-drop ALL --security-opt no-new-privileges`。实际 helper UID 已报告为 0，排除了以 UID1000 读取 root0600 文件的错误测试口径。仅把 trim.media appdata 与 schema helper 分别只读挂载，没有挂宿主根、docker.sock 或单独的凭据文件，也未改变权限/服务。
- appdata 中 maxdepth 3 的文件名检查发现 `database/trimmedia.db`、`database/trimmedia_ext.db`、`database/trimactivity.db`。没有打开或查询 activity 库。
- 两个媒体数据库文件存在：`trimmedia.db` 为 root 所有、0600；`trimmedia_ext.db` 为 root 所有、0700，对应的 `-wal/-shm` 路径当时不存在。helper 的直接文件读取对这两个明确目标各取最多16字节，只输出 SQLite3 header 是否匹配的布尔值，不输出原始字节；两个均匹配。另有SQLite引擎只读打开尝试，未执行应用内容行查询，未返回任何DDL或业务数据。

SQLite3 header、文件名及进程信息证明定位到了具有SQLite3文件标记的媒体数据文件，但不是表结构或隔离的证明。此前实际 Media 版本 0.9.8 / mediasrv 0.8.43 来自项目已保存的只读 API 证据；本次没有重新登录核对版本。

## 有限只读 schema 尝试

临时 Go helper 使用固定的 modernc 驱动，`mode=ro`、`query_only(1)`、2 秒 busy timeout、单连接与 15 秒总时限。SQL 在数据库侧先筛选 media/item/actor/person/mdb/scrap/season/episode/relation/cast/image/poster 相关对象，并排除 user/account/auth/token/secret/license/password/credential/key配置名称；只准备读取 `sqlite_master` 的 table/index/trigger 定义，不查询应用用户或媒体内容行。最多 80 项，每份 DDL 最多 8192 bytes。

先执行 names-only，再仅增加数值错误码及UID/路径元数据诊断以解释失败。最后显式 UID0 复核只读挂载和路径解析后，两个库仍返回相同的14；没有扩大查询范围，随后停止数据库读取：

| 候选数据库 | query_only | 首个失败阶段 | SQLite 数值错误 | DDL结果 |
| --- | ---: | --- | ---: | --- |
| `database/trimmedia.db` | 1 | `SELECT sqlite_version()` | 14 | 未取得 |
| `database/trimmedia_ext.db` | 1 | `SELECT sqlite_version()` | 14 | 未取得 |

最终元数据：

| 目标 | 实际UID | regular / symlink | 解析后的容器路径 | 大小bytes | 挂载readonly / SQLite3 header |
| --- | ---: | --- | --- | ---: | --- |
| trimmedia.db | 0 | true / false | `/dbs/database/trimmedia.db` | 67,436,544 | true / true |
| trimmedia_ext.db | 0 | true / false | `/dbs/database/trimmedia_ext.db` | 36,384,768 | true / true |

这些路径均成功解析到当前只读 bind 内的普通文件，不存在已观察到的绝对 symlink 目标缺失；未通过增加宿主根挂载来补路径。

数值 14 是 `SQLITE_CANTOPEN`，表示 SQLite 无法打开所需文件，可能涉及主文件或其他所需文件；该码本身不确定具体原因。[SQLite 官方结果码说明](https://www.sqlite.org/rescode.html#cantopen)

只读挂载、当前不存在的 WAL/SHM sidecar 与失败相符，但这里只能提出可能性，不能断定服务使用哪种 journal模式或把主文件当作可安全独立读取的当前状态。14也不是取得密钥/证明加密的证据，不据此寻找或读取密钥。没有使用 `immutable=1` 跳过 WAL，没有切换写挂载，没有创建 SHM、复制/备份业务库、停止服务、checkpoint 或修复数据库。

脱敏结果在 `work/fnos-schema-trimmedia.db-names.json`、`work/fnos-schema-trimmedia_ext.db-names.json`；临时 helper 源码/架构明确的二进制在 Git 忽略的 `work/_fnos-schema-reader/`、`work/fnos-schema-reader-linux-amd64`。远端仅上传 helper 到本步骤独占的新 `/tmp/light-media-schema.*` 目录，完成后删除自己的 helper 文件并移除该空目录；没有动主任务 reader/staging 或业务文件。

## 可以证明与仍不能证明

| 问题 | 当前判断 | 原因 |
| --- | --- | --- |
| 标准来源条目 GUID 是否按 MDB/lib 隔离 | **未知** | 未取得相关表DDL、复合唯一约束、外键或应用查找语义 |
| provider/source ID 是否全局唯一或全局复用 | **未知** | 不能由库GUID、路径或SQLite文件名推导；没有读取schema |
| actor/person 及关联表是否按库限定 | **未知** | 没有取得演员/关系表定义；UUID演员名避免命名碰撞不等于表作用域隔离 |
| 图片/刮削缓存是否共享同一 provider key | **未知** | 没有缓存键schema或对应行为证据，未读取任何内置provider配置 |
| 临时库/自建目录能否准确认领并清理 | 前次隔离实验有有限证据 | GUID/路径/基线核对可约束直接操作，但不证明共享缓存、演员记录或所有既有字段未变化 |

即使后续拿到 schema，`UNIQUE(provider_id)` 或复合 `(library_id,provider_id)` 也只能证明数据库所声明的约束。应用可能在插入前做全局查找、共用缓存/图片目录或更新共享 actor；仅有DDL仍不能证明标准 ID 试验的全部副作用已经隔离。

## 对下一次标准 ID 实验的含义

当前不能仅凭随机库GUID/目录就把既有库中的真实来源 ID 导入当作安全隔离已成立。若继续技术验证，需要一条可复核的隔离路径，例如服务完整克隆的独立测试实例、厂商明确的作用域契约，或在不改写既有数据的前提下补充可观察的应用级证据。不能把本次 schema 读取失败改成“没有共享缓存”，也不能为取DDL而读取内置密钥或把在线数据库只复制主文件。

本文件只记录证据与未知项，不授权创建新服务、复制业务库或扩大 Media 写操作。主任务据此决定下一步，已取得的合成字段/NFO及图片读取结果仍保持原有有限范围。
