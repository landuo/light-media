# RSS 候选上下文与完整 MetaInfo 的 Go 实施方案

本方案依据 2026-10-16 的只读审计，描述尚需实现的工作，不表示 RSS 整链、完整身份识别或全部插件已经完成。本文仅使用合成案例，不包含用户配置、账号、站点地址、真实媒体或下载任务。

现有 `internal/releasemeta` 已有两套独立的纯 Go 动漫引擎及资源属性消费投影。它服务于五个资源字段和下载历史 `season`，不能作为完整 `MetaBase`、`MetaInfo` 或 RSS `Context` 的替代品。完整标题对象、原版 RSS 缓存入口及每订阅有条件重建仍未实现。

**RSS 重建直接调用 `MetaInfo(title, description, custom_words)`，不调用 `MetaInfoPath`。** 路径入口应在完整标题对象之后单独实现、单独验收；它不是 RSS 分支的前置条件，也没有从完整实现目标中删除。

## 来源与现有证据的范围

主要依据实际安装版本，不能以本地旧仓库中较新的 `metainfo.py` 替代。以下 SHA 已记录在正式参考 `testdata/plugins/subscription-context-original-oracle.json`：

| 原安装文件 | SHA-256 | 关键方法 |
|---|---|---|
| `app/core/metainfo.py` | `7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9` | `MetaInfo`、`_build_meta_info`、`_find_metainfo_python`、`_meta_from_rust`、`MetaInfoPath` |
| `app/core/meta/metabase.py` | `0156c105bf92991332a8742b9a4af4e1f3a74cf4b6f08107e0ed6de9d546f292` | 属性、`init_subtitle`、`merge`、`to_dict` |
| `app/core/meta/words.py` | `798759c94919636c94b690786b9d68a8b7f2f2815be611b04f7c857491d40277` | `WordsMatcher.prepare` |
| `app/chain/subscribe.py` | `190f0500279b64f4ca2cd726f07dbb3e20ae9b7c46187268a208afcbe9c9b7ab` | `SubscribeChain.match`、`build_subscribe_meta`、身份和缺集判断 |
| `app/chain/torrents.py` | `6c2833fc9a87e9d57881fcdef760d131dd1d36acbc9d5de3d7279cdbd0d96a47` | `refresh`、`get_torrents`、`_ensure_context_compatibility`、`get_subscribe_cache_candidates` |
| `app/chain/search.py` | `2b04f6d246ea2573081cb72039a78acefb81cd7c895603e0433bab7dc7e7a528` | 搜索过滤、局部 MetaInfo 构建及 Context 组装 |
| `app/core/context.py` | `70a088bd54c748ab44ec467e6a1f1fc5bb5368f818bb16288eca0d15cf98877e` | `Context.to_dict`、`MediaInfo.clear/to_dict` |
| `app/helper/torrent.py` | `1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5` | `match_torrent`、`match_season_episodes`、`sort_torrents` |
| `app/modules/filter/__init__.py` | `78f7741b8782ed23a8ef94a18afc14038366b832d9a0907cce9274664052abe8` | `__match_size` |

Rust 对照固定为 MoviePilot-Rust 0.2.6、提交 `017b3a0e4ce2415d641c0eeb84ac907f9f10450c`。完整标题和路径包装的主要方法位于该提交 `parser.rs`：`build_meta_info`、`build_meta_path`、`find_explicit_metainfo`、`apply_explicit_metainfo`、`merge_meta`。动漫三方依赖及两个源模式的已完成切片证据见 [资源元数据移植说明](release-resource-metadata-go-port.md)。

现有 [订阅上下文参考说明](subscription-context-original-reference.md) 和 [离线参考目录](../testdata/plugins/subscription-context-reference/README.md) 保留 28 个 RSS 分支、14 个 options 案例。它们执行原 `WordsMatcher`、指定 RSS/Search AST 与 options 收集代码，但 **MetaInfo 和媒体识别是录参桩**。它们可以验证调用参数、分支、对象引用保留，不能证明真实标题解析、身份、名称、季集、序列化或 RSS 整链等价。后续真实参考必须保留这一边界说明，不能将旧样本改名为完整解析参考。

## 完整对象仍缺少什么

