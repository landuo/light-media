# MPServerStatus 1.4 Go 行为切片

2026-10-16，已完成正式 `internal/plugins/serverstatus` 纯算法、原表单/详情页/仪表板与现有Go SDK handler。正式 App 宿主、固定 HTTP 适配、配置生命周期与前端已接入；本地正常/竞态/全量/纯 Go 构建及隔离浏览器验证通过。完整个性化仪表板、正式用户配置迁移和部署尚未完成，不能称 23 个插件已全部迁移。

## 来源与参考边界

实际安装 `/app/app/plugins/mpserverstatus/__init__.py` SHA-256为 `63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90`。实际 RequestUtils `/app/app/utils/http.py` SHA为 `f8d90a731717de14a822d8035a14486caba2e25d52018c87d6ec0d9fcd3b2b27`。42方法与脱敏work副本去掉docstring后全部AST一致；脱敏副本正文hash未冒充实际来源。快照仅含源码、公开元数据和依赖版本，没有用户配置、秘密、通知或原应用初始化。

`testdata/plugins/server-status-original-oracle.json`含174个实际源码AST执行案例、15组时序，SHA为 `c47b1c6fecdd604e7318255fe47d1d1038a5f0a5481798a38a2debf84bb055e6`。RequestUtils/socket/ssl/time在这份纯行为参考中是合成替身；它证明算法/调用顺序/完整节点树与缓存，不能单独证明真实网络库语义。数字0和0.0按数学值比较，其他值/字段/调用/树逐项对照。

`server-status-http-original-reference.json`额外19例真正执行原RequestUtils AST和相同版本requests依赖，只访问自建loopback TLS服务器；`server-status-unicode-original-reference.json`额外8例直接在实际安装Python3.12.13中只执行原解析静态方法。后两者SHA分别 `2e5906d6f940538527d6ff0b50d8216037b1a3f43d0e57b607ee5f09e66bb616`、`1d6fe7458aad9b4a387651871eb44945e2be68820854b8c49a45e0eeec43d948`。原执行生成器和安装快照位于 `testdata/plugins/server-status-reference/`；Python仅为离线参考工具，运行时纯Go。

## 完整行为矩阵

