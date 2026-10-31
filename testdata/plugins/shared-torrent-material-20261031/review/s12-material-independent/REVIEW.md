# S12.material 独立 source-first 合同复核

2026-10-31 UTC。结论：**可进入这一完整操作的窄实现计划；当前尚无普通 SDK→宿主完整材料调用链，不能计已完成。** 不重开 S06，不扩 download_single、browse/RSS、浏览器、Servarr 或全站 parser。

先独立读固定源 `8a83efeacd3e26b82be495e17d50146e31c96a60`，生成 `SOURCE-FIRST.json`，之后才读取 owner `s12-source-resume/{contract.md,bindings.json}` 与当前 target `760720ed0a96fd9e7a647914c14545bde5ef857e`。两者 material 主合同一致；下述是精确补充，不把 owner 结论充作源事实。本轮仅文本/AST、JSON/hash与本审计写入；没有 Go、源 import、服务、HTTP/网络或产品/账本修改。

## 1. 源接口及可观察分支

入口 `app/chain/download.py:658–783`：`download_torrent(torrent, channel=None, source=None, userid=None)` 返回三元组 `(bytes | string | None, folder, files)`，不是下载任务，也不创建 DownloadHistory。

- 无 enclosure → `(None,"",[])`；精确小写 `magnet:` → 原字符串/空目录/空列表。均不调用 helper、不通知。目标仍需 invocation/capability，但不能凭空要求这些短路输入先有站点/先 browse。`SOURCE-FIRST` 的“no network client”在此准确含义是**不发网络请求**，不对目标构造对象顺序作额外要求。
- 直链：传 `site_cookie`。间接 `[...]URL`：空 `[]` 取尾 URL；非空原样 base64→UTF-8→JSON。第一阶段 cookie 是 descriptor 显式 opt-in，proxy 来自 descriptor；只有 method 精确 `get` 用 GET，其他用 POST，**两者均传 query params，不是 JSON/form body**。非空 header dict 替代默认 headers。
- 间接响应先按 Requests truthiness，失败/假值 → 无 URL早退，**无失败通知**。`result` 假值取 response.text；否则 JSON 的 success key（若有）及每层点路径 `.get` 均按 Python truthiness检查。path+query-param 同时有值则 `urljoin`+`urlencode`；否则调用 `:65–84` normalizer：有合法 trusted base 时，相对 URL连接、绝对 URL只换 scheme/netloc。无整体 catch；base64/JSON/类型/属性异常不能冒作普通网络 None。
- 间接第二阶段 cookie **恒清空**，`cache_invalid=False`；这不禁用有效字节缓存。第二阶段 UA=`site_ua or USER_AGENT`、proxy=`site_proxy`，与换票阶段不同；chain不传 page_url/referer。
- helper string 直接返回（并非重新验证 magnet）。helper falsy content 才发送 Manual 通知：title=`{title} 种子下载失败！`，text=`错误信息：{error_msg}\n站点：{site_name}`，保留 channel/userid，source仅 channel truthy时保留。通知返回后才返回 `(None,"",[])`；通知层抛错不能被本操作无条件吞掉。
- 普通 200 非空坏 bencode：helper解析层吞错，因此可返回**原 bytes、空 folder/files**，不缓存、也不通知。这是源与已留 oracle 的真实行为，不应用 `ProductInspect` 把它改成 None；实际下单的严格安全校验保持独立。

直接 callers只用于核返回用途：`:870` download_single 接三元组；`:1300/:1528` batch遇 string磁力跳过文件/集数分析。它们不属于本片实现范围。

## 2. helper、缓存、网络与通知的最小依赖

`app/helper/torrent.py:66–190,208–260`：

