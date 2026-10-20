# requests HTTP 代理认证

当前为共享传输修复，尚未部署；实际插件迁移数量仍为 2/23。

以原 MoviePilot 容器中 requests 2.34.2 的 HTTPAdapter.proxy_headers 和选定 RequestUtils 方法为只读依据。HTTP/HTTPS 代理的用户名、密码按 Latin-1 构造 Basic，forward 与 CONNECT 均在自有 HTTP/HTTPS 代理上逐字节核对；空用户名、没有冒号的用户名、百分号编码、Unicode 和空密码遵从来源行为。SOCKS 保持原 UTF-8。仅对代理 authority 中原始 Unicode userinfo 补齐 urllib3 对应的转义，不宣称实现 requests 的所有任意 URL 解析差异。

超过 Latin-1 的认证字符在联网前返回固定编码错误，错误不含地址或凭据。公共传输保留该分类，订阅统计明确记录 UnicodeEncodeError、not_requested，不将异常伪装成原版 False 或 None。原服务只读探测的 32 个头部用例和 3 个实际 RequestUtils 异常用例均无网络、文件写入或子进程尝试；只使用合成认证值。

受影响三个包的普通和竞态检查各通过 36 项（包含子测试 3036），无跳过；vet 和 Linux amd64 CGO=0 服务构建通过。初轮发现的 username-only 与原始 Unicode 解析失败日志保留，最终输入冻结后未修改。详细计数与输入摘要见 [验证记录](requests-http-proxy-auth-verification.json)。
