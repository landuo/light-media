# 智能体工具注册表的缓存复制语义

2026-10-25，固定对照 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 `app/core/plugin.py`，blob `8fc8954d7e7df32b970f16f461efa244fa2f142c`。

原版 `get_plugin_agent_tools` 第一次构建后直接返回 getter 的原值，但写入缓存及读取缓存时调用 `_copy_plugin_agent_tools`，其中明确执行 `list(plugin_info.get("tools", []))`。之前 Go 实现只复制原始 JSON，遗漏了非列表值的转换。

现在 `internal/app/plugin_original_extensions.go` 只在缓存复制处应用 JSON 可表达的对应行为：数组保持元素；字符串拆成 Unicode 码点（包括组合字符各自独立）；字典变成保持 JSON 插入顺序的键列表；truthy 数值和布尔值在逐插件 try 对应范围之外返回错误，不发布缓存。第一次成功返回仍是 getter 原值。falsey 值在组装注册表时已被省略，所以缓存为空且不会发生 `list(null)` 错误。既有修订重试、失效和实例绑定逻辑保留，动作执行代码没有改动。

`TestOriginalExtensionAgentToolCacheJSONIterablesActualSDK` 使用真实安装的 `owned-standard-fixture` 扩展制品，验证首次/缓存值区别、中文与 emoji/组合字符、非字典序键、大整数、缓存副本隔离及缓存命中不重复 getter。`TestOriginalExtensionAgentToolScalarCacheFailureActualSDK` 验证四种 truthy 标量连续两次都失败并重新调用 worker，六种 falsey 值则成功缓存空结果。连同已有动作 HTTP、实际调用、缺失方法、缓存生命周期和并发失效测试，共七个根测试。

`testdata/standard-agent-tools/source_oracle.py` 读取固定提交的两个原版方法 AST，用 owned 对象执行相同输入，并核对标量异常、无缓存和重复调用次数。输出为 `testdata/standard-agent-tools/source-oracle.json`。Python 只用于开发时对照，不是应用依赖。

普通与 race 日志在 `work/plugin-module-20261025/agent-tools/`，归档摘要为 `docs/validation/plugin-agent-tool-cache-parity.json`。race 同时覆盖宿主测试和实际 worker；复用了主任务已构建的扩展制品，没有改动共享 fixture 或增加进程副本。

这只完成工具注册表的 JSON 复制语义，不包含 Agent 执行引擎。Python ToolClass 对象、嵌套对象的进程内共享身份和 Python 对象协议不由 JSON/Go SDK 表达，不能据此宣称原版 Agent 全量可用。
