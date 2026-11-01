# S11：主动 `ChainBase.recognize_media` 的有限定界

只读观察，2026-11-01 UTC。活动树观察 HEAD `cd21eaf2bd8aeeb4af012e7a934faad8c5b9b288`；TR 候选由协调者并行收口。原源固定 `8a83efeacd3e26b82be495e17d50146e31c96a60`。ledger S11 精确 inventory **只有 ChainBase.recognize_media**，并非重做 source_paths 中全部 parser、API、scraper、Fanart 或 DAO。本次未执行源/Go/网络/服务，未改产品/ledger。

## 结论先行

**S11 不因 S04/S06/S07 的旧行/native-origin 决策而停住，可以继续真正代码实现。** 输入由当前普通插件调用提供，身份/配置由当前宿主选择，不需要捏造旧站点、历史或下载器行。现有真实 TMDB client、完整 MediaInfo 投影、MetaObject 和反向模块桥可复用；缺的是主动 SDK/host 入站及完整原识别调度，不是新增用户必选配置字段。

最小可交付的垂直路径是：**真实安装 worker 主动调用 → 当前 scope/cap → 原参数归一和模块顺序 → 显式 TMDB ID 的真实 native client → 原 MediaInfo 或 None → SDK 返回**。首个闭环可用 `meta=None, share_meta=None`、明确电影/剧集类型；这正是原入口的普通分支，不需任意 Python 构造，也不会查询/上报名称共享。但不能把该有限闭环称为 S11 全输入已完成；同一入口的剩余确定算法应接续实现，不拆成无生产调用的无限纯叶片。

## 1. 原入口输入、返回及副作用

`app/chain/__init__.py:637–747`，11 参数：

| 参数 | 原默认／含义 |
|---|---|
| meta / share_meta | 均 None；MetaBase 是现成元数据对象，后者用于共享查询/上报原始关键词；不是一条必须重新 ParseTitle 的字符串。 |
| mtype | None 或 MediaType；有显式 TMDB ID 时不能先用标题推断类型覆盖模块消歧。 |
| source / mediaid | None；source 经过 strip/casefold/tmdb alias；requested_mediaid 原值另留给模块。 |
| tmdbid / doubanid / bangumiid / anilistid | 均 None；原兼容 ID 优先与 source 分支见 `_resolve_media_source_params`，不能全部转成 TMDB ID。 |
| episode_group | None；假值可从 meta.episode_group 继承。 |
| cache | True；不仅是一枚传给插件的标签：包围原 run_module 的 `fresh(not cache)` 有上下文传播/恢复语义。 |

原注解不等于运行时 DTO 强制类型。有限 Go wire 必须明确表达缺省/null、原数字精度、当前支持的 MetaObject 状态，不能通过 JSON 解码到 float64 改 ID，也不扩任意 Python 实例、属性执行或反射 registry。

先从 meta 补假值 tmdbid/doubanid/source/mediaid/episode_group，再只留下所选来源 ID；无显式类型且无 tmdbid 时才允许 meta 的电影/电视剧类型推断。完整 `run_module("recognize_media", …)` 在 fresh 范围中执行：插件优先，真实系统模块随后；原 None/全 None tuple、list 累积和 false/0/空dict终止不同，保原 INFO/错误处理。

返回 **MediaInfo 对象或 None**，包括原来源身份、provider 信息、分类/季集/剧集组等，不含额外 Fanart 阶段。不能拿 UI compact `catalog.Media` 或默认空 MediaInfo 代 None；`tmdb_info` 不是此入口。错误也不一律变 None：参数转换及真值结果属性访问等可向外抛；实际系统模块错误由原 run_module 的 handler 决定。原 TMDB 模块还会修改传入 meta.type/meta.tmdbid、写识别缓存；跨进程 Meta 状态同步是要实现的具体技术合同，不是旧数据政策。

成功结果若 `recognize_cache_hit` 假则调用共享上报 helper；其原 `_build_recognize_report_payload` 在 meta 或 mediainfo 缺失时直接 None，因此首个无 meta 的 ID 路径无上报 HTTP。初次未命中且无 source、允许共享且有 meta/无四 ID 时：深拷贝 meta → query → 参数转换 → 再 run_module → 原本地缓存回填 → `MediaRecognizeShareCount` 增量（统计异常只记录）。默认 `MEDIA_RECOGNIZE_SHARE=True`，不能为了整个 S11 绿灯擅自全局关闭。

## 2. 插件→宿主方向与已有交集

| 已有目标 | 可以继承 | 不代表已完成 |
|---|---|---|
| `plugin_original_catalog_recognize.go` | 原 ID/来源优先、sync/async 模块边界、list/假值处理。 | 现用于宿主询问插件；`originalStandardCapabilityAllowed` 没有主动识别 cap；不存在普通 SharedRecognition SDK。该 helper 也没有完整共享回查/fresh 后半段。 |
| `plugin_original_modules.go` | 当前加载对象、get_state/get_module、精确绑定 handler、INFO 顺序、错误归属、list 合并。 | 不可因为 43 反向调用已有信用就抵消 S11 主动请求方向。 |
| `catalog.Client.SourceInformation` → `sourceRecognizeTMDBCore` → `sourceTMDBMediaInfo/sourceTMDBMediaModel` | 现真实固定 TMDB HTTP、完整 append response、分类与完整源模型，不只是 UI 行。 | 当前 `originalRecognizeTMDBModuleGroups` 固定有效类型/正 TMDB ID，不能照搬成全 11 参数入口；原类型未知按 TV 后 movie 查询/消歧尚要接。 |
| `releasemeta.MetaObject` / EncodeState / ToDict | 缺省与实例 null、big.Int、真实现有 parser 状态、copy 行为。`plugins.ModuleMediaInfo` 保原 JSON 字段。 | 内部持久状态不是现成公共 SDK 任意对象工厂；不要重做全 MetaInfo，也不要以空 map 冒缺失对象。 |
| 当前普通 scope、retained calls、日志、App admission | 安装身份、generation、调用存活、关闭/取消和既有 transport 边界。 | 需主动桥实际接入，不靠测试直接调用 native 函数代替。 |

