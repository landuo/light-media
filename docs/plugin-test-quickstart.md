# 新建、安装和使用一个 Go 测试插件

适用于包含完整 Go 插件平台与 HelloTest 示例的版本。main 发布及 NAS 部署状态见[发布记录](plugin-module-main-publish-20261029.json)；旧部署不能直接按本文验证。

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

Linux 产物编译与开发机真实 App/SDK 路由验证分别记录；该示例没有另行宣称 Linux 浏览器实测。插件平台本身的 Linux/浏览器验收见[最终报告](validation/plugin-module-final-20261029.json)。
