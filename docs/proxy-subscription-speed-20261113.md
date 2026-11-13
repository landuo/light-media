# 代理与订阅元数据等待优化（2026-11-13）

基线：main `a4396ed176b34977e0b14d4afafb1e553dd6ac5a`。本提交为源码检查点，未部署 NAS，未合并 main；嵌入前端资源须在整合分支重新构建。

## 发现与修改

- 现有「设置 → 系统 → 网络 → 代理服务器」已支持 HTTP、HTTPS、SOCKS5/5h、加密保存和即时切换连接池。本次没有重复增加第二个配置。补充站点代理开关、容器可达地址和 TMDB 测试入口说明；配置读取期间禁止编辑/保存，避免异步加载覆盖输入。
- 季度列表冷请求此前调用完整识别，包括图片、演员、翻译等附加数据；现在仅获取 TV 头部的原始 seasons 数组。已有有效详情缓存则直接复用；保留原始缺失/null字段投影及插件覆盖。
- UI 季详情此前每次直接访问提供方。新增 12 小时原始响应缓存，按影视、季度、剧集组隔离；保存通过原有严格投影校验复用同一响应。英文缺失文案回退、错误身份/重复剧集检查均保留。
- 网页新增订阅此前绕过详情缓存，重新识别原始影视信息。现在原生回退复用既有 24 小时服务器详情缓存，不信任浏览器提交的影视对象。插件仍每次发现和调用，不缓存插件输出。
- `/api/v1/tmdb` 兼容读取保持实时提供方/错误空列表行为，不套用 UI 季缓存。

## 验证范围

- 合成 `TestSubscriptionSeasonLoadingAndSaveReuseNativeResponse`：打开季详情、重开、保存前严格季校验，共 1 次提供方请求；剧集组缓存隔离。
- 合成 `TestSeasonPickerColdRequestOmitsUnrelatedAppendices`：冷季度列表不携带 append_to_response，第二次读取命中缓存，共 1 次请求。
- 合成 `TestSubscriptionCreateReusesServerDetailNotBrowserCopy`：新增识别复用服务器详情而非浏览器伪造标题。
- 前端 `TZ=Asia/Shanghai npm test`：586/586；vue-tsc 与 Vite 生产构建通过（保留旧 hashed assets）。
- `go test -race ./internal/networkproxy ./internal/catalog` 全通过。包括自有 HTTP/HTTPS CONNECT 代理、代理凭据不泄漏至源站、SOCKS5/5h 和取消测试。MoviePilot 固定源码映射参照 `internal/networkproxy/testdata/original-proxy.json`。
- 应用现有代理专项验证 TMDB 实际 CONNECT、图片真实代理响应、站点未勾选直连/勾选使用代理、切换后旧路由被拒绝。
- 真实插件 SDK 识别覆盖/网页新增调用前缀测试通过。并非全仓库测试全绿；扩大扫描的 opaque catalog 测试另需 catalog-event 专用 fixture，初次广泛匹配的代理挂载测试有既有图片 fixture 400，未列为本次通过范围。

这是本地合成/协议与单元验证，不是 NAS 实网测速，未对第三方站点承诺固定提速比例。首次未缓存请求仍受代理/上游速度影响，保存仍执行持久化和必要业务校验，不伪造立即成功。未改订阅 500 条范围、首次检查延后策略、站点/下载器/媒体库系统设置、生产服务或用户数据。