- URL的 MD5+`.torrent`，区域 `torrents`；先读取有效缓存，folder或files非空即命中。坏缓存重取；有效下载仅 files非空时缓存。原 FileCache 可选文件/Redis；本项目保留已明确的本地存储边界，不能声称实现了 Redis。文件写临时文件后替换，get无逐次TTL。
- GET手工处理301/302；Location缺失在源会抛异常，原 helper不对相对 Location做join。磁力跳转立即返回。首次下载提示匹配 action=`?` 的第一个表单，input按源顺序构成dict（重复键后值）；POST使用 Requests默认自动redirect，而不是GET手工路径。
- None、空200、429、一般状态及首次form失败有不同 error_msg。429/None不写invalid；一般状态仅 cache_invalid=true写invalid。**download_torrent本身不查 is_invalid，不能加成“命中黑名单即拒绝”新行为。** 原invalid区域128项/24h是相邻共享状态，不扩下载检查功能。
- 同步 RequestUtils `:324–456,511–589`：verify=False、timeout20、stream=False；proxies=None仍允许 Requests环境代理，不等于直连。默认Accept缺失；默认form Content-Type；UA等于全局值时有实际caller suffix。此处没有传Session，不能继承仅shared-session触发的幂等重试。
- 源原代码可能跨域重带凭据；已批准目标隔离/撤销/current-site/不降级凭据约束必须保留，TLS例外只新私有client，不能修改全局/NativeSiteHTTP全部消费者或扩大trusted origins。

通知不是日志替代：源 `ChainBase.post_message:1631+` 会render、保存history、发NoticeMessage事件、入队/发送。现 `App.postOriginalPluginMessage` 已实现相应路由底座，可复用并单测此新调用。**不应直接经 `originalPostMessageResource` 代调用**：后者是独立插件post_message入口，补插件详情link且有自己的并发入口约束；DownloadChain失败通知本身未补该link。新材料能力不因内部源副作用而要求额外worker声明post_message能力。保留既有并发/生命周期安全限制，不开启任意serial重入。

## 3. 目标复用：不是全缺，也不能整包假继承

| 实际文件/入口 | 可继承 | 仍需接线/差分 |
|---|---|---|
| `iyuuautoseed/torrent_helper.go`、`torrent_bencode.go` | 已有Download与实际TorrentFiles投影；25文件/18下载旧oracle；保持坏bytes、magnet、cache/form分支 | 新wrapper仍缺；下面确切路径反例和16MiB限制要明列 |
| `iyuuautoseed/file_cache.go` | 真实os.Root、原MD5文件名、atomic rename/sync、热invalid数据 | 新App须真实own/open/close/复用此目录；不能每次建内存假cache。当前构造调用检索仅native组件测试，不能据类存在冒当前ordinary App已接 |
| `iyuuautoseed/native_host.go:310+` | 有真实组件consumer、source site snapshot/lineage思路 | IYUU专用Torrent恒cacheInvalid=true且丢folder/files；不等于主动标准SDK三元组 |
| `sites.Client.Torrent`、native下载 | 当前numericID材料抓取及严格ProductInspect安全链 | 不能替代输入enclosure/descriptor/tuple，也不为了此片放宽实际下载器入口 |
| `plugin_original_resources.go` | 当前identity/capability/invocation/scope/取消底座 | 白名单/dispatch尚无material operation；要新增有限入口而非开放HTTP任意调用 |
| `rsscontext.TorrentInfo`、`originalResourceSiteFields`、`sites/source_torrent.go` | 已有完整私有源模型与可信站点ID/配置 | 不从公开Resource反造Cookie/真实enclosure；不要求先browse，不让worker JSON创建凭据或origin授权 |
| `NativeSiteHTTP`、`networkproxy/requests_*` | origin guard、response/cookie编码、Requests代理merge/取消可复用 | NativeSiteHTTP本身严格TLS、proxy=false无environment route、UA固定Plugin/iyuuautoseed，不能原封当新source client。只独立新策略；实际caller UA、每个RequestUtils逻辑请求独立proxy merge/redirect状态 |

独立核回 helper源SHA与旧oracle相同；旧综合报告直接绑定的helper/cache/siteHTTP/oracle四文件SHA均与当前同值。只继承这些已证输入/分支，不声称全部transitive依赖/环境相同、不重跑全套历史。S06 producer02三份samebytes的**源parser**事实可复用做新精准对比，不冒已有Go同输入门已PASS。

