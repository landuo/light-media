# light-media · M0 全部验收

## 验收结论

M0 按原始技术验证范围全部通过：10/10 项交付物、6/6 项进入 M1 条件。五站有限协议、双下载器只读能力、飞牛样本 NFO、NAS 路径与硬链接、三类真实代表作品元数据关联、Go/SQLite 基线及日志/扩展草案都有实际证据。

本次补齐官方 TMDB 五条元数据，并完成两轮真实来源 NFO 实验；每轮均核对 7 个实际节点、3 条服务返回文件路径及 30 项字段比较。第二轮对齐旧 MoviePilot 同时写入 tmdbid 与 uniqueid 的格式。两个测试库、16 个自建文件和本次 NAS 临时程序均已清理，既有 7 库配置及三个代表条目的有限字段保持一致。

这是技术路线验收；完整自动化程序、生产部署和长时间稳定性仍属后续里程碑。飞牛未明确回传标准 TMDB ID，直接标准 ID 读回仍为未验证；此限制单列，不能推断其内部保存状态。

## 独立项目与实施范围

| 项目 | 结果 |
| --- | --- |
| 项目目录 | /Users/accidia/projects/light-media |
| 原始需求基线 | docs/design-v1.4.txt / .html；原始 M0 未要求克隆飞牛服务、取得内部 DDL 或审计全局缓存。 |
| 技术选择 | Go 1.26.2；SQLite 初选 modernc 静态部署；不引入 Python 运行依赖。 |
| 当前交付 | 可复现的 CLI 验证工具、命名/集号/NFO 核心实验、协议与日志接线、资源实测、接口契约和验收证据。 |
| 旧项目 | MoviePilot 与 moviepilot-v2 只读参考；本轮前后 Git 工作树状态一致。 |
| 授权凭据 | 按用户明确授权，从原 MoviePilot 现有配置读取 TMDB key；仅经内存/stdin 交给固定官方 API。无新凭据文件，无密钥回显。其他授权服务配置同样限于任务目标。 |
| NAS 范围 | 独占 UUID 测试目录/媒体库及一次性容器；未修改原视频、下载器设置、既有媒体库配置或飞牛全局设置。 |

## 逐项验收矩阵

| 原始 M0 交付物 | 结论 | 有限证据 |
| --- | --- | --- |
| D01 五站协议样本 | 通过 | 每站实际鉴权、一页搜索、一份种子材料结构；完整属性、分页、促销和长期适配尚待实现。 |
| D02 双下载器版本/能力 | 通过 | 实际版本、认证、任务和文件只读样本；未拿现有种子测试任何写操作。 |
| D03 飞牛兼容验证 | 通过 | 合成字段/演员/六张不同图片；真实来源两轮 7 节点、3 文件关联与 30 项比较。 |
| D04 真实 NAS 路径/硬链接 | 通过 | 所选宿主/容器两组路径实际 link、SameFile、改名/解除链接、源内容保持和清理。 |
| D05 一部电影命名/NFO | 通过 | 夜王：官方 movie/1602727 → 实际 NFO → 飞牛 Movie/年份/实际文件路径。 |
| D06 一部普通剧命名/NFO | 通过 | 生活大爆炸：tv/1418，S07E17 自身 episode/972676；父链、日期与实际文件匹配。 |
| D07 一部常看国漫命名/NFO | 通过 | 诛仙：tv/206484，S04E05 自身 episode/7706553；父链、日期与实际文件匹配。 |
| D08 Go+SQLite 内存基线 | 通过 | 真实 NAS 受限容器、双驱动比较、七项正确性检查；选定 modernc。 |
| D09 日志草案与接线 | 通过 | 实际 CLI 请求日志、run/target/request 关联、有界脱敏、HTTP/业务失败与空结果区分。 |
| D10 扩展接口草案 | 通过 | v0.1 事件、幂等、能力和控制边界；P1 插件运行器尚未实现。 |

