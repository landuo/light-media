# M0 新 NFO 隔离入库实测与剩余边界

2026-10-15 已在飞牛影视 0.9.8 / mediasrv 0.8.43 完成三次**真实服务上的合成样本实验**。下列首轮记录保留原有证据；后续两个实验另列，不能把后补检查追溯算作首轮已验证。它们验证新生成 NFO 的有限字段和本地旁车图片能被实际导入；不等于完整 NFO 标准、真实元数据供应商 ID 或生产整理流程全部通过。

## 实验范围及隔离

使用 UUID `97f462b1-b27c-4a31-a912-ace48d6380db` 命名的新临时 Mix 媒体库及独立兄弟目录。所有标题、演员名、角色名、自定义 ID 和文件路径均在该 UUID 命名空间内。三段视频均为本机新生成的 1 秒 H.264 合成视频，没有复用真实视频、做种文件或真实 TMDB/TVDB/IMDb ID。

五份 NFO 覆盖电影同名 NFO、普通剧/国漫的 `tvshow.nfo`、两个单集同名 NFO；六张 64×64 JPEG 是各作品的 `poster.jpg` 与 `fanart.jpg`。测试库启用 `prefer_local_nfo=1`，关闭小文件过滤、自动字幕和进度缩略图；未改变全局设置或既有媒体库。扫描请求只允许测试库 GUID，禁止 scanall。

创建只调用了一次。初始验证因服务端为库路径追加尾斜杠而拒绝认领；只读诊断确认唯一新库后，采用安全的规范路径比较继续，未重复 create。诊断、观察和清理持续核对原有七个库的 GUID、名称、类别及目录列表。

## 已观察到的结果

| 项目 | 实机结果与限制 |
| --- | --- |
| 新条目导入 | 读到 7 个节点：1 电影、2 TV、2 Season、2 Episode。递归观察共 23 次有界请求，原有库基线不变。 |
| 中文及 XML 转义 | 电影 API 标题正确还原 `合成电影 <验证> & 本地NFO`，包含 UUID 前缀；中文剧集标题也正确。 |
| 年份 | NFO 输入 `year=2024`，实际 API 返回 `release_date=2024-01-01`，电影还返回相同 `air_date`。没有独立 `year` 字段的逐字读回，不能声称日期精度来自原始来源。 |
| 季集及日期 | 普通剧返回 S01E02，国漫返回 S02E01；两个 Episode 的 `air_date/release_date` 均为输入的 `2024-01-02`。这里验证映射后的 NFO 导入，不验证复杂国漫分季映射算法。 |
| 演员与角色 | 电影、两个 Season 和两个 Episode 都返回两位合成 Actor 的名称与 role。两个 TV 根节点的 persons 均为空，不能将“剧级演员读取”标为通过。 |
| 演员顺序 | NFO 的 `order=0/1` 被规范化为 API 的 `order=1/2`，相对顺序保留。 |
| 自定义 ID | 本轮 `lightmedia-m0` 自定义 uniqueid 未映射到可读作品来源 ID，7 个节点的 `trim_id` 均为空。把该项记录为未支持/未验证，不以合成数值冒充真实 TMDB 等 ID。 |
| 图片真实读取 | 从已验证属于测试库的三个根条目取 posters/backdrops 引用，只请求本源 `/v/api/v1/sys/img/`，禁止外域与全部重定向。14 次总请求包含 6 次图片 GET；六张 JPEG 均可解码，原始字节 SHA-256 匹配对应作品组的本地 fixture。 |

图片字节证据：

| 合成作品组 | 每张大小 | SHA-256 |
| --- | ---: | --- |
| Movie (2024) | 823 bytes | `f1834cc17c989c791432ad17580fa777e8c48adc30313304bfe1f99ce5ea8f29` |
| Series | 825 bytes | `6d7f91888f54def29f8f4cd2cd3f6c6f1c8c1a863f16274fa6a37fa9f5d2e6eb` |
| Animation | 825 bytes | `db3e4d1e3b63bf66341aa7f1fae31f6ca89e875cf2595614b70a24a03e5f7cad` |

**图片测试的必要限制：**同一作品组的 poster.jpg 与 fanart.jpg 使用相同字节。因此可以证明正确作品组的本地 JPEG 被服务器实际提供，但不能排除该组 poster/fanart 被互换。若要验证角色区分，需要新实验使用不同字节/尺寸的两张图；本轮没有因此扩大扫描或重新创建库。

## 清理与证据

cleanup 返回 `cleaned`，7 次请求后确认测试库已消失，原有七个库基线不变。程序不删除远端文件；主任务随后确认远端自建合成目录和临时二进制均已清理、broker 已关闭。库清理证据不证明共享演员表或缓存已被完全回收，也不证明所有既有条目元数据的每一字段不变。

本轮脱敏原始摘要保留在 Git 忽略的工作目录：`work/nfo-experiment-observe.json`、`work/nfo-experiment-images.json`、`work/nfo-experiment-cleanup.json`，以及 UUID 收据。正文中的结论来自这些实机结果，HTTP 回放测试仅用于验证程序边界，二者不能互相替代。

