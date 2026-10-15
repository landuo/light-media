# HTTPS 反向代理入口

同一份程序同时提供两个入口，无需环境变量：

- 内网：`http://NAS-IP:3120/`，保留原有 `lm_session` 登录会话。
- 外网：`https://PUBLIC-HOST:PORT/light-media/`，反向代理到 `http://NAS-IP:3120/light-media/`。

Lucky 的每一层代理必须完整保留 `/light-media/` 请求路径和浏览器原始 `Host`（含外网端口）。不能启用“使用目标 Host”，不能去掉路径前缀；不要将 `Origin` 改成后端地址。两层代理时两层都应满足此约定。只新增此路径的规则，保留其他应用的规则。

程序将 `/light-media/` 作为固定的 HTTPS 入口：登录 Cookie 为 `__Secure-lm_session; Path=/light-media/; Secure; HttpOnly; SameSite=Strict`，不设置 Domain，与内网会话分开。外网入口须由代理提供有效 HTTPS；HTTP 下浏览器不会使用此 Secure Cookie。程序不信任 `X-Forwarded-Proto`、`X-Forwarded-Host` 等客户端可传入的头来改变会话或来源检查。浏览器写请求的 Origin 必须精确匹配 HTTPS 和保留的 Host/端口；失败返回 403。

静态资源采用相对路径，API、图片、日志流、资源搜索、日志导出和备份下载都使用当前入口的前缀。`/light-media` 自动跳转到尾斜杠路径。日志和搜索使用流式响应，代理需要及时转发 `text/event-stream`，不做响应缓冲；不要覆盖程序的缓存策略。登录尝试按后端看到的连接来源限流，代理后的尝试会共用该代理的限额，不采信任意客户端提供的 IP 头。

验证应同时覆盖有效证书、实际登录、刷新后会话、资源列表与图片、日志流和导出，以及内网原会话仍可使用。内网访问公网域名可能经过回流，不能据此声称已经完成异地网络验证。