| 进入 M1 条件 | 结论 | 证据 |
| --- | --- | --- |
| G01 五站接入可行 | 通过 | 五站有限鉴权/搜索/材料实测。 |
| G02 双下载器可行 | 通过 | qBittorrent/Transmission 实际只读协议及历史文件样本。 |
| G03 飞牛读取样本 NFO | 通过 | 三类代表作品及合成兼容样本均有隔离导入/读回。 |
| G04 实际挂载与稳定身份 | 通过 | 实际路径/硬链接；类型化来源 ID、独立单集 ID、真实 NFO 与服务文件/父链关联。 |
| G05 驱动及基线确定 | 通过 | 双驱动比较与真实 NAS modernc 资源/事务实验。 |
| G06 接口错误可追踪 | 通过 | 统一关联日志与实际请求；合成错误回归单独标记。 |

## 五站、双下载器与飞牛

| 目标 | 实际结果 | 验收边界 |
| --- | --- | --- |
| qBittorrent | ok · v5.1.2 · 6 请求 | 单种文件 1；仅只读；write_verified=false |
| Transmission | ok · 4.1.3 (838877323f) · 5 请求 | 单种文件 22；仅只读；write_verified=false |
| 馒头 | ok · mteam_api_candidate · 2 请求 | 有限候选结构 10 个；非完整站点适配 |
| 憨憨 | ok · hhanclub_html_candidate · 2 请求 | 有限候选结构 100 个；非完整站点适配 |
| UBits | ok · nexusphp_candidate · 2 请求 | 有限候选结构 100 个；非完整站点适配 |
| 家园 | ok · nexusphp_candidate · 2 请求 | 有限候选结构 10 个；非完整站点适配 |
| 1PTBA | ok · nexusphp_candidate · 2 请求 | 有限候选结构 25 个；非完整站点适配 |

| 补充 | 事实 |
| --- | --- |
| 家园实际目标 | 现有配置名称为家园，地址 hdhome.org；不将另一个站名相近的站点混入结论。 |
| 飞牛版本 | 前端 0.9.8，后端 0.8.43；账号认证、管理员只读和既有 7 库查询。 |
| 种子材料 | 五站各一份材料结构通过；原字节只在进程内检查，不保存私有种子、不提交下载器。 |
| MTeam 下载跳转 | 仅对实际证明的固定材料 CDN 做窄化允许；站点密钥不转发到该 CDN，不放开任意跨源跳转。 |
| Transmission 两次结果 | 历史 5 请求 / 1 hash / 22 files 通过；后续 recent 有界重查为 4 请求 / 0 files / unverified，两者分别保留。 |

| 材料目标 | 状态 | 结构结果 |
| --- | --- | --- |
| 馒头 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1581113835,"payload_verified":false} |
| 憨憨 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":6,"total_bytes":49064060667,"payload_verified":false} |
| UBits | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1565188927,"payload_verified":false} |
| 家园 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":446872198,"payload_verified":false} |
| 1PTBA | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1581113835,"payload_verified":false} |

## 官方元数据与真实命名关联

| 固定样本 | 标准身份 | 标题/日期 | 父剧或用途 |
| --- | --- | --- | --- |
| movie-nightking | tmdb/movie/1602727 | 夜王 · 2026-02-14 | year=2026 |
| series-bigbang | tmdb/tv/1418 | 生活大爆炸 · 2007-09-24 | year=2007 |
| episode-bigbang | tmdb/episode/972676 | 友谊的湍流 · 2014-03-06 | tv/1418 · S7E17 |
| series-zhuxian | tmdb/tv/206484 | 诛仙 · 2022-08-02 | year=2022 |
| episode-zhuxian | tmdb/episode/7706553 | 偶遇小白 · 2026-09-04 | tv/206484 · S4E5 |

固定官方 API 实际 5 次 GET 全部成功，12 条关联日志，无丢弃或日志失败。来源为用户授权的原 MoviePilot 配置默认值；没有单独自用 key 并不等于本次没有可用授权来源。单集父关系以已验证父剧及固定请求路由为依据，不伪造 API 未返回的 show_id 字段。

