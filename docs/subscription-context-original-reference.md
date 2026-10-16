# 原版订阅识别上下文行为参考

本参考于 2026-10-16 只读提取原 MoviePilot 容器中的 10 个指定 Python 源文件。没有导入应用、读取用户配置或操作媒体/下载任务。Search、RSS、WordsMatcher、Context、Filter 与本地 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 对应文件一致；MetaInfo 以实际安装源 SHA `7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9` 为准，不能直接以本地较新文件代替。

`testdata/plugins/subscription-context-original-oracle.json` 保存 28 个 RSS 分支、14 个输入列表/源模式配置案例及优先级独立调用参考。生成器直接执行原版 WordsMatcher、Context、RSS/Search 指定 AST 和优先级 size_range AST；**MetaInfo 与媒体识别使用合成录参桩，仅验证调用参数、控制流和对象保留，不是完整 MetaInfo、真实媒体识别或 RSS 整链等价证明。** Python 仅用于离线参考，不进入 Go 产品运行时。全部文件 SHA 与复现步骤见参考目录。

## 搜索和 RSS 的分界

| 阶段 | 原版实际行为 | 来源 |
| --- | --- | --- |
| 订阅文字转列表 | 非空原文 `split("\n")`，否则 `None`；没有 trim、合并或删除空行 | `chain/subscribe.py:1355,1847` |
| Search 过滤 | 先对原 TorrentInfo 做附加筛选和优先级过滤，再构造局部 MetaInfo | `chain/search.py:1139–1148,1211,1242` |
| Search 匹配 | 每个候选直接 `MetaInfo(raw title, description, custom_words)`，无需预探测是否应用词 | `chain/search.py:1242` |
| Search Context | 保存该完整 MetaInfo、原 TorrentInfo、目标 MediaInfo 和匹配来源等字段，然后用 Context 排序 | `chain/search.py:1275–1295` |
| 流式 Search 的 append | 初步候选 `MetaInfo(raw title, description)` 使用全局；最终 filtered replace 才走上述局部解析 | `chain/search.py:1542–1557,1573–1587` |
| RSS/爬虫缓存 | 最初 `MetaInfo(raw title, description)` 使用全局；保存完整 Context，站点 TV 类别还可能校正 meta.type | `chain/torrents.py:597–628` |
| RSS 预识别 | 未识别候选且失败次数低于 3，使用缓存的完整 MetaInfo 重试媒体识别；不是按局部词先重建 | `chain/subscribe.py:1741–1782` |
| RSS 局部匹配 | 对原 Context 做浅拷贝；用 Python WordsMatcher 探测其 `meta_info.org_string`；仅 `apply_words` 非空才重建 | `chain/subscribe.py:1867–1890` |
| RSS 重建输入 | 重新调用 `MetaInfo(raw torrent.title, raw description, local words)`，不是探测结果，也不是缓存 org_string | `chain/subscribe.py:1881–1888` |
| 下载 | 使用选中 Context 的完整 `_meta`；另把订阅识别词原文保存到下载历史。DownloadAdded 发送该 Context | `chain/subscribe.py:772,785`; `chain/download.py:817,967,1020` |

`org_string` 是识别用字符串，不等于 `title`。它已经经过识别词、显式媒体标签处理、已配置文件后缀处理和解析构造时的 strip。RSS 探测不能简单改为对原始标题匹配，也不能把探测得到的文字直接交给解析器，否则偏移可能重复应用。

## RSS 重建矩阵

下面案例使用原始标题 `Raw.S01E01.1080p.WEB-DL`，全局规则 `Raw => Cached`，因此缓存 `org_string` 为 `Cached.S01E01.1080p.WEB-DL`。结果指局部重建块执行结束时，后续仍可能匹配失败、回填目标身份或修改标记。

| 局部列表 | RSS 探测 apply_words | 重建？ | Search 与 RSS 的差异 |
| --- | --- | --- | --- |
| `None` | 不探测 | 否 | Search 仍新建 MetaInfo，使用全局 |
| 直接 `[]` | 不探测 | 否 | Search 仍新建；其 Rust/legacy 词表含义见下一节 |
| `[""]`、`["", ""]` | 空 | 否 | RSS 留住缓存的全局结果；Search 用非空局部列表抑制全局 |
| `[" "]`、仅注释 | 空 | 否 | 同上，局部列表存在不等于需要重建 |
| `["["]` 无效正则 | 空 | 否 | RSS 留住原 MetaInfo；Search 仍调用新 MetaInfo |
| 未命中规则 | 空 | 否 | 同上 |
| `Raw => Local` | 空（缓存已没有 Raw） | 否 | Search 在原 Raw 标题可命中；RSS 保留 Cached 元数据 |
| `Cached => Local` | 非空 | 是 | 探测命中，但重建使用原 Raw 标题，该局部规则可能不再命中 |
| `Cached => Cached` | 非空 | 是 | 即使文字没有变化也重建；条件是成功应用标记，不是字符串是否变化 |
| `Cached => Local && MISSING <> E >> EP+1` | 空，尽管探测文字被替换 | 否 | 组合规则只在偏移也成功时记为应用；不能以“文字变了”代替判断 |
| 先有成功规则，再有上述失败组合 | 至少包含先前成功规则 | 是 | 不因后一个组合失败而清空先前应用标记 |
| `E <> .1080p >> EP+1` | 非空 | 是 | 探测用缓存字符串，重建重新从原标题应用一次 |

正常订阅文本无法得到直接 `[]`：空串变 `None`，只有一个换行的原文会得到两个空字符串。不能把内部接口的 `[]` 与订阅空串混为同一状态。

## source mode 的独立差异

