# 新建、安装和使用一个 Go 测试插件

当前插件平台已推送远程 `main`，产品提交 `f8af18cc`，约定范围65/65验收及555/555回归通过。新增宿主能力已随源码交付；本轮未执行生产部署。准确版本、提交与远程读回见[发布记录](plugin-module-main-publish-20261105.json)，源行为和必要适配见[唯一对照表](plugin-module-parity-matrix.md)。

完整示例在 [`cmd/hello-test/main.go`](../cmd/hello-test/main.go)，打包工具在 [`examples/hello-test/build.mjs`](../examples/hello-test/build.mjs)。示例没有定时任务：只在点击“运行一次”时保存一句话并增加计数。

## 1. 插件代码放在哪里、写什么

目前 SDK 位于 `internal/plugins`，尚未发布独立的公共 Go SDK。按 Go 的 internal 规则，先在 light-media 仓库的 `cmd/hello-test` 中开发、编译；编译后的插件是独立可执行文件，无须注册进 builtin catalog，也无须随主程序一起编译。

| 代码位置/方法 | 示例中的职责 |
| --- | --- |
| `manifest()` | 声明 `HelloTest`、版本、宿主能力、页面与 API。 |
| `get_form` | 返回“启用插件”和“测试消息”两个 Vuetify 字段及默认值。宿主负责保存配置。 |
| `get_state` | 返回配置中的 enabled；安装、保存与重启不执行测试动作。 |
| `get_page` | 返回当前计数、最近消息和“运行一次”按钮；不需要另建前端工程。 |
| `http` | 处理 `/status` 查询和 `/run` 手动执行，调用 Base 保存当前实例的数据。 |

配置读取用 `plugins.Base{Host: host}.GetConfig(ctx)`；数据读写用 `GetData`、`SaveData`。不传额外 plugin_id 即使用当前实例的命名空间。页面按钮的实例 ID 取自 `GetInstanceMetadata`，因此来源插件和分身使用相同代码，配置与计数由平台隔离。

`init` 用于初始化必要资源，`stop_service`/`close`/`stop` 用于释放插件自己启动的资源。此示例没有后台资源，所以这些方法不执行动作。`get_service`、`get_command` 返回空列表表示该示例没有普通服务和命令；需要时可以按 SDK 增加。

**普通日志写 stderr；stdout 留给 SDK 通信。不要保存 HostCaller 后在本次调用结束后继续使用。**

## 2. 编译并生成仓库

需要开发环境的 Go 1.26+ 和 Node.js；安装后的插件只依赖生成的 Go 可执行文件，不依赖 Node 或 Python。

在 light-media 仓库根目录执行：

```sh
node examples/hello-test/build.mjs
```

默认输出 Linux AMD64：

```text
work/hello-test-repo/
├── go-plugins.v1.json
└── hello-test-0.1.0-linux-amd64
```

Linux ARM64 使用：

```sh
node examples/hello-test/build.mjs work/hello-test-repo linux-arm64
```

打包工具自动编译二进制、读取实际 manifest、计算 SHA-256/大小，最后生成索引。索引包含版本、manifest、平台、二进制相对路径和 `light-media.sdk-base/v1` 宿主契约。无需手工填写哈希。一次输出一个平台；选择运行 light-media 的 Linux 机器对应架构。

## 3. 在页面安装

1. 将整个 `hello-test-repo` 目录放到 **light-media 服务能够读取的服务器目录**，例如 `/opt/light-media-plugins/hello-test-repo`。使用 Docker 时，把该目录挂载进容器，后面的设置填写容器内路径。
2. 打开“设置 → 系统 → 插件 → 开发”，将该目录填入“本地插件仓库路径”，保存。已有路径时追加，多个目录用英文逗号分隔。
3. 打开“插件 → 插件市场”，刷新后找到“你好测试插件”，点击安装。

目录本身必须包含 `go-plugins.v1.json` 和它引用的二进制文件。**保存本地路径不会自动安装插件；不需要开启“插件热加载”才能首次安装。** 主程序安装时会检查平台、宿主契约及哈希。

## 4. 使用、更新与卸载

1. 在“我的插件”打开“你好测试插件”的设置，开启插件，填写测试消息并保存。
2. 打开插件数据/详情页，点击“运行一次”。页面自动刷新：计数从 0 变为 1，最近消息变为填写的内容。再次点击变为 2；关闭弹窗或重启服务后计数保留。
3. 可通过平台创建分身，在分身中修改消息并运行。分身按钮指向分身自己的 API，来源计数不变；插件不自行创建或管理分身。
4. 更新时修改代码与 `const version`，重新打包并替换服务器仓库文件，然后在插件页面执行更新。开发联调可选择开启“插件热加载”；它监控已构建制品，修改 `.go` 文件后仍需重新编译。
5. 不再使用时，打开插件卡片菜单选择“卸载”并确认。