| 字段或行为 | 必要性 |
|---|---|
| `title`、`org_string`、`subtitle`、`apply_words` | 区分原始输入与识别字符串，决定 RSS 是否重建；成功应用标记不等于文字发生变化 |
| `cn_name`、`en_name`、`original_name`、`year` | 媒体识别、年份检查、标题及副标题兜底直接消费，单个 `Name` 不足 |
| 开始/结束/总季集、`season_list`、`episode_list` 等属性 | 多季拒绝、缺集交集、整季判断、每集大小分母和排序 |
| 专用身份、`media_source/media_id`、`episode_group` | 识别来源、标签优先级、剧集组及候选身份匹配 |
| `part`、平台、编码、位深、音频、帧率及资源属性 | 重建替换的是完整对象，后续 Context 和插件不能只获得五字段投影 |
| `name`、季集属性、`is_in_*`、`set_*`、`merge`、`to_dict` | 保留对象方法语义、零值、缺值、空集合及字段存在性 |

完整 MetaInfo 的配置输入还须区分全局配置快照、局部参数是否存在及局部原列表。仅保存已经替换全局的有效词表，无法重建 Python 的 `original_name` 分支。`None`、直接 `[]`、`[""]` 不能无条件合并。

`Context` 至少需要保留 `meta_info`、`media_info`、`torrent_info`、`media_recognize_fail_count`、`resource_source`、`match_source`、`candidate_recognized`、`media_info_is_target`、`allowed_episodes` 和 `confirmed_full_coverage`。其中允许集数必须保留 `None / 空集 / 非空集` 三态。

`MediaInfo` 的候选自身身份与订阅目标身份必须分开。通用来源/ID、各兼容 ID、类型、标题、原名、年份、季年份、季号、类别和剧集组等不能只由目标 TMDB ID 反推。`TorrentInfo` 要保留原始标题/描述、站点分类、IMDb ID、站点和下载信息、标签及优先级等输入；当前 `sites.Resource` 尚无原版站点分类字段。

## 必须保持的安装版差异

1. **原名的二次解析不同。** 安装版 Python 在 `apply_words` 非空时，不传局部参数再次执行 `_build_meta_info(raw title, subtitle)`，因此使用全局词；Rust `build_meta_info` 对应递归明确使用空词表。不能只从本次有效词表产生 `original_name`。
2. **专用身份字段与通用身份不同。** 安装版标签提取会收集 Bangumi/AniList，并建立通用来源/ID，但 `_build_meta_info`、`_meta_from_rust` 没有向对象赋值专用 `bangumiid/anilistid` 字段。不能按本地新版自动“补齐”；这会改变属性、`match_source` 和字典形状。订阅目标构造 `build_subscribe_meta` 则会显式赋这些字段，不能与候选构造混为一条路径。
3. **字典键存在性可观察。** `MetaBase.to_dict()` 使用 `vars(self)` 再补派生字段。legacy 未赋值的类默认字段可能不出现在实例字典；Rust wrapper 会显式注入较多 `None`。完整序列化不能统一填满所有空键。
4. **缓存类型校正只发生在对应阶段。** `TorrentsChain.refresh` 在全局 MetaInfo 后，以站点 TV 分类修正 `meta.type`。局部重建是整体替换，不能无依据把旧类型再 merge 到新对象。
5. **清理后的 MediaInfo 不是完整详情。** `MediaInfo.clear()` 清空 `names`、多个详情字典及列表；真实参考需要记录清理后的结果，不能为标题兜底保留源中已清除的别名。
6. **入口不同，拷贝不同。** 核心 `SubscribeChain.match` 使用浅拷贝；插件缓存读取 `get_subscribe_cache_candidates` 使用深拷贝并只做轻量候选筛选，后者不执行局部词重建。

完整普通视频身份解析还涉及安装版 `MetaVideo.__init_name/__fix_name/__is_pinyin`、`Tokens`、`StreamingPlatforms`、`StringUtils` 与 `Pinyin2Hanzi.is_pinyin`。Rust 对应 `parse_video/init_name/fix_video_name/is_pinyin_like` 使用 `golia_pinyin`。现有 Go 普通视频资源投影或保守的 `media.Parse` 不能代替这两套完整身份行为。

## RSS 缓存和每订阅重建顺序