| custom_words 参数 | Rust 选项收集 | Python legacy WordsMatcher |
| --- | --- | --- |
| `None` | `_rust_default_parse_options()`，全局识别词及其他默认配置 | `custom_words or global`，全局 |
| `[]` | 复制默认配置，再明确覆盖为空列表；仅词表清空，其他选项保留 | 空列表为 false，回退全局 |
| `[""]` / 空白 / 注释 / 无效 / 未命中列表 | 非空列表替代全局；实际是否生效由 Rust 规则处理决定 | 非空列表替代全局；空白/无效词等可能全部不生效 |

来源：安装版 `core/metainfo.py:297–341`；`core/meta/words.py:102`。Rust 自定义选项先复制默认字典再覆盖，不会把局部列表写回默认配置。

RSS 的预探测始终使用 Python WordsMatcher，即使实际 MetaInfo 优先走 Rust。Rust 未启用/不可用、解析异常或扩展 ID 能力不足时，MetaInfo 可能退回 Python；不能将预探测的 Python 处理结果当成实际 Rust 元数据。模式差异需要分别验证。

完整 MetaInfo 还有 `original_name` 差异：安装版 Python 在 apply_words 非空时不带局部参数重跑 `_build_meta_info(raw title, subtitle)`，会采用全局词；Rust 源 `build_meta_info` 的对应递归明确传空词表。现有五项资源属性和季范围不能代表这一字段已经移植。来源：安装版 `core/metainfo.py:438–443`，固定 Rust 源 `parser.rs:45–61`。

## Context 保留与替换

RSS `copy.copy(context)` 保留全部字段和字段引用。未触发重建时，原 MetaInfo 对象及其全部字段保持，不能投影成五个资源字段后重新推导。触发重建后，`_context.meta_info` 整体替换为新 MetaInfo，不与旧对象逐字段 merge；原缓存 Context 的 meta 引用保持。

重识别返回有效 MediaInfo 时才替换 `_context.media_info`，并更新 match_source、candidate_recognized、media_info_is_target。返回空值时局部变量会变空，后续标题回退可能设置目标 MediaInfo；不能把局部重建块刚结束时仍留有旧 media 引用理解为最终候选通过。其后的身份、季集、缺集、附加筛选和优先级过滤仍可拒绝该候选。

其他 Context 字段包括 resource_source、media_recognize_fail_count、allowed_episodes（None/空集合/非空集合三态）和 confirmed_full_coverage。浅拷贝还保留嵌套集合引用；Go 可以用自己的存储/并发表示，但不能丢掉三态或把显式空集合解释成无限制。来源：`core/context.py:1138–1179`。

## 优先级过滤与排序应使用不同识别范围

原版 `FilterModule.__match_size` 调用 `MetaInfo(torrent.title, torrent.description)`，不传订阅词表；Rust 分支也明确传 `_rust_parse_options()`，无局部参数。因此 size_range 每集大小的分母使用全局识别，附加/正则条件继续使用原 TorrentInfo。排序则直接使用选中 Context.meta_info 的 season_list、episode_list，保留局部识别结果。来源：`modules/filter/__init__.py:164–170,475–478`；`helper/torrent.py:301,315–321`。

这次最小产品修正仅把 `priorityFilter.apply` 的 EpisodeResolver 恢复到全局 `priorityEpisodeCount`；`prioritySorter` 继续使用局部范围。没有把整个过滤器切到局部，也没有改写原始规则输入。当前核心分母解析仍是既有 Go media.Parse，**只修正配置来源，尚不等于完整原 MetaInfo 分母解析等价**。

判别回归保留两类证据：一是同一资源在全局得到 3 集、局部得到 2 集，4 GiB 的 `>1500 MiB` 只允许正确使用全局分母时拒绝它，同时另有两集正例通过；二是真实订阅检查的 1.5 GiB 单集候选，局部保留单集，全局改写为 4 集，`<500 MiB` 应通过。错误复用局部 Context 会使实际检查无法产生两个 Intent。排序和整季包仍单独断言局部行为。

## 后续 Go 接入点及必须补的真实参考

1. 先完成独立的完整 MetaInfo/MetaInfoPath Go 等价表示，包括 raw title、org_string、apply_words、原名称、身份标签、季集、资源属性及原对象方法语义。现有 `media.Parse` 不能冒充这一对象。
2. Search 在当前 `search`/`searchSites` 的候选匹配边界保存完整已选 MetaInfo，排序消费该对象；优先级 size_range 保持独立全局调用。流式 append 与最终 replace 需要各自参考，不能直接共用局部解析输出。
3. RSS 需要独立“全局解析的资源缓存 → 预识别 → 每订阅局部浅拷贝/有条件重建 → 身份/季集/规则过滤”入口。当前 `checkSubscription` 是主动搜索，没有等价的缓存 RSS 入口；不能把 `subscriptionRecognitionContext` 套到所有 RSS 候选就视作完成。
4. `prepareDownloadContext`/确认/最终 Add/DownloadAdded 后续应承载选中完整对象，而非临近 Add 重建少量字段；保留现有加密身份绑定和事务边界。重启、已有 hash 和恢复收据须测试选择字段未被新全局覆盖。
5. 将控制流录参桩替换为安装版真实 MetaInfo 与合成 MediaChain 的离线参考：至少覆盖两种 source mode、None/[]/空白/无效词、全局已改标题、同值替换、组合偏移失败、重识别成功/失败及标题回退、allowed_episodes 三态、完整元数据/Context 字段保留、原缓存跨订阅隔离。单个标题解析 oracle 或当前 42 个控制流案例不足以证明 RSS 这一整链。

本参考没有实现 RSS、替换核心身份解析、迁移用户数据或部署服务。