| 原接口/链路 | 安装版行为 | Go 实现/宿主约束 |
|---|---|---|
| 元数据 | MPServerStatus / MoviePilot服务监控 / 1.4，jxxghp，图标 Duplicati_A.png，order 99/auth 1，配置前缀 MPServer_ | handler.go 保留公开字段；order/auth 走正式平台现有元数据规则 |
| init_plugin | config or {}，enable=bool(config.get)，清 last_sample/DNS/TLS 缓存；不发请求、不写配置 | Init 使用 Python 真值，保留字符串/列表/对象 enable；重载必须真正清宿主缓存 |
| get_form | 单个 VSwitch enable，标签启用插件，cols12/md6；model 为当前 _enable | Form 完整树与实际 AST 对照；正式 app 保存原对象，不强制丢弃未知字段/重写 enable |
| get_state | _enable | 按 Python 真值投影，不能用仅接受 JSON bool 的 struct 偷换 |
| command/API/service | get_command/get_api 均 []；无服务定义、事件监听、定时任务 | 不增加 HTTP API、cron、通知、事件或 one-shot |
| stop_service | pass | 不清样本、不发网络、不写配置 |
| get_page disabled | 仅 div/插件未启用/class text-center，不访问网络 | Handler 和 Machine.Page 均保留 |
| get_page enabled | 每次请求状态；DNS/TLS按缓存；成功12卡+连接明细表/服务探测表，失败4卡+宽服务探测表 | 完整原节点、标题、图标、颜色、列宽、说明及缺失值格式对照，不重设计 |
| get_dashboard | 直接调用时无 enable 判断；每次 HTTP，不调用 DNS/TLS；cols12/md8，attrs refresh10；6卡 | 核心仪表板列表通常受 get_state 过滤，但直接方法的原行为仍保留 |
| HTTP | 固定 https://movie-pilot.org/status，RequestUtils timeout20；TypeError 后用无 timeout 形参重试一次；其他异常返回错误字符串，None 为网络错误 | Network 无 worker URL/host/port/TLS 参数；已支持签名 TypeError 的一次回退；真实宿主必须固定目标 |
| response bool | requests.Response 在 400–599 为假；503 即使正文有效也显示无法连接服务器。非错误状态正文不合法则服务器状态格式异常 | 302/503/None/意外异常均有判别参考 |
| HTTP probe | URL、host、状态、200≤code<400 的 ok、响应秒数、HTTP1.0/1.1/2、Server/Date/Content-Type、实际 content 长度或 text UTF8长度 | Probe 保留；正常 HTTP 异常应与安装 RequestUtils 一样返回 None，而非回显未经处理的底层凭据/请求 |
| 解析 | 去空行；大小写不敏感 active 与读写等待；先 server accepts 后一行取前三数字；否则第一条恰3数字且无大小写敏感 Reading/Writing 的行；缺 active→counter→rw 的错误顺序 | 支持 Python Unicode 十进制数字/空白，计数用任意精度整数，未缩到 int64 |
| 指标 | busy=reading+writing；dropped=max(accepts-handled,0)；0分母为 None；不把busy百分比截到100；累计请求/handled | 完整指标，任意精度计数和原浮点格式 |
| 增量采样 | page/dashboard 共享最后一次成功采样；正时间窗口才计算；计数回退时该速率 None；失败请求不清上一样本；非正时差仍用当前样本替换 Last | 跨页面、失败插入、同时间、时钟回退、计数回退与恢复有参考 |
| DNS | urlparse host/port，getaddrinfo(SOCK_STREAM)，按出现顺序去重 IPv4/IPv6；成功和失败均缓存60秒；以探测开始时间计缓存，age<60 才命中 | Machine.DNS；原版没有显式 DNS timeout，不用 HTTP 请求来伪造 DNS 数据 |
| TLS | 仅https；默认校验context、TCP timeout5、SNI host；优先CN否则第一个非空字段；notAfter UTC；days_left 用 floor（负数不能向零截断）；成功/失败均缓存3600秒，严格 age<3600 | Machine.TLS + 独立真实校验 adapter；错误也缓存；不把 HTTP 连接证书复用替代独立探测 |
| 颜色/格式 | 延迟1/3秒阈值；TLS7/30天阈值；丢弃数1/10；DNS个数反向阈值；千位逗号、小数、百分比、毫秒/秒、待刷新、等待采样、KB/MB、最多3个IP | 全部叶子函数均有实际 AST 样本及边界 |


## Unicode与原方法覆盖

源使用stdlib `re`，不是第三方regex。实际解释器Unicode15.0.0的680个十进制数字（68组）及29空白已固定为Go表；不能随Go未来Unicode升级改变。Python IGNORECASE包含İ/ı/ſ（以及ASCII K对应K），RE2单纯(?i)不够；Go用固定ASCII字面量等价类保留。真实安装参考明确Unicode15 Nag Mundari数字可解析、Unicode16 Garay数字不可解析，上标/带圈数字与U+180E均不能冒充原输入。

42个原方法没有用空实现替代：8个生命周期/公开方法映射Init/State/Stop/Form/Handle/Page/Dashboard（command/api原本就是空）；8个采集/解析函数映射Load/Parse/Metrics/Probe/DNS/TLS/CertificateName；8个页面构造函数映射Elements/dashboardCards/card/stat/table/summary及原成功/失败分支；其余18个安全运算、格式、颜色函数由status.go完整实现并纳入原执行案例。原节点组件、标题、说明、颜色、列宽、图标均在完整树中比较。

## SDK与宿主接线合同

`Manifest()`使用现有plugins.Manifest，`Handle()`使用现有plugins.Invocation/HostCaller；只有config_form、invoke(init/reload)、page、dashboard、stop。两个capability为 `server.status.configure({})` 和 `server.status.snapshot({"include_network_details":true|false})`；没有源不存在的API、事件、cron或通知。