## 可复现工具与未覆盖范围

- [experiments/fnos-nfo](../experiments/fnos-nfo/README.md)：显式授权的隔离 M0 CLI，默认 dry-run；凭据只从 stdin 进入内存。它不是生产写入接口，也未扩展 `cmd/fnoscheck` 的只读能力。提供 prepare、diagnose、observe、image_shape、images、单库 scan 和自身库 cleanup；保存创建前收据，拒绝旧 GUID、根目录重叠、任意 API、跨域请求与不确定成功。
- [experiments/nfo-fixtures](../experiments/nfo-fixtures/README.md)：本地合成 XML/JPEG 生成器，显式 UUID，默认 dry-run，拒绝覆盖现有根目录，不执行 ffmpeg、远端命令或服务 API。
- 合成验证命令：`go test -race ./experiments/fnos-nfo ./experiments/nfo-fixtures`、`go vet ./experiments/fnos-nfo ./experiments/nfo-fixtures`。

首轮尚未覆盖真实 provider ID 的写入/匹配、完整演员信息与头像、TV 根演员语义、海报与背景图的不同内容区分、字幕、评分/所有标签、多版本冲突、覆盖既有元数据、长视频、容器路径变化和生产恢复流程。`prefer_local_nfo=1` 仅证明单库优先项已设置，不能推断云刮削回退彻底关闭。此前对真实作品既有 NFO/API 的只读核对仍只属于兼容性参考，不作为合成导入的真实来源 ID 验收。

## 后续实测：独立归属、不同图片及字段比较

两个新实验均保持 3 段新生成视频、5 份 NFO、6 张独立 JPEG，标题/演员/角色/custom ID 保留各自 UUID，不使用真实 provider ID。海报为 48×72，背景图为 96×54，六张字节和 SHA-256 全部不同。实际 detail 必须提供测试库 ancestor_guid；Movie/TV 根 parent_guid 必须是实际明确空字符串，Season/Episode 必须返回真实父 GUID，不能用遍历路径补造父关系。

| 实验 | 实际结果 |
| --- | --- |
| `12567b4f-a170-40c4-acec-79bf497c9e01`：历史 namespaced 路径 | 7 节点归属通过；6 张图的作品、poster/backdrop 字段、SHA、格式、尺寸均逐一通过。普通剧 NFO/文件名为 S01E02，飞牛却返回 S01E01，离线比较判失败；国漫 S02E01 正确。 |
| 同一第二库，只改两个集视频/NFO basename | 本地和远端 NFO 原始内容 SHA 一致，改名不改预期集号；单库 scan 被接受后再次读取，普通剧仍 E01，比较仍失败。scan accepted 不等于字段成功。未重复创建或重复 scan。 |
| `06208206-17d8-42e2-b052-9e84c315040d`：canonical 发布目录 | 目录/视频/NFO stem 使用 `合成电影 (2024)`、`合成普通剧/Season 1/合成普通剧 S01E02`、`合成国漫/Season 2/合成国漫 S02E01`。创建后的自动导入读取 7 节点，43 条离线字段检查通过：普通剧 S01E02、国漫 S02E01、标题/类型/父关系/日期/有限演员角色正确；6 图各自精确匹配。没有追加 scan 请求。 |

canonical 工具测试证明同一 UUID、同一 image mode 的两套布局 XML 与图片逐字节相同，预期 E02 保持不变。不过第三次实机使用了新 UUID、不同导入时间，不能据此确定第二次错误的唯一根因，更不能宣称所有命名均已兼容。M1 应采用已实测的 canonical 目录，发布后核对实际季集；不匹配时进入待确认，不能自动更改目标集号来适配错误结果。

三个实验均保留各自原始脱敏结果。第二次比较失败保留在 `work/nfo-distinct-compare-before-rename-current.json` 与 `work/nfo-distinct-compare-after-rename.json`；第三次为 `work/nfo-canonical-compare.json`。比较器只核对实际 XML 与 API 摘要，不请求网络；`api_read_success` 与字段 passed 分开，所有结果仍 `provider_ids_verified=false`。图片校验另行提供证据，比较器不冒充验证图像字节。

第二/第三次实际 observe 各 23 请求/48 条 JSONL，images 各 14 请求/30 条 JSONL；第三次日志包含固定业务 operation、run/target/request ID。各日志独立受 256 KiB 总量预算控制，未丢弃/写入失败。两库 cleanup 分别确认不存在，既有七库配置不变；共 28 个自建远端文件经精确清单与 SHA 核对后删除，目录消失；凭据 broker 关闭，自己的 NAS staging reader 已清理。

真实三代表样本的标准来源/独立单集 ID 和新 NFO 闭环仍未完成。同源 provider 查询返回的 Trim 不透明 ID不能填作 TMDB ID，详见 `fnos-provider-contract.md`。标准 ID 的写入还需要独立服务状态或已证明的共享缓存隔离；schema 只读研究未取得 DDL，详见 `fnos-identity-isolation.md`。不因 UUID 目录或原库配置不变而声称所有全局元数据/演员缓存无变化。
