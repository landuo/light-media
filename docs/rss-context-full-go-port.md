# RSS cachedContext 完整对象纯包（应用链路未接入）

正式纯包为 `internal/rsscontext`，使用 `internal/releasemeta` 完整 MetaInfo 入口，不调用 Python、不导入 App。原独立候选及其验证保持保留；本次仅移入纯包，尚未替换 App/RSS/SubscribeGroup 正式消费者，不能称为已经上线的完整 RSS 功能。

## 已实现边界

- 真实 `MetaObject` 的原标题/副标题构造、全局设置冻结；缓存站点分类仅在 producer 阶段校正类型。
- `MediaInfo` 65 个默认实例字段、`TorrentInfo` 25 个默认实例字段；所消费的 `title_year` / `detail_link` / `to_dict` / `clear` 以及类型枚举、原字符串区分。引用 `internal/rsscontext/schema.json` / `testdata/plugins/rss-context-full/reference/installed-schema.json` 的实际安装版本，非旧仓库推断。
- 原 producer 的空媒体对象、失败计数、来源/身份标记；旧 Context 兼容补字段按实例 membership 判断。
- preflight 在缓存原 MetaObject 上重识别，成功即使返回空 MediaInfo 也 Clear 并重置计数；3 次上限；保留 `<3` / `elif >=3` 对无效类型的原异常边界。
- 订阅目标真实构造（year、季、四 ID、generic identity 显式覆盖），识别参数保留 null presence。媒体库检查先于目标 Clear，接口不隐藏这次状态改变。
- core 容器浅 copy；局部 Python WordsMatcher 在**缓存 org_string** probe；仅 apply_words 非空才从**原始 torrent title/description**完整重建；该重建不重复应用 producer 站点分类校正。成功、nil 和空对象分别处理，nil 时使用局部变量回退，不能误用浅 copy 中尚存的旧 media。
- 原 `TorrentHelper.match_torrent` 身份标签/类型/年份/中英文/别名/非英语标题与副标题 fragment 逻辑。
- core 精确 type、tmdb/douban 原值比较；TV 单季、显式 begin season presence、缺失集交集。Bangumi/AniList/generic identity 没有擅自增加 core 阻断。
- 插件读取独立 deepcopy、四 ID 和 generic identity 归一比较、显式季范围、仅无身份候选的诊断标题匹配。插件分支不应用订阅局部识别词。
- 版本化状态 envelope、完整 MetaObject 原状态、原 torrent title/description、枚举状态、稀疏 controls、缓存全局 frame、选择全局 frame、局部 None/[]、原订阅字段与有效 sites/domains。反序列化不重跑解析，不用新配置解释旧缓存。

`IdentityCandidate` 返回 `Reason=identity_candidate` 时仅表示已通过这段原身份/季门槛，`NextStage=subscription_rules_and_best_version` **不是允许下载**。

## 真执行参考与验证

`testdata/plugins/rss-context-full/reference/installed_rss_context.py` 由 `testdata/plugins/rss-context-full/reference/prepare_oracle.py` 生成。只读 NAS 已安装源码/包，校验固定 SHA；不 import app，实际完整 MetaInfo/MetaBase、两套动漫引擎、WordsMatcher、Context、MediaInfo、TorrentInfo 和原工具方法均执行。只有网络识别替换成返回真实 MediaInfo 的受控合成接口；没有录制 MetaInfo stub、真实账号、站点数据或网络元数据请求。

producer、preflight、core prefix 是按固定已校验源行选择的原 AST 语句；core 到附加规则前为止，TV 洗版分支明确留给 continuation。插件 candidate/helper 方法为原 AST。输入、每阶段完整对象、原调用 kwargs、真假识别、最终 cache、core/plugin alias 身份均作对照。

110 个案例为 55 个判别场景 × Rust/legacy：108 个正常完成，2 个保留原 TypeError（计数为 None 即使候选已识别，仍进入 `elif counter>=3` 校验）。插件诊断标题候选原 `MediaInfo(type=subscribe.type字符串)` 的 `to_dict` 会抛 AttributeError；参考单独保存该对象字段及类型状态，Go 保留相同错误，不把原 bug 伪装成正常成功。

`testdata/plugins/rss-context-full/reference/installed-oracle.ndjson.gz` 流式逐 case 读取；原执行完整 JSON 可由保留的只读离线脚本重建，正式目录仅保存流式 gzip 与全部输入，原 JSON SHA 保留在 provenance。`testdata/plugins/rss-context-full/reference/provenance.json` 含 Python、依赖版本、Unicode 版本、源码 SHA、夹具 SHA。Python15 casefold 表由真实运行时产生；Upper 表复制自冻结正式 legacyanime 的完整 Python15 映射，文件头标明 SHA。清理符号、空白（含 U+001C–001F）和 byte.isalpha 分支按源保留。

