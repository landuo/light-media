# light-media · M0 首轮技术验证

## 结论与 M0 门槛

独立项目已经建立，首轮 M0 实验已完成：Go/SQLite、双下载器只读协议、五站有限鉴权/搜索及种子材料、真实 NAS 与容器硬链接、飞牛新 NFO 核心字段和旁车图片读取均有代码及实测证据，技术路线可行。M0 尚未全部验收，不自动进入 M1。标准 TMDB / 单集来源 ID 的权威元数据绑定仍需自用凭据验证；剧级演员语义和完整 NFO 字段不能从本轮结果推定。

五站当前是有限协议探针；完整资源属性、促销、筛选、分页与稳定原生适配仍需后续实现。下载器添加/校验/迁移等写操作没有拿现有种子做实验。

## 独立项目与验证范围

| 项目 | 结果 |
| --- | --- |
| 项目目录 | /Users/accidia/projects/light-media |
| 需求基线 | docs/design-v1.4.txt / .html |
| 语言与工具链 | Go 1.26.2；不引入 Python 运行依赖 |
| 项目内容 | 命名/集号映射/NFO实验、路径实验、SQLite双驱动基线、只读下载器/站点/飞牛探针、有界日志、插件v0.1契约草案 |
| 旧项目 | MoviePilot 与 moviepilot-v2 均未写入；本轮前后 Git 工作树状态一致 |
| NAS 实验 | 临时测试文件和一次性容器；另创建并清理独立 UUID 合成媒体测试库；不改下载器、飞牛全局设置或既有库配置，不改原视频 |
| 凭据 | 现有授权配置只读获取；内存与 stdin 传递；原始配置、Cookie、Token、种子材料不进入 Git/报告 |

## 真实接口与路径

| 目标 | 实测结果 | 界限 |
| --- | --- | --- |
| qBittorrent | ok · v5.1.2 · 6 请求 | 单种文件 1；仅实际读能力已验证，写能力为 false |
| Transmission | ok · 4.1.3 (838877323f) · 5 请求 | 单种文件 22；仅实际读能力已验证，写能力为 false |
| 馒头 | ok · mteam_api_candidate · 2 请求 | 首页/鉴权与一页搜索；10 个候选结构，完整适配未验收 |
| 憨憨 | ok · hhanclub_html_candidate · 2 请求 | 首页/鉴权与一页搜索；100 个候选结构，完整适配未验收 |
| UBits | ok · nexusphp_candidate · 2 请求 | 首页/鉴权与一页搜索；100 个候选结构，完整适配未验收 |
| 家园 | ok · nexusphp_candidate · 2 请求 | 首页/鉴权与一页搜索；10 个候选结构，完整适配未验收 |
| 1PTBA | ok · nexusphp_candidate · 2 请求 | 首页/鉴权与一页搜索；25 个候选结构，完整适配未验收 |

| 飞牛/目录 | 实测结果 |
| --- | --- |
| 飞牛影视 | 前端 0.9.8，后端 0.8.43；账号认证及管理员只读能力通过 |
| 媒体库 | 7 个既有库；只读返回目录并查询三个代表作品；新 NFO 另用独立临时库实验 |
| 家园身份 | 现有配置中名称匹配家园，实际地址 hdhome.org；以本次已配置目标为依据 |
| 路径/硬链接 | /vol2/1000/media/download → /vol2/1000/media/国漫：实际 link、SameFile、目标改名/解除链接后源内容保持、测试文件清理全部通过 |
| 路径限制 | 另在整个父目录 bind 的一次性容器内验证上述路径与 Transmission 实际默认下载子目录；生产应用仍未部署，qB 另一 /downloads 路径未测试 |

## 命名、集号与 NFO

| 真实文件名 | 语法解析 |
| --- | --- |
| 夜王 (2026) - 1080p.mkv | 夜王 · 年份 2026 |
| 诛仙 - S04E05 - 第 5 集.mkv | 诛仙 · S4 E5 |
| 生活大爆炸 - S07E17 - 第 17 集.mkv | 生活大爆炸 · S7 E17 |

