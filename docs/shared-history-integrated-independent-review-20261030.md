# S06 查询窄片独立集成复核（2026-10-30）

结论：两个精确查询及约定的 native/legacy 支撑通过本轮独立集成复核，不代表完整 S06。

- 源固定 `8a83efeacd3e26b82be495e17d50146e31c96a60`，主树基线 `e0258042f5196a5d94753f7dceaed9a8ad261d9e`。合并只为共享 dispatcher 增加 S06 两处入口，保留 S05 分支。
- 新鲜真实 source Oper/SQLAlchemy/SQLite → 15 个查询输入；实际安装 Go worker → host → SQLite。独立 normal/race 各 6 根通过，覆盖 native 物理冲突/成功重试、legacy、raw/typed Unicode、冷重启。
- 新普通 UI consumer 与 S05 worker 在同一实际 App 共存：六个 HTTP 拒绝分支，源/目标查询，clone，冷重启，clear 后历史和订阅保持。页面不缓存事实：外置 stale-result worker 变异被同一断言拒绝，未改产品。
- 新鲜当前闭包下既有 S05 13 项、clear 9 项 race 回归通过。但 S05 已另行确认未配对 surrogate 的 JSON 值/键会被替换为 U+FFFD，仍是开放缺陷；本回归不是全部 S05 等价认证。
- 协调者实际完成浏览器安装/配置/两按钮/缺失及 storage 边界/clone/cold；独立解析其 DOM 证据，4 条完整 30 字段等于唯一合成 fixture。此门只证明浏览器读取，native copy 另由真实文件测试证明。
- UI 仅将展示选择写回自身配置；历史事实每次由 SDK 读取，不进入 plugin data。它是历史只读消费者，不是完全不写配置的插件。

复现：设置 `HISTORY_SOURCE_ROOT` 为只读固定源、`HISTORY_SOURCE_PYTHON` 为兼容的测试专用 Python；Go 1.26 可用后运行：

```sh
bash testdata/plugin-shared-history-20261030/run-independent.sh NEW_OWNED_OUTPUT_DIR --race
```

脚本重跑真实源查询并构建真实 host/worker，不依赖私有审计绝对路径。生成目录内 config 可能含测试初始化密钥，勿整体上传。适用的精确 hashes 见配套最小 summary。未验范围仍包括 download/delete、辅助文件和自定义 callback producer、早期准备失败、完整源整理链及全平台验收。
