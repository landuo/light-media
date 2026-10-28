# 历史有限验收基线

当前任务与唯一状态表见 [任务说明](plugin-module-continuation-20261025.md) 和 [50 节对照表](plugin-module-parity-matrix.md)。下方旧 G16/G17 已按用户要求排除，不计入当前未完成项或阻塞；旧组不再作为当前任务总数。

---

# 插件框架有限验收基线（2026-10-24）

## 当前范围和状态

框架整体仍未完成。本阶段只实现插件模块的通用能力；具体插件业务恢复、通知渠道、卡片、provider 协议和系统业务补建均暂停。已有 core 可通过薄接口调用；缺失 core 必须明确依赖不可用，不能以模拟发送或新增渠道冒充框架闭合。

旧 queue/revision/receipt/native recovery 文档属于已退役框架的历史材料，不再作为标准插件开发要求。当前运行入口为统一 original manager、版本化标准 SDK 制品及 instance-bound Base。分身、重置、更新由模块统一负责。

## 固定 28 组

以下是有限范围，不是 28 组均已通过的声明。每项关闭必须核对固定原源、真实入口和最终同字节证据。新增发现必须归入既有组并标明遗漏、实现缺陷、测试配置错误或外部 core 依赖，不继续增加模糊的“最后几项”。

- G01：Lifecycle/load/start/stop/reload/order
- G02：Manager and Base configuration
- G03：Plugin KV data and owned data paths
- G04：Runtime getters/attributes and display identity
- G05：Custom API declaration/registration/invocation
- G06：Form and Page
- G07：Dashboard metadata and dashboard content
- G08：Vue frontend artifact/assets
- G09：Sidebar navigation
- G10：Management inventory and folder endpoints
- G11：Install/update/acquire/backup/restore/clone package
- G12：Local-repository development and file watch
- G13：Automatic installed-plugin repair/update synchronization
- G14：Clone/reset/uninstall management semantics
- G15：Module override extension registry
- G16：Workflow action extension registry
- G17：Agent tool extension registry/cache
- G18：Authentication-provider extension registry
- G19：Public services/scheduling/manual runs
- G20：Concurrent methods and scoped background submissions
- G21：Generic broadcast event module
- G22：Generic mutable synchronous chain events
- G23：Commands/ingress/menu registration
- G24：Scoped plugin logging and manager log views
- G25：MoviePilot auth-level/licensing backend
- G26：External notification delivery
- G27：Original market history/statistics/ratings/telemetry
- G28：Rich original core business-event payloads

## 后续进展

2026-10-25 的续开发、真实入口验证与未关闭项见 [插件模块续开发核查](plugin-module-continuation-20261025.md)。本文件以下记录保留为 2026-10-24 的历史证据，不代表当前所有能力均已验收。

## 本次已验证的小批次

- G21 的一部分：广播目标字段、分身类身份隔离、标准事件名称 `plugin.reload` / `plugin.data.reset`，不提供旧拼写别名。执行前再次检查 enabled、完整错误事件等仍在单独核验，不能把本次目标过滤门当作整个事件体系完成。
- G22/G14 的一部分：通用共享可变 JSON 链、稳定优先级、Reset 使用同一 registry；普通返回值不替换事件，异常前合法 mutation 保留并传给后续 handler，HTTP Reset 最终仍作用于原 route ID。
- G23 的一部分：真实 `command.register` 拦截菜单元数据，保留 cancel/force/既有可执行命令子集边界。现 core 没有菜单注册 provider 时明确 unavailable，不声明外部平台菜单已经注册。

验证为 SDK 5 根与 App 11 根普通及双端 race 各一次，全部 0 skip；16 个实际 worker 制品分别按模式构建，race 制品核对 buildinfo。SDK/App vet 通过。移除真实 mutation 投影的独立负对照在预期断言失败；保留旧菜单形状处理的负对照亦在原源要求的 force-empty 断言失败。原方法 oracle 的 34 例暴露并验证 truthy list/string 与 truthy scalar 的差异，已据此作最小修复。此前 14+14 只是修前局部门，保留其证据而不代替修后结果。不是完整 App、完整 SDK、生产认证或最终 UI 的全量验收。

原工作区丢失后的已提交基线以官方 c039 Git tree 逐路径恢复；本次未提交材料区分精确旧 hash 匹配与完整文本转录，并全部重新测试。46 个既有二进制模型/历史样本/图标未在本地完整恢复，未占位、未删除，远端原 blob 保留；不得把本次限定门外推为这些资源的验收。

## 原源和证据限制

目标规则 oracle 直接执行已核 Git blob 的原 EventManager 方法；31 广播和 19 chain 名称以固定源枚举为准。旧审计引用的 API SHA 16cb070… 与重新读取固定 commit 得到的 16e962… 不一致，旧绑定不能继续当作权威来源，管理 HTTP 全量对照需按官方 blob 重新核验。

详见 [本批验证摘要](plugin-generic-chain-verification.json)、[目标事件合同](plugin-original-event-target.md) 和 [命令注册链](plugin-command-register-chain.md)。后续最终门仍须覆盖完整模块清单、实际入口、异常恢复及真实组件交互；未执行项目保留未验证。
