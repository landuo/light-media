# 原日志视图的离线参考

`generate.mjs` 使用项目已有开发依赖 TypeScript AST，从指定的原版 `LoggingView.vue` 读取三个实际函数 `normalizeLevel`、`matchesLogFilter`、`canMergeIntoGroup` 和两个 `computed` 回调 `groupedLogs`、`filteredGroups`；仅将这些函数放入带合成输入的 Node VM 中求值。不启动 Vue、不运行插件、不连接服务、不读取用户配置。

从仓库根目录重建：

```sh
node testdata/plugins/logging-reference/generate.mjs /path/to/MoviePilot-Frontend/src/views/system/LoggingView.vue
```

输出为相邻的 `logging-original-oracle.json`，含完整源文件 SHA-256、10 个规范化输入、11 个分组序列及 30 个筛选组合。前端测试逐项比较结果，保留负时间差、空时间、Unicode 关键词和未知级别的源语义。

这些参考只证明所抽取的规范化/分组/过滤算法，不覆盖原版网络、SSE、布局、解析 Python 文本日志或下载行为。Go 结构化日志适配、SSE 生命周期、鉴权、游标和导出另由相应后端及浏览器检查验证。没有把自行改写的预期值标为原执行参考。