worker参数不能选择URL、hostname、port、timeout、TLS政策、配置revision或cache。宿主从真实已安装版本/代际/revision及运行/视图身份核对当前配置；page要求details=true，dashboard=false。只读表单默认false由App合并真实保存配置并按Python真值显示；保存对象本身保留未知字段/原enable值。

Machine的Last、DNSCache、TLSCache必须放主进程易失内存，同实例串行采样，不能留在每次退出的worker或持久KV里。Init/reload清全部三份cache，同revision reload也换epoch；冷启动自然清空。Configure回调不能在已持process令牌时重新等lifecycle锁；只在短pluginMu范围核对，cache替换不等旧ProbeMu，网络期间不持pluginMu/settingsMu/SQL事务。快照取样前后均验证owner/epoch，配置切换/卸载/重装不能发布旧结果。App 正式实现位于 `plugin_server_status.go`，固定网络适配位于 `plugin_server_status_network.go`。

源page失败仍执行DNS/TLS，dashboard直接调用没有enable判断；只在page禁用时完全不访问host。页面的10秒refresh来自浏览器属性，不是后台周期服务。源HTTP20秒是connect/read idle；独立TLS5秒，DNS无专用timeout。平台总视图预算须明确有界，不能伪称源已有该总时限。

## 前端兼容

原15种组件：VAlert、VCard、VCardText、VCardTitle、VCol、VForm、VIcon、VRow、VSwitch、VTable、div、span、tbody、td、tr。VCardTitle/VIcon需同时进renderer与page白名单。源码所需15个MDI名称在reference/ui-reference-audit.json；只读快照时离线图集仅已有mdi-alert-circle-outline，另14个须由前端补齐，不使用自创控件/字符替代。正式 renderer/page 白名单已加入 VCardTitle/VIcon，并补齐这15个图标。安装版 Vuetify 3.7.3 的32条实际布局/文字 utility 由构建脚本提取，仅作用于插件页面/配置/日志弹窗，不引入全局 reset 或新配色；字体继承原项目 Inter/CJK 覆盖。

## 纯包与早期原型验证（历史证据）

正式包 `go test ./internal/plugins/serverstatus -count=1`通过1.279秒，`-race`通过7.786秒，`go vet ./internal/plugins/serverstatus`退出0。真实测试子进程运行plugins.Serve，验证handshake→固定空/布尔host_call→完整原页面→stop；两个短worker共享宿主LastSample，page持有DNS/TLScache，reload重置，禁用page零hostcall，直接禁用dashboard仍采样。未运行App/full，未改catalog/App/go.mod/go.sum，未部署/探测公网或用户服务。

独立网络原型normal0.461秒/race1.668秒/vet0另通过，含专用loopback HTTP可读自签名证书、独立TLS和普通客户端始终拒绝同证书。已知不完整的独立原型HTTPNetwork没有复制进正式包；正式固定宿主adapter另在App实现，见下述接线补充。

## 已安装 RequestUtils 与独立探针的实际网络语义

来源：MPServerStatus1.4 SHA `63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90`；RequestUtils SHA `f8d90a731717de14a822d8035a14486caba2e25d52018c87d6ec0d9fcd3b2b27`。只读核验，未导入原应用或读取配置值。source metadata记录实际安装依赖版本；本机专用venv全部固定相同版本，避免用本机默认requests环境替代。

`generate_network_reference.py` 将实际安装 RequestUtils三方法AST及原插件类AST置于仅有必要依赖的namespace，真正调用requests/urllib3、socket、ssl，不是假造HTTP响应。服务器只绑定127.0.0.1，证书/key在临时目录生成并销毁；客户端NETRC指向自建空文件、NO_PROXY限loopback，没有读/发任何用户账号、Cookie或Authorization。源码的verify=False只作用于该临时请求；严格requests客户端和原独立TLS探针仍拒绝同一证书，没有关闭任何全局TLS或凭据登录验证。

## 已确认的实际行为

