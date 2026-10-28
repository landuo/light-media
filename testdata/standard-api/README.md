# 固定插件 API 对照与验收夹具

固定后端为 `8a83efeacd3e26b82be495e17d50146e31c96a60`，固定前端为 `2829e3b08214348f05ff2f5beae644882eccad58`。本目录只包含平台验收用零业务插件与源函数回放，不修改原版，不导入旧宿主配置或服务。

| 固定依据 | 目标 | 验证 |
| --- | --- | --- |
| `B/app/core/plugin.py:873` getter 与 `B/app/api/endpoints/plugin.py:130–253` 移除旧路由后注册双版本，匿名/默认 key/bear、方法与异常 | `plugin_original_api_methods.go`、`plugin_custom_api.go` | `source-oracle.json` 的原 manager/route AST + 实际 FastAPI 注册；Go source tests 与实际独立生产 CLI |
| `B/app/api/endpoints/plugin.py:764–839` 源代码文件权限、类型、inline 文件流、Range 被忽略 | `plugin_original_static_http.go`、`plugins/artifacts/package_resource.go` | `static-source-oracle.json`、真实受管包字节读取/分身更新及卸载 CLI |
| `F/src/api/index.ts:8–92` 创建 Axios、`window.MoviePilotAPI`、请求认证、无条件 `response.data`、错误 | `web/src/features/plugin-remote-api.ts` | `frontend-axios-oracle.json` 实际固定 Axios1.9 + 固定初始化/拦截器 + 受控 adapter 18 例；目标实际 Axios 与 Vue SFC 测试；新浏览器待验 |
| `F/src/utils/requestOptimizer.ts:33–83` 不覆盖显式 signal，自动请求加入 Set、响应清理；`F/src/router/index.ts:284–286` 导航中断 | `abortPluginAPIRequests`、`App.vue` 真实 `change/expired` | 目标真实 Axios 自动/显式信号测试与既有组件卸载测试 |
| `B/app/core/plugin.py:2140–2200` 分身复制/重写前端插件标识 | 平台中央 JS/CSS/资源名派生；global及props.api均保留请求的真实标识 | 实际 source2 / clone1 测试夹具；新浏览器待验 |

## 必要适配

- 目标后端保持 Go SDK 进程协议；Python 任意 callable/Response 对象不作为 Go 内存对象运输。已实现原始请求字节、普通 JSON、二进制、204/304、文件流及原始错误状态。认证/参数/路径仍由实际后端路由验证。
- 固定前端 bearer 账户会话改用已有 HttpOnly 单用户 Cookie。`bear` 路由的 native 与原 v1/v2 别名均接受相同同源会话；来源校验保持。`apikey` 仍需实际 key，显式错误 bearer/key 优先级不变。会话 token 不暴露给前端。
- 受信安装代码可调用已有普通同源宿主与显式其他插件 API，Axios wrapper 不发明按插件的权限边界。`plugin/file`、`remotes`、`sidebar_nav`、自定义 API 保留源 v1/v2 wire。目标未提供源别名的表单/数据页/仪表板复用现有 native 路由并只解开 Go 的 `request_id/data` 宿主包裹，不解插件业务 data。
- 分身按固定顺序派生真实 JS/CSS/资源名；Axios 不按 owner 改写请求路径，运行时拼接出的源 URL、明确其他实例和全局 API 均保留请求身份。原版返回 HTTP200 `success:false` 或 `error` 字段仍 resolve，调用方可读取真实结果；不将其改为 transport reject，也不据此宣布业务成功。HTTP 非成功状态保留 AxiosError/response.data。
- 固定锁定 Axios 为1.9.0。原 `yarn.lock` 与目标 npm lock 的 tar integrity 及安装 `lib/axios.js` 字节已核对。使用该库的 callable、request、defaults、interceptors、参数编码、转换、timeout、blob/arraybuffer 等；不是手写 fetch 模拟。
- 目标使用 Axios 的 fetch adapter，把 redirect 设为 error，阻止凭据或提交体跨源转发。拒绝跨源 URL/baseURL；同源绝对 URL、baseURL、普通相对接口正常支持。fetch 的 ETIMEDOUT 映射至源超时错误文案，现有会话401/403触发宿主退出事件。目标无独立全局离线状态计数系统，不为该插件请求依赖迁移新业务。

`ui/remoteEntry.js` 由 `cmd/owned-api35-fixture` 声明为真实 federation 配置/数据页：实际调用 API、字节 SHA、原始体、空响应、503，以及实际 Axios callable/request/host/peer/global/absolute/baseURL、200业务 false、源 file 流。`TestPluginAPI35ProductionCLI` 从 ZIP 安装 source1/更新 source2/保留 clone1，实际重启和卸载核查；`API35_PROCESS_BROWSER_STATE` 只用于交给浏览器操作的 opt-in hold（`.restart`/`.done`）。受控源码回放、真实 CLI、浏览器分别记证，未运行的新客户端浏览器不算通过。

## 2026-10-27 重开后的新增待纠正项

完整固定克隆源码会在原 `dist` 中修改所有 JS/CSS 的 literal 标识及部分顶层资源名；此前 owner 前缀绑定不能覆盖全局 `window.MoviePilotAPI`、CSS 和资源文件，也会误绑定运行时拼接出的源 ID。该策略已撤除。平台集中前端派生与源码回放在 `testdata/standard-clone-frontend/`，目前已落地并通过源9树/实际SDK/包测试，仍须在新共享main完成实际浏览器，不凭局部测试验收14/15/31或完整35。

已有 `plugin_organization_compat.go:84` 提供原 `v1/v2/plugin/folders/**` 和 `v1/v2/user/config/PluginOrder`，client现已优先保留这两个源别名及其原wire，受控实际Axios与新真实CLI已核对；与集中clone派生一起绑定21cbbd9b新main，浏览器尚独立续验。旧f37实机进展只按其对应代码计证。

## 串行SDK请求取消的实际失败与修复

21cbbd9b的真实浏览器已证明JS/CSS/资源名/全局与运行时身份、分组排序wire及AppPage fallback；全局慢GET被实际App导航中止时，又发现旧串行RPC把HTTP context取消等同于整个长期worker终止。浏览器返回后clone descriptor/Page500、API502；独立同main自动250ms取消门再现失败，不能仅把fixture切换成Concurrent掩盖。

目标原模块 `callSession` 现对已准入的串行RPC排空响应，不把一次HTTP断连当作停止插件；保留既有runner预算、会话/应用stop，不自动重启或初始化。已协商并发SDK保留单call取消，generic legacy SDK自身“半截协议不可复用”的契约不改。新真实CLI在取消后要求同PID/init/config以及frontend/Page继续可用，随后由浏览器核对实际导航离开/返回。旧21cb失败及局部进展单独归档，下一main尚未通过前不升级35。