### 已见确切parser缺陷（静态，不是猜测）

`TorrentFiles` 当前拼接 `joined=="" || strings.HasSuffix(joined,"/")` 时执行 `joined=s`。实际torrentool `files` 使用 `os.path.join(base,*path)`。

标准bencode对象 `info.name="Root"; info.files=[{length:1,path:["sub/","x"]}]`：源helper得到 folder=`Root`, files=`["sub/x"]`；当前Go直线控制流得到 folder=`Root`, files=`["x"]`。旧25例未覆盖跨component的末斜杠。需要仅修这个拼接分支并补同bytes一例；无需重造parser/扩任意Python对象。该例是metadata投影，不授权写其文件路径。

## 4. 大字节与安全预算：实际差异，不取消guard

`runtime-safety-boundaries.md` 不是无限输入豁免，也不是用户已接受所有现有预算的证据。本片保留防护，明确披露原源无此上限：

- `torrent_bencode.go:20`：raw>16MiB直接解析失败；因此可能改变源folder/files和cache行为。
- `file_cache.go:48–59`：get/put最多16MiB。
- `site_http.go:424–458`：原响应及解压后各16MiB。
- `plugins/transport.go:13–20,146–170`：logical约16MiB+4KiB，JSON `[]byte` base64会将可携带原bytes压到约12MiB。现transport.chunk只分physical frames，**不会提高logical ceiling**。

不能以“已有分块”宣称解决16MiB材料传递，也不能为忠实性取消全局预算。至少不能让新ordinary入口把已有有效16MiB材料静默再缩小。有限可行方案（建议，未实现/未授权新API）：新S12内部prepare产生绑定当前invocation/identity/generation的不可猜handle和固定total；SDK透明按约1MiB页读取同一不可变bytes，复用既有HostCaller与physical分块，每页/最后校验长度hash、每页scopeguard，调用结束/取消清理。16MiB约17–18个hostcalls，低于现默认64，wire预算也可有界；不得暴露文件路径或绕过cache/root。现SharedLocalStorage只有exists/list/delete，**不存在可直接冒用的普通read-range接口**。该方案是本片小私有协议，不是全局transport放宽或通用blob框架，owner须先冻具体生命周期/预算再实现。

对>现预算的source差异仍登记未批准，完整ordinary功能可按已明确运行预算计验收，不称任意大小严格源等价。仅一份接近16MiB、跨base64阈值的有效合成材料与一个超界拒绝足够检验这条新边界，不造巨大矩阵。

## 5. 下一片有限门与终止条件

1. **source wrapper小oracle**：仅原download_torrent/normalizer选定AST，受控RequestUtils/helper/notice接收者，保存原三元组、两阶段参数、通知/异常。覆盖direct/间接与早退/通知分歧；不重跑旧helper18/25，不import整应用/Requests，不把stub记录当HTTP源执行。
2. **Go核心/SDK**：nullable bytes/string/None三态、folder/files与顺序；descriptor GET/POST query、cookie清空、UA/headers、truthy与异常、base/path/result；复用旧helperoracle，加上述一个拼接反例。严格metadata解析与下单安全边界分开。
3. **真实普通worker→host**：一个当前标准artifact，经真实scope及可信站点，owned HTTP/TLS固定材料响应，验证直链、间接两阶段、form/redirect代表路、取消/配置撤销/跨origin凭据隔离、确切失败通知保存/事件；无真实站点凭据、CONNECT或上游转发。资源允许时只相关并发根race。
4. **真实冷启动/持久缓存**：首host取得tuple并落盘，第二host重用同目录无新增材料请求；损坏缓存恢复；新大材料分段与取消/回收证据。可与普通CLI两host合门，不另造server矩阵。原消息provider实际发送可由owned标准模块承接，不能测试外发真实用户。

只在上述新普通链、实际副作用/失败恢复与受影响边界闭合后，计 S12.material操作完成；browse/rss/S14、download_single和S06 producer/DAO均不因此完成。当前仅合同准备READY，无新的运行PASS。