三条实际文件名分别为“夜王 (2026) - 1080p.mkv”“生活大爆炸 - S07E17 - 第 17 集.mkv”“诛仙 - S04E05 - 第 5 集.mkv”。纯名称输入不伪造身份，历史 needs_review 结果保留；新增确认输入仅从这份真实报告复制类型化 ID 与目标集，3/3 解析及映射通过。源报告 SHA-256：de525228bcd112c304ffb765ce0f473a2506453da328bef7e6eab22136377e3a。

## 两轮真实来源 NFO 与飞牛关联

| 实验 | 配置与实际结果 | 清理与既有基线 |
| --- | --- | --- |
| v1 uniqueid-only | UUID 24dd83d2-28d9-4052-8ebd-0282d808cf2f；5 NFO + 3 新生成 1 秒视频；observe 23 请求，inspect 32 请求；30 项比较通过。 | 测试库移除；8 个文件逐项 SHA 核验后移除，根目录消失；前后各 17 请求，三个既有条目的所读有限字段一致。 |
| v2 tmdbid + uniqueid | UUID 63a741d5-3dfb-4319-b512-ebd324d81128；全新目录及 3 段新视频；相同来源 ID 同时写两标签，单集使用自身 ID；observe 23 请求，inspect 32 请求；30 项比较通过。 | 测试库移除；8 个文件逐项 SHA 核验后移除，根目录消失；前后各 17 请求，三个既有条目的所读有限字段及库配置一致。 |
| 实际读回 | 1 Movie、2 TV、2 Season、2 Episode；真实 ancestor/parent；S07E17、S04E05、单集标题和播出日；3 条实际 file_path 精确绑定本轮目录。 | 未从目录遍历补造父 GUID；未从 GUID 拼出“观察到的路径”。 |
| 文件接口证据 | 先读取编辑详情及 scoped item；仍缺路径时，使用客户端已证实的 /media/itemfile/:guid；每轮成功恰为 32 请求。 | 不是播放/转码请求；第 33 请求仍拒绝；错库/越界/多文件歧义拒绝。 |
| 元数据与文件真实性 | 真实来源报告 → 5 份实际 XML 的 SHA/内容 → 实际条目类型/季集/父链/日期 → 3 条服务路径。 | 视频均为新生成测试片段；验证关联，不声称片段是作品真实内容。 |
| 标准 ID 直接读回 | 两轮 direct_standard_id_readback=unverified；trim_id 为空，无明确 TMDB 命名空间字段。 | 不推断飞牛内部是否保存或不支持 ID；本应用保持自己的类型化身份账本。 |
| 日期 | Movie/TV 的 NFO 只写 year，飞牛规范化到当年 1 月 1 日；单集 aired 与官方精确日期相同。 | 根条目仅通过年份，不声称完整上映/首播日读回。 |

离线比较器验证输入证据的一致性，仍诚实输出 input_source_authenticity=not_independently_verified；真实来源由独立的官方请求执行记录证明。比较器固定支持 v1/v2 两种编码，历史 v1 文件与哈希没有被新编码回写；矛盾 ID、父 ID 冒充单集、错误父链/路径/日期都会失败。

## 历史合成兼容实验与失败记录

| 轮次 | 保留结果 |
| --- | --- |
| 首轮 UUID 97f462b1-b27c-4a31-a912-ace48d6380db | 7 节点，中文/XML 转义、电影/季/集演员与日期正确；6 张 64×64 图同组 poster/fanart 同字节，不能证明两角色无互换。库及 14 文件清理。 |
| 第二轮 UUID 12567b4f-a170-40c4-acec-79bf497c9e01 | 7 节点实际归属、6 张不同图逐字段匹配；普通剧期望 E02 却返回 E01，只改 basename 并单库 scan 仍 E01。两次比较失败保留，库及 14 文件清理。 |
| 第三轮 UUID 06208206-17d8-42e2-b052-9e84c315040d | 43 项字段比较通过；canonical 目录下普通剧 S01E02、国漫 S02E01 正确；6 张不同 SHA/尺寸的图片与 poster/fanart 角色逐一匹配。库及 14 文件清理。 |
| 原因界限 | 第三轮改变了布局、UUID 和时间；只证明 canonical 布局可行，未证明第二轮集号错误的唯一根因。 |
| 演员兼容 | Movie/Season/Episode 可返回合成演员 name/role；order 0/1 规范化为 1/2；TV 根 persons 为空。剧级完整演员及头像尚未覆盖。 |
| 共享缓存 | 各轮自建库/文件清理不等于全局演员/图片/provider 缓存完全回收。没有取得内部 DDL，不作内部存储结论。 |