1. `TorrentsChain.refresh` 使用原标题/描述做全局 MetaInfo，按站点分类校正类型，再识别媒体并 `clear()`。缓存完整 Context，并记录 rss/spider、候选识别状态、身份来源；识别失败初始次数为 1。
2. `get_torrents/_ensure_context_compatibility` 给旧缓存补实例缺失字段。原方法检查 `vars(context)`，不能因为读到类默认值就跳过回填。
3. `SubscribeChain.match` 先对尚未识别且失败次数小于 3 的缓存候选，以其缓存 MetaInfo 重试识别。成功更新缓存媒体及来源、重置次数；失败递增次数。
4. 每订阅浅拷贝 Context，先做站点范围判断。局部词表非空时，用 Python WordsMatcher 探测 **缓存 `org_string`**。
5. 仅探测的 `apply_words` 非空时，从 **原 TorrentInfo 的 title/description** 调用 MetaInfo。不能把探测结果交给解析器，也不能逐字段 merge 旧 MetaInfo。
6. 使用新 MetaInfo 重新识别，传订阅剧集组，成功时才替换副本 MediaInfo。失败时局部变量为空；即使副本暂时仍引用旧 MediaInfo，也必须进入真实标题兜底。
7. 标题兜底成功才用目标 MediaInfo 回填，并标记 `match_source=title`、`candidate_recognized=false`、`media_info_is_target=true`。随后继续身份/类型、季集、缺集、附加条件、优先级、洗版和排序判断。
8. 选中 Context 继续传给下载和事件。下载历史的季来自选中 `_meta.season`，不能改用订阅季号。

失败次数达到 3 仅禁止预识别，不禁止因局部词命中而进行的后续重新识别。局部识别成功也不能擅自照搬预识别的失败次数重置逻辑。

Search 是另一条入口：对原 TorrentInfo 先执行相应过滤，再直接用局部参数构造 MetaInfo，不做 RSS 的缓存探测。流式初步 append 的全局对象与最终 filtered replace 的局部对象也应分别验证。

## MetaInfoPath 的独立实施范围

固定来源为 Python `MetaInfoPath`、`app/core/meta/infopath.py::should_use_parent_title_for_file_stem/clear_parsed_title_for_parent_merge`、`MetaBase.merge`；Rust 对照 `build_meta_path`、同名辅助函数及 `merge_meta`。

- 使用同一配置，依次解析文件名、父目录名、祖父目录名，不访问真实文件内容。
- 仅在文件名完全由辅助中文标签构成、长度不超过 16、父目录含拉丁标题、没有明确身份等条件成立时，清空中文名、英文名和原名；不能清除资源或季集。
- 文件为 TV 或来源目录不是 TV 时才进行对应合并，避免非 TV 文件被 TV 目录污染。
- 按源字段和赋值顺序补空。Python 多处按真假值，Rust 多处仅按 `None`；`Some("")` 不能统一当成缺失。
- Python merge 会补 `episode_group`，固定 Rust `merge_meta` 不补。两者也不应自行新增所有专用身份字段的传播。
- `title/org_string/subtitle/apply_words/isfile/web_source` 不属于该 merge 的普通补空字段。

该路径入口完成后用于文件识别相关流程；RSS 标题重建不应为了复用它而引入虚构父目录。

## 新项目接入点与建议实施顺序

当前 `checkSubscription` 是主动搜索，`RSSSites` 是站点控制配置，不代表已实现原版 RSS 资源缓存。`cacheSearchResources` 保存资源、query、page、时间和 media_key，不是完整 RSS Context。当前下载快照只冻结五资源字段、season 和有效 options。

| 步骤 | 独立可验收产物 | Go 接入位置 |
|---|---|---|
| 1 | 真实 MetaInfo/MetaBase/MediaInfo/Context 参考；外部媒体识别和下载明确作为合成边界 | 先锁定实际输入、全部字段、属性及序列化；不改旧 28/14 参考的证据范围 |
| 2 | 完整 MetaBase 表示、配置参数存在性和方法 | 在完整对象层复用 `releasemeta` 两个动漫引擎；保留消费投影 API |
| 3 | 两种模式的完整 MetaInfo 标题入口、普通视频身份和显式标签 | `recognition_scope.go`、身份/季集匹配边界；不能继续用 `media.Parse` 宣称原对象等价 |
| 4 | 独立 MetaInfoPath 三层解析和 merge | 文件识别边界；与 RSS 分支分别验收 |
| 5 | 全局解析的 RSS/spider 缓存 Context、分类校正、身份来源、失败次数和旧字段兼容 | 新增实际缓存生产/读取入口，不能复用仅有 SearchOutput 的查询缓存 |
| 6 | 每订阅浅拷贝、Python 探测、有条件整体重建、重新识别/标题兜底状态机 | `subscriptionRecognitionContext` 与 `checkSubscription` 附近新增独立 RSS 匹配入口；不把主动 Search 伪装为 RSS |
| 7 | Search 与 RSS 分别保留选中完整对象；过滤和排序保持来源差异 | `evaluateResourceContext`、`priorityEpisodeCountContext`、搜索结果和候选排序 |
| 8 | 完整对象贯穿确认、重试、Add、历史和 DownloadAdded | `prepareDownloadContext`、预览/Intent/最终下载及既有加密身份绑定、事务和恢复边界 |