| 项目 | 原调用的实测与源证据 | 正式实现约束 |
|---|---|---|
| HTTP与TLS信任 | RequestUtils.request设verify=False；同一自签名fixture状态读取成功并有active=12；独立ssl.create_default_context返回CERTIFICATE_VERIFY_FAILED，失败结果被TLS缓存 | 不能把两个探针合并，也不能将HTTP专用差异传播到全局/认证/站点客户端。早期原型HTTPNetwork保持安全校验；正式 App 专用固定目标适配已保留该源差异，并隔离严格代理和独立TLS探针 |
| 传输版本 | 实际requests/urllib3使用HTTP/1.1，raw.version=11 | net/http自动HTTP2可能改变协议展示；专用adapter需考虑原协议 |
| 原始headers参数 | User-Agent=None、Accept=None、referer=None；Content-Type为application/x-www-form-urlencoded; charset=UTF-8 | 不根据构造函数docstring猜测Accept默认application/json，实际并未设置 |
| 实际wire headers | 无Accept/Referer/Cookie/Authorization；User-Agent=python-urllib3/2.7.0；Accept-Encoding=gzip, deflate, br, zstd；Connection=keep-alive | User-Agent不是python-requests；net/http默认UA/gzip集合不等价 |
| charset | text/plain无charset→ISO-8859-1；0x80保留U+0080而不是windows1252欧元符；显式utf-8与application/json默认UTF8；无Content-Type使用实际chardet7.4.3 apparent_encoding | 该参考揭示早期原型缺口；正式字符解码及完整chardet的边界见下文 |
| 错误UTF8 | ff ff e2 82按Pythonerrors=replace变为三个U+FFFD | 连续invalid-byte替换/不完整序列边界须真实对照，不用简单ToValidUTF8猜测 |
| 解压 | gzip、zlib deflate、raw deflate、brotli、zstd均自动解码，response.content和Probe.content_bytes使用解压后的字节，headers.Content-Length仍是编码后的长度 | 该参考揭示默认Go缺口；正式专用adapter已实现上述codecs，保护限制见下文 |
| timeout参数 | 构造时20，get_res无额外覆盖；不是全请求总时限；连续每0.03秒返回数据、timeout0.1秒的本机请求总时长超过0.1秒仍成功 | Go Client.Timeout=20是总时限，不能直接代替requests connect/read两阶段语义；另需有界宿主视图预算 |
| 连接/重试 | 本调用_session=None，requests.request每次创建/关闭自己的Session。普通连接/读取/截断异常吞为None，不进入只对已有_session开放的一次幂等重试 | 不能凭“GET幂等”加自动重试；长驻Go连接池复用会改变该源延迟/建连行为，需要专用处理 |
| None/异常 | get_res吞RequestException→None，插件转换为网络错误；非RequestException ValueError透出，插件catch保留其str；TypeError先重建无timeout形参的RequestUtils再调用一次，构造默认仍20 | 不把正常超时底层请求字符串直接回显；保留意外错误与兼容重试边界 |
| redirect | allow_redirects=True，302→/status后得到200正文，第二跳移除Content-Type等entity headers；Probe.url仍是源固定_server_base | 不能把HTTP302本身一律当最终失败，fixture验证同源重定向，不推断用户代理/私有URL授权 |
| status bool | 503得到非None响应但bool(response)=False；源后续显示无法连接服务器，不解析有效Nginx正文 | 不能把非nil响应直接当成功 |
| DNS | 另走socket.getaddrinfo(host,port,SOCK_STREAM)，顺序去重，源未设置DNS超时；本机仅解析数字loopback，未发真实域名查询 | 不能用HTTP连接IP替代独立DNS结果；缓存正负结果60秒 |
| TLS | socket.create_connection timeout5、默认验证、SNI、独立新连接，不复用HTTP的verify差异/证书或HTTP代理；正负结果缓存3600秒 | TLS异常按原卡显示；Go与OpenSSL自然错误文案可能不同，参考记录平台，不强求伪造相同自然错误字符串 |

## 运行与证据边界