此前将独立服务状态/内部 DDL 或全局缓存隔离证明提升为 M0 先决条件，属于额外加严，现已撤回。原方案要求独占测试库和文件、归属检查与受控清理；本次按该范围执行，并增补三个既有条目的有限字段快照。未把有限快照扩大为全库无变化证明。

## NAS 路径、身份与硬链接

| 角色 | 实际配置或实测布局 | 界限 |
| --- | --- | --- |
| qBittorrent | 容器 /media/download → 宿主 /vol2/1000/media/download | Mounts 确认；非默认 save_path 验证；另一 /downloads 路径未测。 |
| Transmission | 原生默认 /vol2/1000/media/download/电视剧 | 只读路径核对；未启用 incomplete-dir。 |
| 飞牛 | 国漫库 /vol2/1000/media/国漫/ | 正式目录只读核对，测试使用自己的新文件。 |
| 建议应用 | 整个宿主 /vol2/1000/media → 容器 /media | 现有 Alpine 一次性容器，UID1000/GID1001；非生产应用已部署。 |
| 实际两组实验 | /media/download 和 /media/download/电视剧 → /media/国漫 | link、SameFile、目标改名/解除链接后源保持及清理全部通过；不推广到其他卷。 |

已验证的静态布局适合保留做种文件与整理目录；硬链接共享内容，不允许整理过程原地改写共享视频。跨卷降级、崩溃恢复与生产容器部署属于应用实现阶段。

## Go 与 SQLite 资源基线

| 条件/指标 | 实测结果 |
| --- | --- |
| NAS | Debian 12 / x86_64；4 个逻辑 CPU；MemTotal 8,053,360 KiB。 |
| 限制 | 已有 alpine:3.22，一次性/无网络/只读根；1 CPU / 512 MiB，GOMEMLIMIT=256MiB。 |
| 场景 | 200 订阅、5000 元数据、50000 文件，独占 /vol2 ext4 目录，合成数据，实验后清理。 |
| 实际 SQLite | 3.53.4 |
| 驱动 | modernc v1.60.1 |
| 子进程最大 RSS | 25.688 MiB；wait4 最大值，不是进程树求和。 |
| 容器 cgroup 峰值 | 50.293 MiB；含相关进程/页缓存记账。 |
| 耗时 / 分页 p95 | 31.057 秒 / 0.839 ms |
| 正确性 | claim_and_job_transaction_rollback: passed；foreign_key_enforcement: passed；reopen_and_expired_job_lease_recovery: passed；source_integrity: passed；ungraceful_process_exit_recovery: passed；unique_reservation_with_contention: passed；vacuum_into_snapshot_integrity_and_isolation: passed |

本机同场景三轮 cgo 对照更快、RSS 较低；modernc 的选择依据是静态部署和交叉编译便利，未宣称它比 cgo 更省内存。这里是数据库实验，不能外推完整应用空闲内存、峰值或 72 小时稳定性。

## 日志、错误与插件预留

- 各 HTTP CLI 的 stdout 是结果 JSON，stderr 是每次独立有界 JSONL；256KiB 预算、run/target/request ID、固定 operation、字段白名单与秘密/URL 脱敏。实际 TMDB 和飞牛请求均有日志，dropped=failed=0。
- 错误回归覆盖 HTTP400、重定向、断连、HTTP200 业务失败；正常空结果与异常空表/模板变化、缺鉴权分别判定。HTTP 成功/日志成功不替代字段验收。
- SQLite 验证唯一占位冲突、同事务回滚、外键、异常退出后已提交保留/未提交回滚、租约恢复、VACUUM INTO 快照一致性/隔离与源完整性。
- 命名与集号保留原始/标准编号；中文集号冲突、未知目标集、范围外及多映射进入待确认，不自动猜测。
- P1 插件只交付 v0.1 事件封套/幂等/能力/控制草案；辅种、转移做种及插件运行器未实现，也未增加独立做种任务产品模块。
- 文件轮转、Web 日志中心、持久事件及完整状态机属于后续应用实现。

