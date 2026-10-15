# M0 新 NFO 入库实测与兼容边界

2026-10-15 已在飞牛影视 0.9.8 / mediasrv 0.8.43 完成三次**真实服务上的合成元数据实验**及两轮**标准来源代表样本实验**，所有视频仍为新生成的合成片段。原始 M0 的三种代表样本命名/NFO 和兼容验证已在列明范围通过；总矩阵为 10/10 交付物、6/6 进入 M1 条件通过，见 [m0-acceptance-matrix.md](m0-acceptance-matrix.md)。

五条官方 TMDB 实请求确认来源身份；两轮标准样本各完成 7 节点归属、3 条后端实际文件路径和 30 项字段/关联比较，并完成清理。**标准来源 ID 在飞牛 API 的直接读回仍为 `unverified`**，不能据缺字段推断内部未保存或不支持。完整 NFO 字段、生产整理流程、全局缓存隔离和完整应用资源验收不由本轮证明。下列三轮历史记录保留，后补检查不会追溯算作首轮已验证。

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
| 自定义 ID | 本轮 `lightmedia-m0` 自定义 uniqueid 没有得到可观察的来源 ID 读回，7 个节点的 `trim_id` 均为空。记为本轮未验证，不能据此推断后端未保存或所有 NFO ID 格式均不支持；不以合成数值冒充真实 TMDB 等 ID。 |
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

- [experiments/fnos-nfo](../experiments/fnos-nfo/README.md)：显式授权的临时库 M0 CLI，默认 dry-run；凭据只从 stdin 进入内存。它不是生产写入接口。提供 prepare、diagnose、observe、image_shape、images、inspect_files、单库 scan 和自身库 cleanup；保存创建前收据，拒绝旧 GUID、根目录重叠、任意 API、跨域请求与不确定成功。既有条目的有限 before/after 摘要由单独的只读 `cmd/fnoscheck` 提供。
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

上述三轮仅使用合成自定义来源 ID；真实三种代表样本的后续证据见下一节。同源 provider 查询返回的 Trim 不透明 ID仍不能填作 TMDB ID，详见 `fnos-provider-contract.md`。schema 只读研究没有取得 DDL，详见 `fnos-identity-isolation.md`；全局元数据/演员缓存隔离仍未知。独立服务、获取 DDL 或证明全局缓存隔离不是原始 M0 额外门槛，也不能反过来因为 UUID 目录或原库配置不变就宣称共享状态没有变化。

## 标准来源代表样本：实际五次官方请求

`work/live-metadata-authorized-source.json` 为固定官方 HTTPS TMDB 探针的实际结果：`evidence=live_request`、五个 GET、全部 `ok`，12 条 JSONL、logging健康且无丢弃/写入失败。凭据只在主任务授权的内存执行链中使用，不进入本文件或报告。

| 固定样本 | 官方已确认类型与来源 ID | 官方来源日期/年 | 单集上下文 |
| --- | --- | --- | --- |
| 夜王 | `tmdb:movie:1602727` | 2026-02-14 / 2026 | 不适用 |
| 生活大爆炸 | `tmdb:tv:1418` | 2007-09-24 / 2007 | 不适用 |
| 友谊的湍流 | `tmdb:episode:972676` | 2014-03-06 / 2014 | 父 TV1418，S07E17 |
| 诛仙 | `tmdb:tv:206484` | 2022-08-02 / 2022 | 不适用 |
| 偶遇小白 | `tmdb:episode:7706553` | 2026-09-04 / 2026 | 父 TV206484，S04E05 |

两个单集的父上下文均为 `verified_series_and_fixed_request_route`：先验证父剧，再使用固定父剧/季集请求路由；没有把缺少的响应 show_id 字段说成已经直接返回。单集 own SourceID 不从 RequestedSourceID/ParentSourceID 复制；身份按来源、类型和 ID 区分，不因不同类型的数字相同而合并。

`experiments/metadata-nfo` 严格验证这五条报告后生成三作品 canonical 目录的五份 XML；默认零文件 dry-run，仅显式 apply 创建独占新 work 目录。输入原文 SHA 和五份 XML SHA 保留供后续关联。离线工具的 `supplied_validated_report` 及 `input_source_authenticity=not_independently_verified` 只描述它能校验提供的报告，不能独立认证远端请求。真实来源证据来自固定官方探针的另行实际执行，不来自 JSON 自己声明或离线 passed。