| 飞牛查询 | 类型 | 来源 ID | 结果 |
| --- | --- | --- | --- |
| 夜王 | Movie | tm1602727 | ok |
| 生活大爆炸 | TV | tt1418 | ok |
| 诛仙 | TV | tt206484 | ok |

真实文件名夹具故意不伪造身份或已知目标集，因此单独运行时剧集映射进入 needs_review。飞牛查询另提供作品身份佐证；标准分集元数据与文件的自动绑定仍未验收；新 NFO 的合成字段读取另见下节。

既有 NFO 只读样本：生活大爆炸，根 tvshow，tmdb=1418，tvdb=80379，imdb=tt0898266，actor 节点 7。TMDB ID 与飞牛查询一致。新 XML 的中文、转义、actor 和单集 ID 边界通过合成测试；新 NFO 的真实服务导入已另用隔离合成样本核对，详见下节；标准 provider ID 未用合成值冒充验收。

## 资源与 SQLite 基线

| 测量条件/指标 | 结果 |
| --- | --- |
| NAS | Debian 12 / x86_64；4 个逻辑 CPU；MemTotal 8,053,360 KiB |
| 受限实验 | 现有 alpine:3.22 一次性容器；无网络、只读根文件系统；1 CPU / 512 MiB；GOMEMLIMIT=256MiB |
| 数据与介质 | 200订阅、5000元数据、50000文件；新建 /vol2 ext4 目录绑定 /data，合成数据，实验后清理 |
| 实际 SQLite | 3.53.4 |
| 实际驱动 | modernc v1.60.1 |
| 子进程最大 RSS | 25.688 MiB（wait4 最大值，非进程树求和） |
| 测试容器 cgroup 峰值 | 50.293 MiB（包含相关进程/页缓存等容器记账） |
| 总耗时 | 31.057 秒 |
| 分页读 p95 | 0.839 ms |
| 数据库检查 | claim_and_job_transaction_rollback: passed；foreign_key_enforcement: passed；reopen_and_expired_job_lease_recovery: passed；source_integrity: passed；ungraceful_process_exit_recovery: passed；unique_reservation_with_contention: passed；vacuum_into_snapshot_integrity_and_isolation: passed |

本机同场景三轮 cgo 对照更快且 RSS 较低。初选 modernc 是为了静态部署和交叉编译便利，不能声称它比 cgo 更省内存。以上是一次合成数据库实验，不是完整应用的空闲 RSS、峰值或 72 小时稳定性验收；/tmp 首轮与 ext4 受限轮的条件不同，不直接用耗时比较语言优劣。

## 三方路径核对

| 角色 | 真实配置或实测布局 | 限制 |
| --- | --- | --- |
| qB | 容器 /media/download → 宿主 /vol2/1000/media/download | Mounts 确认；不是默认 save_path 验证；另一 /downloads 挂载未测试 |
| Transmission | 原生默认 /vol2/1000/media/download/电视剧 | 路径字段只读核对；未启用 incomplete-dir |
| 飞牛 | 国漫库 /vol2/1000/media/国漫/ | 既有库只读目录 |
| 建议应用 | 整个宿主 /vol2/1000/media → 容器 /media | 已有 alpine:3.22 一次性容器、UID1000/GID1001、无网络/只读根目录；不是生产应用已部署 |
| 实际两组探针 | /media/download 与 /media/download/电视剧 → /media/国漫 | 两组实际link、SameFile、改名/解除链接后源保持及清理全部通过；不推广到其他卷/路径 |

完整路径与逐实例映射限制见 docs/path-baseline.md。硬链接共享内容，禁止原地改写共享视频。

## 新 NFO 与图片隔离实测