已有 `testdata/standard-module-host/recognize-source-oracle.json` 的 10 case 与固定 chain/object/media 三源 SHA 实际相同，可继承原参数/dispatcher 交集；其生成说明明确 **synthetic plugin/core model outputs，fresh 为 recording/no-op，share 关闭**，不是 native HTTP/cache/shared-server 等价证明。对应反向实际 SDK 测试源码仍在；本次没有复跑或重算其历史 credit。

## 3. 不能直接套用的真实依赖

1. **同 worker 重入**：主动 host 识别若直接执行现 `originalPluginModules`，会再次向当前有 get_module 的 worker 请求 get_state/get_module；serial `callOriginalMethod` 持 methodMu，不能假定可重入。S10 已有 `originalManagerNestedTarget` 对确实自身目标要求实际协商 concurrent、其它 idle serial 不一概拒绝。S11 应沿该已交边界做窄校验/retained parent，并验证真实并发 worker；不可静默跳过当前插件、再开 worker 或绕开模块优先级。当前还未接，因此这里只记明确实施依赖，未运行死锁探针。
2. **原 native 识别不是现 UI resolver**：`ResolveExploreMedia` 的“唯一 exact title/type/year 才自动认领”是现 UI 身份选择合同；原 TMDB `_prepare_search_names/_search_by_name` 是中文/简体/英文有序尝试及具体季/年份回退。不能用前者冒后者。未知类型的双查询/同 ID 消歧、完整 group-season 读取与填充、正负识别 cache/fresh 都是可在授权内按固定源码编写的依赖，不需让用户决定每字段默认。
3. **已有 native 明确 pending 要保留事实**：`sourceTMDBMediaInfo` 碰 CN alias 简繁短语转换时返回 `SOURCE_MEDIA_CONVERSION_PENDING`；当前 anime 字符/类别映射不是完整 zhconv 短语算法。TV model虽有 episode_groups 字段，原 `_process_episode_groups` 的实际组详情请求不能只复制字段称完成。catalog 目前固定 ID 上限/4 MiB body/12 s 等是既有资源边界，不自动提升为 Python 无限域。
4. **来源和共享层**：固定原 Douban/Bangumi/AniList 也实现 recognize_media；现 explore/provider client 可作后续部件，但尚不能直接宣称已执行同一普通识别方法。当前 App 未见原 `MEDIA_RECOGNIZE_SHARE` 的 query/report/cache回填/计数链，不能用内存结果或 fake provider 代替。其实现是技术依赖；此报告不启动中心访问、不新增任意 URL 或放宽既有网络/TLS政策。
5. **不要混入上层行为**：`originalCatalogRecognizeByMetaMethod` 的 name.recognize 事件、未知 media-key 转换、获取图片以及 UI 默认响应来自其它上层 caller；S11 直接 ChainBase 入口不能额外调用它们改变次数/结果。

## 4. 下一完整代码块与验收边界

建议一次提交可执行的 **主动识别 SDK + 新 App fixed handler/native closure + 资源两窄 hooks + 实际 worker**，先闭显式 TMDB 分支，不修改全局 decoder/ABI、既有反向入口或其它 provider transport。新文件名可用 `internal/plugins/shared_media_recognition.go`、`internal/app/plugin_shared_media_recognition.go` 及对应测试；所需 native 缺算法集中在私有 recognition helper，不建立业务 registry。具体名字/文件写窗由协调者裁定，本报告无产品写授权行动。

首闭环必须实际 worker/当前 scope/真实 native decoder 和完整返回贯通，覆盖命中、None、插件优先、无匹配后 native、精确过期 callback 与 self-reentry 边界；不能只拿 recording provider 成功。现 catalog 测试 seam `NewWithTransport` 及旧 `imageTransport` 只是包内协议模拟，**不是已存在 ordinary CLI owned-provider 证明**。若使用 owned HTTP 协议 fixture，需由协调者明确受信私有 seam/固定端点并保原生产 authority；不自行搭通用转发、CONNECT、改 hosts/env/TLS 或真实外连来制造成功。

完整 S11 收口还需同一入口正常覆盖 meta 名称识别、cache/fresh、四来源与共享路径；不得将这些缺项标成等待用户旧数据决定。可先复用已核 10 case 的 source交集，新增采集仅围绕缺失完整原节点及真实受控组件，不能重新造 oracle 或以目标输出反推。当前阶段只给依赖判断，未准备/执行新 collector、未运行任何门，S11 ledger 保持 discovered。
