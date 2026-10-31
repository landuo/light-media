# S15 最小完整重建合同（新执行，不冒充旧恢复）

基线：新恢复仓库 `42d6ef0ae3b4697200c13c22b7353de52320af6a`，固定源 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`。本稿已实际读取源 `app/helper/ocr.py`、`app/utils/http.py` 的同步 RequestUtils、OCR_HOST 默认配置与已交42d6的宿主/transport入口。具体文件SHA见同目录 source-and-path-bindings.json。

**现场边界**：旧S15未提交25路径、原日志当前不可达；历史21normal/6race/两host五调用仅历史报告，不用于新候选字节或通过声明。这里拟重建相同有限行为，不声称找回旧文件/哈希，不重建LOST旧目录。当前只写本审计目录，不改产品、不启动Go/Python源导入/HTTP。

## 有限端到端合同

1. SDK入口为 `ocr.get_captcha_text` capability 与 `plugins.SharedOCR.GetCaptchaText`。请求仅四个 optional string：`image_url/image_b64/cookie/ua`；不接收端点、proxy、TLS选项或任意宿主构造。保留源实际 result 值，不能因 Python注解str而强转字符串：使用有限可验证的 typed value envelope，覆盖null/bool/数字/Unicode codepoints/list/ordered object；保留非有限float和未配对surrogate等源JSON语义，错误类别只传已列源异常，任意主机错误脱敏。
2. 宿主 `originalResource` 先沿已交identity/capability/invocation scope校验，再路由新OCRresource；保留42d6已交qB及所有原分发。调用前、每次实际请求/redirect、返回前验证活跃scope；取消/撤销不被误吞成普通网络空值。原 `OCR_HOST` 默认https://movie-pilot.org，在进程启动时固定，可信Options可供自有测试端点；SDK参数不能改。
3. `OcrHelper` 普通算法：先规范化image_b64；非空image_url若有效data:image/...;base64则覆盖；否则GET，非nil空正文直接空字符串，非nil任意HTTP状态的非空正文都转base64，nil保留原fallback。最终无base64则不POST。按Python whitespace移除及Unicode字符数mod4补`=`，不主动base64解码/校验字母表。
4. 固定POST为 `${OCR_HOST}/captcha/base64`、JSON对象 `base64_img`。源Response truthy为状态非400..599；请求失败/HTTP错误返回空字符串；成功用requests响应解码再JSON解析，读取 `.get('result') or ''`。缺失/falsey结果为空，truthy原类型保留；JSON/非object/编码异常保持源类别，不吞为成功。源无session，因此不新增重试。
5. HTTP复用42d6可信 `BoundSiteHTTP` / `RequestsProxyPolicy`、源URL准备、cookie/header/idle/compression工具。只为OCR新入口建立每请求私有transport和无既有1MiB文档截断的OCR响应路径，保留普通Do行为。OCR私有实例按既有用户批准verifyFalse；不改全局、Git、浏览器或未来其他消费者。继承当前environment proxy/NO_PROXY，不清除、不加通用directfallback。已拒请求停该门，原样留证。
6. 带Cookie图片请求仅由宿主确认配置规范origin后发出；跨origin不携Cookie/Authorization/仓库token，返回Set-Cookie按origin隔离；OCR POST不能误带图片Cookie/UA。匿名图片可用普通非凭据路径但仍沿scope/URL/redirect检查。源允许redirect并不授权扩大凭据转发。
7. requests JSON响应编码纯工具从42d6 `iyuuautoseed/site_http.go` 四函数窄提取到新 `internal/requestsjson`，仅必要导出名变化；**不改IYUU facade/业务文件**。编码detector和其已交embed资源复用，不重新复制依赖或展开全编码项目。

## 精确候选路径与小块落盘

机器清单计划22个新路径+3个旧路径（25），只是计划，不承诺重现失踪旧25字节。若实做发现额外必需路径，先报依赖理由，不暗扩。

- **A — SDK/value/core**：`internal/plugins/shared_ocr{,_value,_test}.go`；`internal/ocr/{client,json_value,client_test,extended_source_test}.go`；`internal/requestsjson/response.go`。先落能独立审的实际代码及新增hash。固定原源码抽取的纯算法/确定性fixture观测必须新采集，不能拿新Go输出造oracle。
- **B — 请求transport**：`internal/plugins/autosignin/ordinary_ocr_transport{,_test}.go`，旧 `http_transport.go` 只窄加私有OCR路径/返回content-type/reader与redirect隔离；普通Do差分必须独审。同块落6份新source-observation JSON（helper/status/ownedHTTP/extended/urlprepare/redirect），明确实际采集层级与未执行项。
- **C — App/真实SDKfixture**：新增 `internal/app/plugin_shared_ocr.go`及4个有限测试文件，`cmd/owned-ocr-fixture/main.go`；旧 `plugin_original_resources.go`仅allow/dispatch两hook，旧`runtime.go`仅OCRHost快照/可信Options装配。无需后台任务/Close生命周期新增义务。fixture只能自有固定服务，不放业务AutoSignIn。

每块先保存实际preimage/新文件hash/独立恢复包到**NEW/audits外于Git布局清理的审计位置**，再报父级恢复点；不能等长设计全部完成才落第一块。无clean/reset/sparse布局变更，未验候选不混入交付。父级授予源码窗口前不执行以上写入。

## 新验收边界（待源码窗口/独审/唯一执行窗）

- 小SDK与transport编译先行，复用当前正常工具cache，不重复大server编译；如果新实现输入变化确需重编，只串行且记录实际依赖。旧丢失程序不可视作可复用。
- 原纯算法仅固定文件抽取/自有确定性response，不全量import源应用，不绑定真实凭据/站点。生成新oracle标新日期/命令/输入SHA；已交42d6纯codec资源只验证窄依赖连续性，不全历史回归。
- normal范围沿已核有限21根结构：SDK1、transport5个OCR+7个既有邻接、App7、qB dispatcher邻接1。期望来自原行为，不为凑历史数量制造根；如根组织改变如实注明。
- fresh OCR有限race围绕transport取消/独立TLS/redirect，App snapshot/真实SDKfailure-recovery-cold/credential隔离，约6根。没有旧joint10或历史S15日志代验。
- 新normalserver+真正安装V4式SDKworker+自有固定HTTP服务：标准安装一次、401/405、warm success→typed JSON failure→network-empty→recovery→新host冷启成功，五次OCR调用、无init重放、两host自然Wait0。仅exact `http://127.0.0.1:39123`固定方法path控制客户端、不redirect/传输拒绝重试/改环境代理；同route404→405注册状态轮询与传输拒绝区别记录。产品安装/请求若被当前环境挡住则停此门，不换路。
- 全部门用新source/target/driver/env边界绑定，normal/race/CLI真实原始终态和失败保留。无新UI不代表可豁免实际后台验收；不声称OCR引擎识别效果、S14浏览器、IYUU/AutoSignIn业务、全61或CI完成。

**停止条件**：本轮最小25路径行为完整、相关独审+真实有限门通过后即交协调者精确提交/发布，不无限扩展代理/TLS/全DAO矩阵。