电影/父剧 NFO 根使用 title/year/uniqueid；v2附加同值 tmdbid 兼容标签，未增加根日期标签。来源完整日期留在报告/计划。API 实际返回电影 `2026-01-01`、两剧 `2007-01-01/2022-01-01`，只按**年份**核对，不能说完整发行/首播日被读回。两个 episodedetails 才写 aired、showtitle、明确季集及自己的独立集 ID，实际日期分别精确匹配 `2014-03-06`、`2026-09-04`。

## 两轮标准 NFO 实机、文件绑定和比较

| 运行 | 输入及实机结果 | 比较和收尾 |
| --- | --- | --- |
| v1 `24dd83d2-28d9-4052-8ebd-0282d808cf2f` | 五份标准来源 uniqueid NFO、三段新合成视频；实际 1Movie、2TV、2Season、2Episode。observe23请求/48事件，7节点 ancestor/parent/type通过；inspect32请求/66事件，3条后端实际路径成立。 | `work/nfo-real-compare.json`：`real_nfo_sample_association_v1`、`passed`、30项检查。两个集号/日期、标题、父链、根年份与实际文件绑定通过；直接标准ID仍未验证。 |
| v2 `63a741d5-3dfb-4319-b512-ebd324d81128` | 新独立库/自建目录；五份 XML 保留 uniqueid，并附加标准 tmdbid 兼容标签。observe23/48，7节点归属；inspect32/66，3条实际路径。 | `work/nfo-real-tmdbid-compare.json` 同样30项 `passed`；API 没有可证明的标准ID直接读回。不能由此判断 tmdbid/uniqueid 的内部保存或支持情况。 |

两个实验没有生成演员/图片资料，合成图片角色/字节兼容性由前述不同实验单独证明，没有把它们混成这两轮也下载了图片。三段视频都是新合成片段，只承载文件/NFO验证，不是实际电影/剧集内容，不证明真实媒体 payload 的身份。

30项比较包括唯一标题/类型、根年份和父关系、两个集号/日期/showtitle/真实父链、三条实际文件绑定、七节点完整性，以及“无冲突显式标准ID”检查。最后这一检查在未返回标准ID时只表示没有相反断言；**不是来源ID已读回**。两轮比较均明确 `direct_standard_id_readback=unverified`；不能将 `identity_readback_count=5` 解释为五个标准ID从飞牛API直接返回。

### 文件关联来自实际返回值

canonical 三条视频路径分别为：

- `夜王 (2026)/夜王 (2026).mkv`；
- `生活大爆炸/Season 7/生活大爆炸 S07E17.mkv`；
- `诛仙/Season 4/诛仙 S04E05.mkv`。

先完整验证自有测试库及七节点；只用当前 Movie/Episode GUID 进行后续读操作，不接收任意 GUID/endpoint。各路读取与结果分开记录：

| 实际读取阶段 | 结果及证据 |
| --- | --- |
| POST `/item/getEditDetail`，26请求版本 | 三个条目的允许字段中只有 item_guid，没有可证明文件路径；`work/nfo-real-inspect_files-no-path-history.json` 仍为 `FILES_NOT_PROVEN`。 |
| 重新 GET `/item/:guid`，29请求版本 | 再核对当前 type/ancestor/parent/depth，实际 media_stream 是浅对象，没有 files/path；`work/nfo-real-inspect_files-shallow-stream-history.json` 仍未验证。 |
| GET `/media/itemfile/:guid`，32请求版本 | 已安装公开客户端及调用方证实该路径接收 item GUID 并返回 file_path 数组。仅在前两端无唯一路径时读取；最多4记录、有关系断言则核对，三份单一关联路径均来自实际后端返回，精确位于对应实验根内。两轮各32请求，没有扩大32上限。 |