`network-reference-requirements.txt` 固定实际NAS版本：requests2.34.2、urllib32.7.0、certifi2026.7.22、idna3.18、charset-normalizer3.4.9、chardet7.4.3、brotli1.2.0、zstandard0.25.0。安装仅在`.network-reference-venv`，未改系统/主程序依赖。自然TLS错误来自本机OpenSSL3.6.3，而非冒充NAS OpenSSL错误文案。

参考包含19项实际调用（16条本机HTTP路径+3个合成库异常），保存constructor kwargs、RequestUtils传递参数、真实wire headers、解码文本/content/encoding、重定向、错误边界以及源TLS失败缓存。相同版本的第三方请求库真实执行，源码方法没有用Go重写成oracle。慢/分段返回只缩短自建实例timeout以避免耗时20秒，构造20秒默认也单独保留，不使用用户配置。

Go新增测试同时检查上述原参考合同，并在自建TLS服务器上重现HTTP专用放宽/独立TLS拒绝/普通HTTP客户端仍拒绝的隔离效果。**不是**把当前HTTPNetwork包装成已经完整复制requests；当时charset、压缩、连接池与每阶段timeout尚未正式接线；下文正式专用adapter已据此实现。完整chardet仍保留明确差异，不能将原型证据冒充生产接线证据。

复现：在本目录创建专用venv并安装network-reference-requirements.txt；运行`.network-reference-venv/bin/python generate_network_reference.py`。脚本只访问本机临时服务器，生成JSON；之后Go测试只需该已有参考和自己的httptest，不依赖Python/SSH/真实服务。源核验read_source.py仅只读NAS代码和版本元数据。


## 正式固定网络适配补充

`internal/app/plugin_server_status_network.go`的零值 `pluginServerStatusNetwork{}`仅允许源固定URL、DNS host/443、HTTP20秒（兼容回退0仍取20）和TLS5秒；没有worker可指定的网络参数。私有Go测试seam仅接受无userinfo/query/fragment的数字loopback origin，配置/JSON无法构造这些字段。没有读取用户NETRC或发送目的站Cookie/Authorization。

HTTP使用专用transport，源verify=False只作用于此无凭据公共状态origin；HTTPS代理用独立严格TLS握手，代理带认证时也不能继承目的站差异。CONNECT代理认证不进入目的站请求；独立TLS探针直连、严格校验、不走HTTP代理，也不复用HTTP证书。没有改http.DefaultTransport、普通HTTP/登录客户端或任何全局TLS状态。

HTTP1.1与固定原headers已逐条真实loopback比较；请求创建临时transport/session，单次重定向内复用，后续采样不复用连接。重定向只接受相同scheme/host/port，无userinfo；最多30次。302等会移除entity headers，明确删去Go自动添加而原requests没有的Referer。跨origin/downgrade拒绝是平台授权限制，原requests本身没有此同源限制；HTTPS代理严格校验亦是平台保护要求。

connect和TLS握手各按阶段设超时，socket每次读取重置idle deadline，Client没有20秒总时限；外层宿主负责60秒总预算。trickle fixture总时长超过0.1秒仍成功，静默读取超时和截断均为None→网络错误。普通请求异常不回显含代理/请求信息的底层字符串。

gzip、zlib/raw deflate、brotli、zstd及Content-Encoding多层反序解码已实现；压缩前响应和每层解压均限1MiB，超过明确失败，不返回截断成功。额外保护为64KiB响应header与zstd8MiB解码window/memory；这些边界不是原版隐含承诺，应作为平台限制保留。任意失败不改变其他连接或配置。

编码遵循声明charset、text默认ISO-8859-1（不是Windows1252）、application/json默认UTF8；显式UTF8的非法字节替换边界保留Pythonerrors=replace。其他声明编码用现有x/text表，BOM支持UTF8/16/32。**未声称移植完整chardet7.4.3**：无声明/BOM时ASCII/合法UTF8可用，其他模糊字节流显式编码失败，避免猜错后伪造成功；固定Nginx状态ASCII正文不受影响。Go/OS DNS顺序与自然错误文案、TLS/OpenSSL文案也有平台差异，缓存/失效/负结果行为仍按源。

