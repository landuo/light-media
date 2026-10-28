本目录只提供插件平台验收用的自建样本。固定后端为 `8a83efeacd3e26b82be495e17d50146e31c96a60`，固定前端为 `2829e3b08214348f05ff2f5beae644882eccad58`；生成器不导入原版宿主、配置或服务，不修改原版文件。

`generate_oracle.py` 执行 `app/core/plugin.py` 的侧栏聚合和联邦发现原函数 AST，覆盖 Python str/int、Unicode、排序、非法项目、部分追加、渲染解包与异常边界。`generate_dashboard_oracle.py` 执行原 `get_plugin_dashboard` 和原 `Plugin` / `PluginDashboard` / `PluginSidebarNavItem` 类，使用原 requirements.in 允许的 Pydantic 2.13.4，输出完整仪表板 DTO（包括继承默认字段）和侧栏 HTTP DTO 的成功/失败结果。官方授权字段仅保留原始 metadata，不构成认证门禁。

`generate_frontend_oracle.mjs` 从固定 Vue/TS 解析并运行共享配置、保存、最近访问与布局操作原函数；`generate_gesture_oracle.mjs` 运行完整 `usePullDownGesture`，Vue、DOM、时钟和回调边界为自建受控输入。`generate_icon_oracle.mjs` 运行原图像工具与固定 ColorThief 2.6.0 浏览器模块，输入是合成 canvas 像素。目标 npm lock 的 tar integrity 及浏览器 mjs 字节已与固定原版锁/安装制品核对。以上均为源码回放或合成测试，不能替代浏览器和真实 Linux 进程证据。

Go SDK 的 `get_render_mode` 使用双字符串 JSON 数组契约。原 Python 可以解包双字符字符串、双键字典或非字符串成员；这些结果分别保留在 `source-oracle.json`，不误称原版全都抛错。目标拒绝不满足该已声明协议的值，保持普通合法字符串 pair 的返回及错误路径。目标使用已有独立会话/制品校验、不可变 digest 资源路径与现有 Vue/Vuetify 单例；这些是 Go 进程与制品身份的适配，不引入 Python、工作流或 AI。

`ui/` 为零业务能力的真实 Vue federation 资源，由 `cmd/owned-frontend-fixture` 的验收编译开关提供服务端声明。实际共享 Vue/Vuetify injection，包含嵌套 VTextField、VSwitch、VTabs、VDataTable、VDialog、错误与重试和相对 ESM/CSS；通过 `TestPluginUI3133ProductionCLI` 从实际 ZIP 安装。使用 `UI3133_PROCESS_BROWSER_STATE` 可停留在 source2 / clone1，交给浏览器检查，`.restart` 请求真实冷重启，`.done` 继续卸载核查。测试目录和仓库均独占，未触及旧服务。