按当前源行为，**来源插件卸载会停止运行并移除已注册 API，但保留配置和 KV 数据**；重新安装可以读回计数。如果要清空此示例，先“重置”，再“卸载”。分身卸载会清理该分身的配置与 KV，不影响来源。宿主不会删除你编写的源码或整个外部本地仓库；此示例没有自行写入数据目录文件。

## 验证示例

```sh
go test ./internal/app -run '^TestHelloExampleLocalInstallRunCloneRestartResetUninstall$' -count=1 -v
```

该测试实际调用打包脚本、配置本地仓库、安装独立 SDK 进程，经公开路由验证禁用时拒绝执行、配置保存、计数、分身隔离、App 冷重启、卸载后 API 404、来源重装保留计数、重置清零与最终卸载。测试使用工作树下独占临时目录；不会改现有服务。

Linux 产物编译与开发机真实 App/SDK 路由验证分别记录；该示例没有另行宣称 Linux 浏览器实测。插件平台本身的实际Linux/浏览器验收及准确版本边界见[本轮封存](../testdata/plugin-platform-final-20261104/final-completion.json)。

## 插件主动调用宿主（2026-10-31工作分支）

调用现有宿主能力的插件使用制品契约 `light-media.sdk-resources/v1`，并在实际编译的 manifest `Capabilities` 中逐项声明使用的常量。`sdk-base/v1` 只开放基类；改仓库索引或抄另一个插件ID不能扩大当前调用权限。宿主用当前对象、实例、版本和调用上下文核验，停止、卸载、替换或调用结束后旧HostCaller失效。

例如，在插件当前handler中枚举一个已配置下载器的已完成任务：

```go
// manifest: append(plugins.BaseMethods(), plugins.DownloaderCompletedCapability)
result, err := (plugins.Downloaders{Host: host}).Completed(ctx,
    plugins.DownloaderCompletedRequestV1{DownloaderID: downloaderID})
if err != nil {
    return nil, err
}
return json.Marshal(result)
```

`Completed`省略Hash查询已完成任务，显式空数组查询零项；原`Tasks`仍要求明确的Hash数组，不能用null扩成全部任务。控制操作逐个选择准确Hash，`remove_task`始终保留下载文件。未知写入结果必须先核对下载器实际状态，不能自动重发添加/删除。`DeleteFiles`是独立入口，要求非空准确Hash、宿主认领及本地授权目录，保护其他任务引用、硬链接和符号链接；检查`Outcome`、`DaemonApplied`和`BookkeepingCompleted`，外部删除成功而本地记录失败时也不能重发删除。

| SDK对象 | 当前有限入口 | 用法边界 |
| --- | --- | --- |
| `Downloaders` | Connection、Completed、Downloading、List、Trackers、Add、Control、Tags、FindByTag、DeleteTags、DeleteFiles | 复用宿主连接；Add支持bytes、magnet或URL及必要Cookie；新公共能力遵守下载器启用状态。材料及私有URL不得写入日志/公开数据页。 |
| `Sites`、`Subscriptions`、`History` | 站点读取/账户/浏览/RSS/材料/普通请求，显式私有凭据；订阅读取/patch，历史读取/仅记录删除 | 使用稳定宿主ID及查询返回的资源ID；不开放数据库；普通请求仅对配置规范来源，不把Cookie/passkey作为普通配置返回。 |
| `SystemSettings` | Get、Set | 只开放明确的普通配置键；USER_AGENT是只读宿主HTTP身份；null、省略、空数组语义分开；不开放AI/工作流配置。 |
| `Storage`、`MediaHost` | 受管文件exists/list/delete，媒体服务器列表/刷新，普通影视识别 | 宿主目录安全检查仍执行；媒体刷新返回接受状态，不等于索引已完成。 |
| `PluginHost` | Read、States、ControlRequest | 实际运行对象与调度由统一平台管理；自停止/重载等延后当前调用结束，检查`Accepted`与`Completed`，不要把已接受当已完成。 |

会重入插件`get_module`或`get_state`的调用需要`plugins.ServeConcurrent`，并同步保护插件自身共享状态。串行worker不能通过持有HostCaller或另起无上下文goroutine绕过重入限制。来源/分身的配置、KV和数据目录仍隔离；站点、订阅、系统词库等宿主资源本来就是共享的，不应虚构每个分身一份。

备份文件读取使用显式配置、挂载的目录和普通Go文件API；临时文件放`Base.GetDataPath`返回的实例目录。该目录隔离不是操作系统沙箱。不要根据插件名称猜NAS路径，不重复实现分身管理。标准文件验收插件见`cmd/owned-local-files-fixture`；其余`cmd/plugin-host-*-fixture`均为验收用例，不作为已迁移业务插件发布。