额外状态/并发测试验证全局 nil/[] 与 platform nil/{}、局部 None/[]、缺失字段、状态独立恢复、浅 copy 的共享字段、deepcopy 嵌套隔离及多个候选并发选择。

Go 目标为 `go test ./internal/rsscontext`、`go test -race ./internal/rsscontext`、`go vet ./internal/rsscontext`。普通与 race 对 110 条原执行参考流式逐条验证。

## 仍需实现/由宿主提供

1. 本模块未移植 MediaInfo 原始 provider detail 正规化：`set_tmdb_info`、`set_douban_info`、`set_bangumi_info`、`set_anilist_info`（及各自派生辅助）。非空 `*_info` 构造输入返回 `provider_detail_normalizer_required`。宿主 catalog/recognizer 必须先提供**已经正规化**的真实字段，不能只返回一个 ID。未实现的 UI/message 图片、星级、overview 辅助也不伪称完整 MediaInfo。
2. 非空 torrent freedate 的原 `diff_time_str` 需要明确时钟/时区适配，当前 ToDict 返回 `freedate_clock_adapter_required`。该错误不阻止保留原字段并 EncodeState；不能把非空免费期悄悄展示为空。`pub_minutes` 尚未实现，匹配本前缀未调用。
3. 网络识别、站点/域名读取、默认 RSS 站点交集、库事实/订阅完成状态由宿主适配。Prepared.Domains / Sites 必须是原源解析规则得到的结果；不能把未解析的配置当作全部允许。共享缓存 preflight 需由宿主串行/加锁；并发测试仅独立选择/克隆，不声称同一可变缓存可并发写。
4. `check_and_handle_existing_media`、best-version 全集/范围覆盖与优先级、filter_torrent 附加参数、filter_torrents 规则、sorting、download、事实事务、cache 容量/周期/去重仍未接入。`IdentityCandidate` 为明确 continuation 边界；这些未完成不能算作完整 RSS 或可下载成功。
5. 全局/局部任意 regex 的完整 CPython re / Rust fancy-regex 方言仍是正式 matcher 已有边界。110 例证明已录制行为，不能证明所有用户表达式；固定词法 Unicode 表不等于通用 `internal/pyregex.Compile` 具有完整 Python Unicode/flags 语义。超时返回 `rss_words_probe_limit`，不会把限额当作“无应用词”。
6. 本纯包在 JSON-shaped 正规化字段上工作。它不实现任意 Python 对象 pickle、任意自定义对象 `__str__`、datetime/自定义映射键或 provider 实例类型；相关适配必须在宿主边界明确转换/拒绝。未迁移 `MetaInfoPath`，RSS 标题入口不要求先完成它。

源代码来源为 MoviePilot GPL-3.0，完整许可证见 `internal/rsscontext/MOVIEPILOT-GPL-3.0.txt`；原来源/安装源码固定 SHA 见 `testdata/plugins/rss-context-full/source/manifest.json` 和参考 provenance。Go 翻译保留对应函数来源，不是重新定义业务规则。Unicode 数据许可保留在 `internal/rsscontext/UNICODE-LICENSE.txt`。

## 接入接口与顺序

纯包与全量原执行参考已经移入正式目录，后续仍需独立接入宿主 producer/reader 和真实消费者；纯包落地不等于消费者已经替换。

```go
NewEngine(Frame{Revision, Global: releasemeta.MetaInfoEnvironment{...}}, Recognizer) (*Engine,error)
Produce(ctx, *TorrentInfo, stype) (*Context,error) // nil,nil 表示 enclosure 缺失
Preflight(ctx, *Context) error                   // 明确修改共享缓存
PrepareSubscription(ctx, *Subscription) (*Prepared,error)
(*Prepared).ClearTargetAfterLibraryCheck()       // 先读取 source seasons/names 等库事实
IdentityCandidate(ctx, cached, prepared, domain, MatchFacts) (MatchResult,error)
PluginCandidates(cached, subscription, allowTitle, stype) ([]*Context,error)
(*Context).EncodeState() ([]byte,error)
DecodeState([]byte) (*Context,error)
```

`RecognitionRequest` 有 Phase / API / **真实 MetaObject 指针** / Kwargs map。API 是原 `recognize_by_meta` 或 `recognize_media`，参数 map 明确保留原 null/缺失差异：producer/preflight 仅 obtain_images=false；local_rebuild 额外含 episode_group（即使 null）；target 含 mtype、完整四 ID、source/mediaid、episode_group、cache=false。回调可以修改该 MetaObject，就像原调用传递对象引用；若宿主要隔离必须明确其与原行为差异。回调 nil,nil 是原未识别，不是一个空 MediaInfo；空对象重置失败计数。