新增Go运行依赖由父级集中固定：github.com/andybalholm/brotli v1.2.6与github.com/klauspost/compress v1.20.3，字符表复用既有golang.org/x/text。没有运行时Python。

窄网络目标 `go test ./internal/app -run '^TestPluginServerStatusNetwork' -count=1`最终通过1.068秒：直接比较16条原实际HTTP路由的状态/协议/解码字节/文本/wire headers，另测越权URL/host/port/timeout零I/O、1MiB压缩/解压限制、同源重定向、单次session、HTTPS代理认证及严格证书、独立TLS、DNS排序/解码边界。原19参考的3个异常模式由纯包原调用序列/TypeError回退参考覆盖，不能把Go固定库误报会抛Python TypeError。初次集成发现Go自动Referer差异，修正后目标终态通过；全宿主/race/NAS最终结果由父级追加。

## 正式 App 与浏览器接线（2026-10-16）

插件已加入内置可信 Go 目录，支持实际安装、保存、初始化、重载、重置、卸载和只读详情/仪表板调用。配置中的原 Python 真值和未知字段加密保留，表单只将 enable 投影为布尔。实例/版本/generation/revision、具名方法和固定空/布尔参数均由宿主核验；工作器不能选择 URL、端口、超时、TLS 政策或其他实例。

每实例 Machine 放在宿主易失缓存，不写 KV；真实 init/reload 替换缓存，配置成功提交后立即取消旧请求。短期锁只覆盖身份核对/缓存分配，HTTP/DNS/TLS 不占 pluginMu、SQL 事务或注册表锁。探针前后核对当前配置/缓存，卸载重装不能发布旧结果。页面等待同代/同修订的 init/reload 时释放唯一进程槽，让初始化工作器可以真正执行，避免先采样后被 init 清零。宿主额外设60秒总视图预算，源20秒 connect/read idle 语义在专用网络层保留。

App 测试经真实 SDK 子进程、SQLite、HTTP 路由和受控网络契约，覆盖12卡/两表、禁用详情不访问网络但直接 dashboard 可采样、跨工作器速率/缓存、重载清缓存、错误方法/实例/代际拒绝、阻塞请求被配置保存取消，以及等待 init 不持进程槽。最新目标竞态检查（含 SystemProcess 与卸载共享边界）通过86.818秒；正常全量 App 通过74.762秒。全量 Go、modernconly 全量、vet/模块校验通过；前端128项测试、类型检查、格式与生产构建通过。

浏览器使用真正的 Go App/SDK 和受控 Network，两个独立详情请求显示100.00次/秒速率；完整12卡和两表、原单开关配置在1280×720与390×844核对，窄屏无水平溢出。数据是合成 fixture，不能据此声称公网可达或用户配置已迁移。截图：`outputs/plugin-server-status-go.png`、`outputs/plugin-server-status-mobile-go.png`（当前 Codex 工作目录）。网络语义的真实 loopback HTTP/TLS、压缩/字符集/代理认证验证独立于这些 UI 数据。

NAS 独占验证已终态退出0、OOMKilled=false：7个 CGO_ENABLED=0 linux/amd64 测试二进制的本机/远端 SHA逐项相等，App选择154个顶层用例、服务监控纯包8个用例均通过且零跳过；完整元数据/拼音/动漫引擎/RemoveLink纯包也全部通过。容器为alpine:3.22、UID1000:GID1001、network=none、只读根及只读fixture挂载、cap-drop ALL、no-new-privileges、256MiB内存/128MiB临时盘/GOMEMLIMIT64MiB。独占容器和带随机标记的临时目录已清理，原light-media/MoviePilot/qBittorrent启动时间与重启计数不变。结果与二进制校验和见 [plugin-status-nas-verification.json](plugin-status-nas-verification.json)；这证明所选隔离用例，未探测公网状态站或执行正式迁移。