`Downloaders.Connection`即时核对宿主保存的连接，返回`inactive`；它不是启用开关。停用、无效配置或调用失效返回错误。变更操作的`FailureCode`若为`CALL_CHANGED_AFTER_MUTATION`，须结合`Accepted`/`Outcome`核对实际副作用，不能把调用被撤销理解为外部未执行。

普通远程文本使用`plugins.Network{Host: host}.Get`，在manifest声明`plugins.NetworkGetCapability`。请求仅接受HTTP(S) URL和`GitHubHeaders`选择；宿主使用自己的代理，且仅向精确GitHub官方HTTPS来源附加全局令牌，不使用插件包镜像和仓库专用令牌。返回`Response == nil`表示网络或解码失败；非空响应保留非200状态与原始字节，插件自行判断内容。此入口不返回凭据、Cookie或任意响应头。

普通站点 HTTP 使用 `plugins.Sites{Host: host}.Request`，声明 `plugins.SitesRequestCapability`，传准确站点 ID 和相对路径，选择 GET/POST、query、form、JSON 或 bytes。宿主附加当前 Cookie/UA/代理，重定向保持同源。检查 `Response`、`RequestState` 和 `ScopeChanged`；POST 结果未知或写后调用变化时先核对副作用，不自动重发。响应 `Cookies` 保留原响应 Cookie 的有序 name/value，`CookieOverride` 省略使用配置 Cookie，显式空数组清空，本次提供的值替换默认值。内容和 Cookie 属于私有材料，不放进配置、数据页或普通日志；插件不自行管理分身身份。

源版 `SiteOper` 允许可信插件读取凭据。需要在 Go 中判断凭据是否存在、计算签名或发送原协议时，单独声明 `plugins.SitesCredentialsCapability`，调用 `Sites.Credentials(ctx, siteID)`；返回当前站点的 Cookie、UA、APIKey、Token，仅通过私有 SDK 通道供本次调用内存使用。普通 `Sites.Read`、公共配置页仍只返回安全投影。不要序列化整个私有结果；Cookie、APIKey、Token 不写配置、KV、日志、公共插件 API、数据页或报告；既有可编辑 UA 保持原行为。请求仅发往用户配置的规范来源，不向跨域重定向转发。标准 Go 可执行插件是可信代码，此接口不提供操作系统沙箱。普通算法由插件用 Go 完成，平台不增加凭据表达式或业务签名框架。

普通 Go 代码向插件自己配置的 URL 发请求并需要宿主代理时，单独声明 `plugins.NetworkProxyReadCapability`，调用 `plugins.Network{Host: host}.ProxySettings(ctx)`。返回 `Proxies` 对应固定源的 nullable `settings.PROXY`，`Environment` 仅含代理变量及 CGI `REQUEST_METHOD`，保留大小写、空值和 host-specific 代理键。将二者交给现有 `internal/networkproxy.NewRequestsProxyTransport`，保持 requests 的环境合并与 `NO_PROXY` 行为；不要用 Go 缓存式 `http.ProxyFromEnvironment` 替代。代理 URL 可能带账号密码，结果只在私有调用内存使用，不保存或公开。此入口提供普通传输依赖，不实现通知通道业务。

普通验证码识别使用 `plugins.CaptchaOCR{Host: host}.Text(ctx, request)`，声明 `plugins.CaptchaOCRTextCapability`。图片可以是现有站点 ID 下的图片路径、data image URL 或 base64；远程图片请求使用宿主保管的站点 Cookie、UA 和代理。OCR 服务地址来自宿主 `OCR_HOST`，结果保留原版 JSON 值；网络失败或无有效结果返回空字符串，响应 JSON 格式或类型错误按源行为返回错误。这项辅助不包含模型、Agent 或工具调用系统。

需要站点 JavaScript 执行后的 HTML 时，声明 `plugins.SiteRenderCapability`，调用 `plugins.RenderSite(ctx, host, plugins.SiteRenderRequest{ID: siteID, Path: "/owned-page"})`。`HTML == nil` 是渲染失败，非空指针也可能指向空字符串。正式 Dockerfile 包含已核摘要的普通浏览器与 Xvfb 运行库；不以普通 HTTP 页面替代真实 JavaScript 渲染。插件不接收浏览器句柄、站点凭据或任意动作编排接口。

渠道插件接收消息时，声明 `plugins.MessageReceiveCapability`，使用 `plugins.Messages{Host: host}.Receive` 提交原始 body 与可重复的 form/args。平台调用普通 `message_parser` 模块，再处理输入回复、历史、处理中状态、注册命令、普通 `user.message` 和插件回调。`Accepted` 仅表示非持久后台调用已接收；处理结果由插件数据、事件和安全错误记录观察。来源和分身共享普通广播，各自的配置、数据和 parser 来源选择由统一实例身份隔离。

精确类型与当前支持的参数见`internal/plugins/*_host.go`；源行为、必要适配、尚未验证/未实现部分及实际证据统一记录在唯一对照表S01–S19，不以本节入口列表作为完成证明。
