# 插件平台 23/24：安装统计与评分

## 固定输入和交付边界

- 源后端：MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`；前端：MoviePilot-Frontend `2829e3b08214348f05ff2f5beae644882eccad58`。
- 独立实现树：`work/usage-rating-20261030`，基线 `122fafc7b4ee93589250cb82be849fead48e79d1`。这里是候选，不表示已集成、推送、CI 或浏览器验收。
- 复用现有 App、管理员会话、SQLite、安装清单、市场安装事务、真实 SDK worker、中心 HTTP 客户端、启动上报与持久标记；不重写它们，不引入 Python 运行依赖。
- AI、工作流、外部存储及暂停业务插件不在本片；文件夹 27 的独立差异不混入本片。

## 源入口 → 完整宿主链

1. `app/factory.py` 注册同一 plugin router 的 v1/v2，v2 middleware 改变普通 JSON 与异常信封。
2. `app/api/endpoints/plugin.py`：GET `statistic`、GET `rating`、GET/POST `rating/{plugin_id}`；请求/返回模型在 `app/schemas/plugin.py`，通用 Response 在 schemas。
3. `app/helper/server.py`：async 获取统计/单项/批量评分，POST 评分，安装上报，`init_plugin_report`、`sanitize_plugin_repo_url`、UID 与 User-Agent。中心是外部黑盒，只迁移可观察客户端合同，不声称重建其聚合算法。
4. `app/core/plugin.py`/启动装配 → `init_plugin_report` → 原 installed 清单 → 系统配置 DAO 标记；仅 HTTP 200 写 `"1"`。空清单不标记，失败可重试，冷启动保留成功标记。
5. 安装 endpoint 成功链 → 安装上报；已有代码非 force 的成功分支也上报。共享开关关闭时统计/上报不外发，评分不受该开关限制。
6. 固定前端 `PluginCardListView.vue` 获取统计、排序/显示安装数，原生安装动作触发上报。固定前端没有评分控件，不新增虚构评分 UI；评分仍有公开 API 及插件 Axios 入口。

## 路由与输出

| 入口 | 合同 |
|---|---|
| `/api/v1/plugin/statistic`、`rating`、`rating/{id}` | 原 v1 完整 body/status/Content-Type/method/Allow/307 Location |
| `/api/v2/plugin/...` 同上 | 实际源 v2 middleware 信封；不是把 v1 期望套用到 v2 |
| `/api/plugins/...` | 既有 Go 扩展保留 native data/request_id 外包；业务与校验复用同一实现 |
| 根 statistic/rating 的 PUT、DELETE | 原版选择通用 `/{plugin_id}` 配置/卸载链（29/17），不被本片截成 405；Axios 保留既有适配 |

源兼容入口不经过 native 外包归一化。源普通失败 500 是精确纯文本 `Internal Server Error`；422 使用实际 Pydantic detail，v2按原 middleware 转换。POST业务失败保持 HTTP200+success:false；未安装使用原清单成员判定，不以 installation row 或运行对象代替。

请求 multiple_of、边界顺序、布尔、浮点精度、整数字符串与大整数、缺省/null、合法 JSON 非对象、坏 JSON、NaN/Infinity/溢出均取真实源。未知 rating 模型字段忽略，泛型 Any 不错误忽略。JSON bytes 包括 UTF-8 BOM、UTF-16/32 大小端有/无 BOM、WTF-8 surrogatepass；普通非法 UTF-8 拒绝。中心无效 JSON 的统计异常、评分 fallback 与 schema 异常分开。共享 `originalRatingFloat` 保持未改，避免影响服务进度/浏览器存储。

## 明确保留的安全边界

- 已授权 host 管理员/session、同源和路径校验不放宽；不重现源外部 Token 的跨权限放行。
- 既有请求/中心响应 2 MiB 预算、中心地址无 userinfo/query/fragment、UID 不跨中心 origin 重定向不放宽。
- 非 local 仓库上报仍清除 userinfo/query/fragment。凭据不外发的安全边界保留；普通非敏感 repo query 也被清除则是仍未批准的产品差异，本轮不修、不声称严格等价。local 仅保留编码 ID/version，绝不发送本机 path；异常 local userinfo 同样清除。
- Error() 返回固定安全文本，日志不拼接源异常、请求或中心原始内容；源 422 对调用方反映输入，不复制到日志。
- 未执行旧被拒 inode、权限、替换、TLS/CA/CONNECT 探针；无生产、NAS 或真实凭据。

## 原程序证据与复现

`testdata/plugin-server-20261030/source-http.json` 是 230 个正常导入原 factory/router/helper/schema/SQLite、真实 owned HTTP 的观察；`source-wire.json` 是真正 v1/v2 的 660 个 HTTP 观察。它们由同目录脚本原程序运行产生，不来自 Go 输出。每份记录固定源、依赖版本、驱动和完整原运行结果 hash。保留 v1–v6 驱动与旧失败日志，未覆盖失败。

脚本运行方式：

```
<已有隔离 venv>/bin/python testdata/plugin-server-20261030/source_http_wire_replay_v6.py <全新独占输出目录>
```

驱动顶部记录当前固定源及只读依赖路径；若迁移环境，应显式重绑定这两者，不修改原源或安装共享依赖。依赖实测 FastAPI 0.136.3、Pydantic 2.14.2、pydantic-core 2.50.2、httpx 0.28.1、cachetools 7.1.8。范围外媒体/AI等依赖列在结果 `fixtures`；协议重放使用合成 UID（UID真实算法另由独审实际源与冷报告验证），API使用测试principal，不声称执行完整 daemon/JWT/真实浏览器。进程中 socket.connect 只允许 owned center，网络拒绝记录是该边界观察，不是 OS 沙箱证明。

## 目标验收入口

正式 Go tests：
- `plugin_original_server_source_test.go`：230 native 比较和 660 v1/v2 原 wire 比较，真实 App/Handler、SQLite 和 HTTP center，完整 schema/状态/副作用；仅 native 扩展测试明确拆 native data，v1/v2不拆。
- `plugin_original_server_lifecycle_test.go`：原清单独立授权、预算/会话/同源、共享有限浮点 helper；五个新进程重开 SQLite 并走真实启动同步函数，空清单、关闭共享、失败、重试成功、冷成功不重报，UID稳定；不声称完整服务器定时器已在该测试执行。
- 同文件实际安装测试使用真实 `cmd/owned-standard-fixture`、普通仓库、下载/安装/加载链：首次成功、已有安装成功均上报；关闭共享和材料失败不报。
- `plugin_original_server_shape_test.go` 更新旧 502 断言为真实源 500；旧普通 HTTP、cold settings、retry/UID、固定 Pydantic oracle 一起回归。
- `web/tests/plugin-axios-source.test.mjs`：实际 Axios adapter 观察 v1/v2 和完整 method，不只字符串断言；保留原根 PUT/DELETE，原 data/request_id 不误拆，显式 native 仍按 native 适配。

Go 复现需构建 owned worker 后设置 `OWNED_STANDARD_FIXTURE_BINARY`，使用 `GOPROXY=off GOSUMDB=off GOFLAGS='-mod=readonly -buildvcs=false'`，清空测试代理和 MP_SERVER_HOST。精确白名单命令/编译闭包/前后哈希/二进制/log/exit 保存于 `work/usage-rating/evidence/final-01`；不要用宽泛 OriginalPluginServer 正则重跑旧禁止探针。

## 仍需交付门

独立审查使用自己的原源 220 API 与报告/cold观察复验候选。之后由协调者将窄 http.go、Axios 分支人工融合已有 service wire，运行联合后端与全 web/build，并在官方 4173 preview 用原生安装/统计入口完成 UI 观察。未闭合这些门之前，不把本文件或 backend通过称完整交付。


## 候选 03：源 QueryParams 与多尾斜杠

基于候选02逐文件 before hash 的窄增量，不覆盖02成果/证据。统计、评分根及
评分 item 的兼容分类一次去除全部尾 `/`，响应仍由后端生成一次空307，原 query
字节不变；前端只用 canonical 名称判断 adapter，不改发出的 path/query。
根 PUT/DELETE 继续走原有通用配置/卸载分支；带尾 PUT/DELETE 的17/29源等价
尚未在本片验收，不把保持既有分流说成已经源等价。

固定 v1/v2 rating `plugin_ids` 使用独立纯Go源 QueryParams解码：`&`分隔，末值
优先且保留空值，semicolon为数据，坏percent保留字面，非法UTF8按源replacement。
算法复用已冻结service wire2，但本组实现不引用该未提交依赖，不改全局URL.Query、
token权限或native扩展的旧解码规则。统计/报告安全规则、2MiB预算均未变。

`source-query-slash.json` 来自独审 source08 真实完整源116项、52次owned HTTP；
原结果SHA256 `aa55698a72a6c5ce1f7794f3ce8773cd3dfb769373836a6af61a1d6781a599f1`。
`source_query_slash_replay.py` 正常导入固定factory/router/helper/schema/SQLite，
参数指定现有source/dependency根；固定101源码hash/commit、重放116及QueryParams38
并与原始oracle全等比较，不根据目标生成期望。UID仅记存在/长度/一致性。
该驱动保留源鉴权dependency/未用集成/无daemon边界，只有独占本地HTTP peer可连。

`TestOriginalPluginCenterQuerySlashSource116` 使用真实TCP→App.Handler→SQLite→HTTP
peer，比较原始输入query字节、52次peer完整URI/body、响应status/Allow/Location/
Content-Type和完整JSON或精确空body。额外断言尾slash鉴权/跨站拒绝无peer副作用，
root PUT/DELETE不被本兼容器吞掉。`TestOriginalPluginCenterQueryParamsSource38`
单独比较真实Starlette标量解码，包括截断UTF8/原始Latin-1字节。
原10根（含230/660、实际安装SDK、五次独立冷进程）保持并与新增2根一起normal/race。
精确执行与before/after绑定在 `work/usage-rating/evidence/final-03`。

复现新增源：
```
<现有Python>/bin/python testdata/plugin-server-20261030/source_query_slash_replay.py \
  --source-root "$SOURCE" --dependency-root "$DEPS" --output <全新owned目录>
```
`--dependency-root`可按优先级重复；不安装依赖、不增加生产Python。
`OWNED_USAGE_QUERY_OBSERVATIONS`可保存目标116条观察，不保存UID本体或认证cookie。
此候选仍需独审、主树共享入口/adapter集成及浏览器授权后的实测，不表示完整23/24交付。