| 项目 | 观察与界限 |
| --- | --- |
| 隔离范围 | 新 UUID 目录和 Mix 测试库；三段新生成的1秒视频、5份NFO、6张JPEG；不复用真实媒体或来源ID |
| 读取结果 | 7个节点、23次请求：1电影、2TV、2Season、2Episode；中文及 <验证> & 正确还原；S01E02、S02E01与2024-01-02单集日期正确 |
| 年份 | 输入year=2024，API以release_date=2024-01-01返回；并非独立year字段或精确首播日读回 |
| 演员 | 电影、Season、Episode均返回两位合成Actor的name/role；order 0/1被规范化为1/2；TV根条目的persons为空 |
| 来源ID | 自定义lightmedia-m0 uniqueid没有映射为trim_id；本轮不拿合成ID冒充真实provider ID |
| 图片 | 14次请求含6次本源图片GET；6张64×64 JPEG的SHA-256匹配对应Movie/Series/Animation本地样本。每组poster/fanart同字节，不能排除组内两图互换 |
| 清理 | 临时库 cleaned；既有7库GUID/名称/类别/目录基线保持；14个自建文件已清理并确认目录消失；NAS临时二进制已清理，凭据broker关闭 |
| 未覆盖 | 真实provider ID写入、完整剧级演员/头像、所有NFO字段、全量正式条目元数据一致性、共享演员/图片缓存的完全回收 |

详细复现与兼容结论见 docs/nfo-verification-plan.md；实验源码位于 experiments/fnos-nfo 和 experiments/nfo-fixtures。标准元数据探针结果：unverified。

## 种子材料验证

每站最多一页搜索、一份种子材料；五站均已通过一份材料的结构检查。原始字节仅留在进程内，不提交下载器，不保存含 tracker/passkey 的原始文件。

| 目标 | 状态 | 材料检查 |
| --- | --- | --- |
| 馒头 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1581113835,"payload_verified":false} |
| 憨憨 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":6,"total_bytes":49064060667,"payload_verified":false} |
| UBits | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1565188927,"payload_verified":false} |
| 家园 | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":446872198,"payload_verified":false} |
| 1PTBA | ok | {"info_dictionary_valid":true,"raw_info_hash_computed":true,"v1":true,"v2":false,"piece_layers_checked":false,"file_count":1,"total_bytes":1581113835,"payload_verified":false} |

## 可靠性、日志与插件草案

- SQLite：唯一占位冲突、占位与 job 同事务回滚、外键、异常进程退出后的已提交保留/未提交回滚、重开租约恢复、VACUUM INTO 快照一致性/隔离与源完整性通过。
- 协议：鉴权失败/挑战/限流/模板变化区别于空结果；禁止跨源重定向；输入、响应、请求次数与超时有界；不回显敏感服务错误或秘密。
- 命名：中文显示集号与 SxxExx 不一致进入待确认；原始编号与标准编号分开；范围外/多映射冲突/未知目标集拒绝自动猜测；不改写源文件。
- 日志：slog、关联 ID、字段白名单、已知秘密和 URL 脱敏、JSONL 防换行注入、有界输出及失败/丢弃统计；轮转/网页日志中心留待应用阶段。
- 插件：v0.1 事件封套/幂等/能力/控制边界草案，未运行 P1 插件，不宣称权限执行或事件持久化已经实现。

## 最终检查

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

完整源码测试、race、无 cgo 构建测试、vet 与依赖校验通过；Mac arm64 和静态 Linux amd64 CLI 构建通过。TMDB 未配置路径实跑为 unverified、0 请求、退出码 1，未被计为真实接口通过。

## 复现与下一步

```sh
cd /Users/accidia/projects/light-media
make check
make build
make linux-amd64
make offline
```

实机运行需按各契约从 stdin 提供目标配置。不要把凭据放进命令行、Git 或普通配置，不关闭 TLS 校验。项目报告见 docs/m0-report.md，受限实验及驱动选择见 docs/sqlite-baseline.md。

- 补齐自用 TMDB 配置，复核电影及普通剧/国漫标准单集身份；确认剧级演员聚合策略及未覆盖 NFO 字段。隔离导入与图片实测已完成，边界在 docs/nfo-verification-plan.md。
- 将有限站点协议结构扩展为五站真实资源属性/促销/下载材料契约；M0 仅为接口可行性证据。
- 后续以一个站点、一类下载器先实现 M1 手动闭环，再按既定里程碑扩展；M0 未通过的门槛须明确关闭或修订后再推进。