`NewMediaInfo` 体现原 dataclass 构造：默认集合独立，显式传入的集合保持引用，raw type 字符串保持字符串；`NewNormalizedMediaInfo` 是**适配器**：冻结 JSON 输入并把合法 type 字符串转换为源 MediaType 枚举。recognizer 通常使用后者。不能使用后者偷偷修复插件诊断 title 分支原 raw string 状态。`FromDict` 会跳过原 property 名称、按源直接赋值，并在最后转换 type，出错可能保留部分赋值。

`NewTorrentInfo` 体现原 dataclass 的原字段与显式集合引用；没有绕过站点 API 或存入任何真实 cookie。本模块无 secret 管理。正式持久化沿用宿主既有敏感数据存储策略。

`NewSubscription(map)` 冻结数据库投影；`Words() (*[]string,error)` 保留 falsy→None / 原换行 split，包括空行与同值替换。真但非字符串按原 AttributeError 返回，不能被当作没有局部词。

`Context.ShallowCopy` 仅新容器/controls map；Meta/Media/Torrent 与原 nested controls 仍共享。`DeepCopy` 保留稀疏字段 membership 且隔离对象/集合/整型值；插件读取先兼容旧缓存，再逐候选 deep copy。`ToDict` 是原展示投影，不足以代替持久化；EncodeState 保留完整 MetaObject 私有/缺失/空/None 状态及 type 枚举 presence。

State version1 中 meta 是正式 MetaObject state；media/torrent 为原实例字段；media_type_is_enum 是序列化补充；fields 是稀疏 controls；meta_frame 对应该 MetaObject 实际产生时全局；selection 有 present/words、decision_frame、原 subscription map、有效 sites/domains。原 title/description 在 torrent 字段，不会被改写为处理后 org_string。local 重建时才替换 meta_frame，未重建候选保留缓存产生时 frame。恢复只 decode，不调用 ParseTitle；禁止根据新配置重算缓存字段。

接入最小次序：

1. 本批已落地纯包和全部原执行参考，现有完整 MetaInfo API 保持原签名，也没有复制第二套 engine；下一窗口先接 producer/reader，不重新定义解析结果。
2. 站点资源 producer 返回完整 TorrentInfo，catalog adapter 返回正规化 MediaInfo。依照原链路创建 Context，再原子写 envelope；明确版本/旧缓存迁移策略。没有历史完整状态时不得从五字段 Metadata “重建出”原 MetaObject。
3. cache reader 恢复→EnsureCompatibility→batch preflight，保存成功/失败次数变化。逐订阅目标在当前配置构造，先执行库事实检查，再 Clear。
4. 用原 get_sub_sites 的默认站点规则解析 Sites，SiteOper 解析 Domains，作为 Prepared 明确事实；局部选择保存完整 selection frame。IdentityCandidate 之后必须交给实际 rules/best-version continuation，只有全部通过才能下载。
5. SubscribeGroup 调用独立 PluginCandidates 读取完整 Context。它不等于 core 自动订阅筛选；保留四 ID/generic 归一比较、低置信标题标记和 deep copy。父级消费所需的 resource_* / customization / season 应从该 Context.Meta 取得，不能另行对下载标题再运行五字段 Parse。
6. 宿主用合成 catalog/site/library callbacks 先串联 event/任务/快照事务；再在隔离 NAS 环境验证，最后启用。原始 provider 正规化、freedate 时钟和完整 matcher 边界没有补齐前，不宣称原 RSS 全功能已经等效。

正式迁移保持全部运行时 Go 与状态测试同冻结候选逐字节一致；仅新增包说明、来源/许可证，以及 oracle 测试夹具路径和失败诊断临时目录。离线参考补齐 subscribe.py 源 SHA 的执行时断言并重新运行：110 条结果及 gzip 字节与候选完全相同，SHA 为 `dc54692f83108c0db6ff36ccd740f10abdaea05d0bc885313a18d1221636213e`。

正式纯包验证：`go test ./internal/rsscontext` 通过（2.040s）；`go test -race ./internal/rsscontext` 通过（7.311s）；`go vet ./internal/rsscontext` 退出 0。本批未运行 App、全仓或 NAS 验证；应用消费者仍未接入。

后续纳入本地Go/modernconly全量检查并通过。NAS CGO0 linux/amd64纯包4个顶层用例（含完整110例原参考）全部通过、零跳过；256MiB/GOMEMLIMIT64MiB容器退出0，无OOM，本地/远端二进制SHA一致，独占验证目录及容器已清理，见 [plugin-organization-nas-verification.json](plugin-organization-nas-verification.json)。这些证据仍不代表App/RSS消费者已接入。