size_range 每集大小分母仍独立使用全局 MetaInfo；排序使用选中 Context 的季集。有效词表配置来源修正并不意味着当前 `media.Parse` 分母已经与原版等价。

完成步骤 8 时，下载端应保存实际选中对象及来源，不临近 Add 重新解析一份少量属性。RSS 未触发重建时必须保留缓存元数据，而非用后来改变的全局配置覆盖它。已有 hash、恢复收据、重启与重试都要断言此点。

## 最有判别力的真实参考和验收案例

以下全部使用合成标题、合成配置和合成媒体识别结果。实际 MetaInfo、MetaBase、MediaInfo、Context 和对应原分支代码必须执行；只记录参数的 MetaInfo 桩不能满足验收。

| 案例 | 必须区分的结果 |
|---|---|
| 全局 `Raw => Cached`，局部 `Raw => Local` | RSS 探测不命中，保留缓存完整 MetaInfo；Search 在原标题可命中 |
| 同一缓存，局部 `Cached => Local` | RSS 探测命中，但重建用原 Raw 标题，不能直接得到探测生成的 Local 元数据 |
| 局部 `Cached => Cached` | 即使字符串不变也重建；比较 `apply_words`、真实对象和原名 |
| 替换成功但组合偏移失败；再增加一条前序成功规则 | 前者可能无应用标记而不重建，后者仍重建；不能用字符串变化替代 |
| `None/[]/[""]/空白/注释/无效表达式`，两种模式 | 同时比较 options 来源、缓存对象是否保留、实际 `original_name` 和异常行为 |
| 带 `{[tmdbid=101;type=tv;g=abcdef;s=4;e=1-3]}`，再带 `[tmdbid=202]` | 标签优先级、剥离后的 org_string、剧集组、总数/集合和专用/通用身份 |
| Bangumi/AniList 标签与多种身份并存 | 安装版专用字段、通用身份优先级、match_source 和 to_dict 键存在性 |
| 缓存失败次数为 3，局部词成功应用 | 预识别跳过，但局部重建与识别仍可执行 |
| 缓存旧 MediaInfo 有 ID，局部重识别失败 | 标题兜底失败必须拒绝；成功则回填目标并设置 title/false/true，不能沿用旧候选身份 |
| 站点 TV 分类修正 UNKNOWN，之后发生局部重建 | 分别断言缓存对象和新对象的类型，禁止无依据 merge |
| `allowed_episodes` 三态、两个订阅消费同一缓存 | 保留三态，断言原缓存与局部替换/引用关系，不能只比较资源列表 |
| 全局解析 3 集、局部解析 2 集 | 大小过滤用全局分母，排序和选中对象用局部季集 |
| 无集号/多季/季零/明确结束季/倒序与缺集交集 | 比较原属性及决策，不以目标订阅范围覆盖候选自身范围 |
| 合成路径 `Example.Movie.2024/简体.mkv`，再换纯中文父目录和显式身份文件 | 辅助标签清理条件、三层 merge、身份保护、Python/Rust 剧集组合并差异 |
| 选择后修改全局词、模式、制作组及占位符，再重启/重试 | 选中完整对象、历史和 DownloadAdded 保持，不重新解释已接受下载 |

真实 RSS 参考应在“缓存初始、预识别后、局部副本、重建后、兜底后、最终接受、原缓存最终状态”分别捕获完整字段及识别调用。先明确哪些分支改变缓存、哪些只改变副本，再进行 Go 比较。

本方案不把有限样本通过扩大为全部输入已证明等价。完整 RSS 验收还需接通真实缓存生产、身份服务、过滤、下载及恢复；旧 28/14 控制流参考、单标题资源投影或已有主动搜索流程均不能独立替代该验收。