## 最终验证与清理

| 检查 | 结果 |
| --- | --- |
| go_test_all | passed |
| go_test_race_all | passed |
| go_test_modernconly_cgo_disabled | passed |
| go_vet_all | passed |
| go_mod_verify | passed |
| macos_arm64_static_cli_build | passed |
| linux_amd64_static_cli_build | passed |
| isolated_experiment_cli_build | passed |
| metadata_unconfigured_requests_zero_exit_one | passed |
| nfo_comparison_cli_build | passed |
| canonical_nfo_fields_actual_fnos | passed |
| distinct_six_image_roles_actual_fnos | passed |
| all_seven_item_ancestry_actual_fnos | passed |
| owned_two_libraries_and_28_files_removed | passed |
| credential_broker_closed_and_staging_removed | passed |
| git_diff_whitespace | passed |
| metadata_nfo_static_macos_linux_build | passed |
| actual_unconfigured_metadata_nfo_zero_file_rejection | passed |
| strict_five_record_metadata_nfo_tests | passed |
| actual_official_tmdb_five_gets | passed |
| real_filename_source_bound_mapping_three_samples | passed |
| real_nfo_v1_thirty_checks_and_three_actual_paths | passed |
| real_nfo_v2_thirty_checks_and_three_actual_paths | passed |
| both_real_existing_three_samples_baseline_equal | passed |
| real_two_libraries_and_sixteen_files_removed | passed |
| real_credential_brokers_closed_own_nas_staging_removed | passed |
| real_nfo_comparator_static_linux_build | passed |
| old_two_project_worktrees_equal_current_turn_snapshots | passed |
| original_tmdb_key_absent_from_repository_and_outputs | passed |
| versioned_evidence_has_no_credential_fields | passed |
| output_report_structure | passed |

最终源码之后运行 make check（普通/race/无 cgo 测试、vet、依赖校验）和 make build linux-amd64 experiments 均成功；真实关联比较器另完成静态 Linux 构建。两轮真实 NFO 实验合计移除 2 库、16 文件，broker 已退出，本次唯一 NAS staging 程序与目录已移除。

历史“未配置 0 请求、退出 1”和 metadata-nfo 零文件拒绝仍是有效的失败路径验证；不会拿它们冒充本次真实官方成功。源码、报告与已纳入 Git 的证据均经过密钥/空白检查；原始凭据与私有种子未纳入。

## 兼容限制与后续入口

- 飞牛不透明 Trim ID 不能通过去前缀推断成 TMDB；真实 NFO 本轮标准 ID 直接回传未验证，应用需要独立保存类型化来源身份与文件映射。
- TV 根演员、头像和其他未覆盖 NFO 字段，其他版本、其他卷、站点完整属性/分页/促销以及下载器写操作均不由 M0 外推。
- 第二轮合成 E02→E01 失败保持可见，后续整理必须校验季集与归属，失败进入待确认。
- 正式运行应使用项目自己的 TMDB 凭据并遵守接口要求；本次复用仅是用户授权的验证来源，未将默认 key 硬编码到新项目。
- M0 全部通过后，可按原方案进入 M1：先实现一个站点、一类下载器、可预览/确认的手动搜索到整理入库闭环，再扩展其他里程碑。当前没有擅自开始 M1 或部署。

```sh
cd /Users/accidia/projects/light-media
make check
make build
make linux-amd64
make experiments
make offline
```

离线 real-nfo-compare 与真实 producer 证据分开保存。详细契约、清理、字段限制和复现步骤见 docs/m0-acceptance-matrix.md、docs/nfo-verification-plan.md、docs/m0-final-steps.md。