三端字段 shape 均保留；外库、坏路径、冲突关系、URL/越界/遍历立即失败，不用后续回退绕过错误。缺路径或多文件仍未验证。`file_evidence` 仅携带 experiment_id/owned_guid/root 和三条 item_guid/relative_path/actual_path，不从 GUID、目录名或本地 manifest 推造 actual_path；另用自建清单、原始 XML/视频 SHA 连接已知准备输入。该证据是本轮路径关联，不替代所有物理别名/真实视频内容检验。

### 既有条目的有限对照

两轮各自在实验前后通过固定三标题的安全读取取得既有条目摘要；before/after 各17请求。搜索必须标题/类型唯一且未截断，GET detail 核对 GUID、已知库 ancestor、根空 parent，记录已实际返回的核心字段。不是任意路由或全库扫描。

`work/nfo-real-existing-baseline-comparison.json` 和 `work/nfo-real-tmdbid-existing-baseline-comparison.json` 均为 `passed`：三个既有条目的已捕获字段相同；v2明确记录库配置也相同，库清理另外核对原有七库配置。`all_existing_metadata_compared=false`、`global_cache_audited=false` 按原义保留。未采集字段、其他条目、全局演员/来源/图片缓存不在对照范围。

### 清理与原始证据

两轮 cleanup 均6请求、`cleaned`，确认临时库不存在、原有七库 GUID/名称/类别/目录列表不变。每轮8文件（5NFO+3新合成视频）经自建清单和哈希核对后删除，自建根不存在。`work/real-experiments-final-cleanup.json` 最终确认两库、16文件、自建根、凭据 broker 和自有 NAS staging 收尾完成；`global_cache_reclamation=unverified`，不声称所有共享缓存已回收。

原始本地摘要在工作目录中分别保存，不覆盖历史结果：

| 证据类别 | v1 | v2 |
| --- | --- | --- |
| 输入/清单 | `real-experiment-plan-24dd83d2-28d9-4052-8ebd-0282d808cf2f.json`、`nfo-real-manifest.json` | `nfo-real-tmdbid-manifest.json` 及对应本地 XML |
| 观察/绑定 | `nfo-real-observe.json`、`nfo-real-inspect_files.json`、`nfo-real-file-evidence.json` | `nfo-real-tmdbid-observe.json`、`nfo-real-tmdbid-inspect_files.json`、`nfo-real-tmdbid-file-evidence.json` |
| 离线比较 | `nfo-real-compare.json` | `nfo-real-tmdbid-compare.json` |
| 既有条目快照 | `nfo-real-baseline-before.json`、`nfo-real-baseline-after.json`、`nfo-real-existing-baseline-comparison.json` | `nfo-real-tmdbid-baseline-before.json`、`nfo-real-tmdbid-baseline-after.json`、`nfo-real-tmdbid-existing-baseline-comparison.json` |
| 清理 | `nfo-real-cleanup.json`、`nfo-real-remote-file-cleanup.json` | `nfo-real-tmdbid-cleanup.json`、`nfo-real-tmdbid-remote-file-cleanup.json` |

上述接口运行各有对应 .jsonl。标准来源5/12、observe23/48、inspect32/66和cleanup6/14的日志健康均为ok，failed/dropped为0；run/target/request ID只作关联，不证明业务/字段成功。`make check`、`make build linux-amd64 experiments` 在最终代码上退出0，不代替实机字段比较。

## M0 结论与 M1 处理边界

原始 M0 的有限兼容性/三样本闭环已通过；不存在因未取得内部 DDL、未部署独立服务或未证明全局缓存隔离而追加的 M0 阻断。它们仍是如实披露的未知，不是已经获得的隔离证明。

M1采用已实测 canonical 发布布局，持久化应用自己的 `source+type+ID` 与资产映射；发布后按实际库/父链、季集、字段和文件关联核对，不依赖空 trim_id 来判断来源身份。季集错误进入待确认，不修改目标集号来适配错误结果。ID在后端内部如何保存和经哪个API暴露仍未验证，不能写成确定不支持。

历史 namespaced E02→E01 失败、首次同字节图片的角色局限、TV根演员为空、演员顺序规范化及年精度日期都保留。完整演员资料/相关推荐、复杂国漫自动映射、所有NFO扩展字段、下载/整理端到端、生产恢复、完整应用资源/72小时稳定性与P1插件按后续阶段验收。
