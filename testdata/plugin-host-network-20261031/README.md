# S14：宿主代理与 GitHub 认证的有限 GET

固定后端 `8a83efeacd3e26b82be495e17d50146e31c96a60`：`app/utils/http.py:329–560`、`app/core/config.py:1069–1087,1155–1164`；安装版普通消费为 RemoteIdentifiers `105–120`（安装版文件 SHA 保存在 host-residual-review）。这里只接必要宿主 GET，词库解析、重试与业务保存不属于本项。

目标为 `internal/plugins/network_host.go` → `internal/app/plugin_original_resources.go` → `plugin_original_host_network.go` → `internal/networkproxy/{requests_merge,requests_transport,requests_timeout,requests_content}.go`。独立 Go 插件 `cmd/plugin-host-network-fixture` 通过真实 SDK 调用，返回状态和原始解压 bytes；没有任意方法、任意请求头或返回宿主凭据的接口。

## 原版行为依据

- `source-oracle.json`：执行固定 RequestUtils 的 AST，10 个状态、空值、二进制及异常场景。未提供 Session，所以不存在 Session 配置的自动重试。
- `vendor-content-oracle.json`、`vendor-headers-oracle.json`、`vendor-edges-oracle.json`：只读使用 NAS 已安装 requests 2.34.2 / urllib3 2.7.0 作纯内存计算。启用 gzip、x-gzip、deflate、br；zstd 保持原 bytes。15 个截断、校验、拼接、多重编码边界由实际原依赖计算。
- `vendor-http-oracle.json`、`wire-headers-oracle.json`：隔离、无外部网络的临时原依赖镜像内访问自建 loopback HTTP，保存实际响应解码与请求头。旧 MoviePilot 服务未运行这些 HTTP 请求。普通请求最终 User-Agent 是 `python-urllib3/2.7.0`，没有 Accept，且含原版 Content-Type、Connection 与 Accept-Encoding。
- `timeout-oracle.json`：固定方法加相同 vendor 版本访问独占 HTTP。标量 timeout 是连接/每次 socket 等待限制；持续到达的响应和重定向没有额外总时限。本地 Python 3.14 与 NAS Python 3.12 分开记录，未声称二者环境相同。

## 已执行验证

`sdk-wire-final-race-r9.jsonl`：实际安装 SDK → App → HTTP；源状态/解码、实际请求头、重定向 cookie、配置撤销、取消、自签证书拒绝及 15 个源编码边界通过。`sdk-timeout-race-r6.jsonl` 和 `timeout-race-r1.jsonl` 分别验证实际 SDK 15 秒空闲失败/16 秒持续响应成功，以及共享 transport 四个原版超时场景。历史失败日志全部保留：zstd、截断 gzip、测试构建输入变化和工具环境失败不改标通过。

`linux-process-r2/result.json`：真实 NAS Linux `cmd/server` **5770c6a3c01f77bf2929937c45d82713cf8f7149b78e2fc427d9bedce12e46a0**，4057 个编译输入前后无变化（`build-server-r3/build.json`）；两个真实 SDK worker 各自有构建清单。35 项观察、80 次真实 HTTP 记录覆盖源响应、22 个编码场景、15/16 秒超时、宿主代理认证、精确 GitHub token 作用域、同源 cookie、跨源去认证、重定向上限、超限/非法请求、1.0→2.0 更新、分身隔离、冷启动和卸载后 API 404。TLS 使用仅此子进程信任的自建 CA，未关闭证书验证。

`linux-process-r1` 是较早主程序 **69b62b0e…** 的原始生命周期证据，`driver.py` 固定当次实际驱动。`browser/result.json` 与六组 AX/PNG 属于这个较早版本：真实配置保存、页面按钮调用、错误保留数据、修正恢复、分身独立保存及浏览器刷新保持。浏览器刷新不冒充服务重启；最终网络 transport 增量由 r2 的实际 Linux 调用验证。两个版本不混作同一二进制。

`cleanup-linux-r1.json` / `cleanup-linux-r2.json` 确认仅本任务测试进程和临时目录已清理。所有源/目标调用均为自建协议站点；没有声称 GitHub 公网或生产插件在线验收。

## 必要适配与限制

1. Go 独立进程使用有限、版本化 DTO；encoded/decoded/JSON 传输均有上限。配置撤销和取消返回明确宿主错误；普通网络/解码异常按源返回无响应。
2. 项目既有凭据保护优先：只在精确官方 HTTPS origin 注入全局 GitHub token；离开来源后永久移除。源消费方的 URL 子串判断不复制；不套用仓库 token 或镜像路由。
3. 项目既有 TLS 证书验证继续保留。固定 RequestUtils 的 `verify=False` 不复制。实际受信自建 CA 正例和不受信自签证书反例分别留证。
4. Go `net/http` 会规范化 HTTP header 末尾空白，requests 保留 `Content-Encoding: gzip ` 并返回压缩原 bytes。`vendor-http-oracle.json` 同时保留源原始结果和独立执行的规范化 header 源结果；只有此 HTTP 场景使用后者比较。原始 decoder 单测仍对照未规范化源结果。这是可观察的 Go HTTP 解析差异，未隐藏或声称逐字节严格等价。

独立审查记录为 `downloader-independent-review.json` 及后续增量；最终清单必须绑定实际审查与执行版本，不能只更新旧报告哈希。
