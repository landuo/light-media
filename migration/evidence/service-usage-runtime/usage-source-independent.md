# 23/24 候选 03 独立复核

## 有限结论

**QueryParams / 重复尾斜杠的已知共因修复通过本轮独立复核。** 我的固定 source-08 的 116 项在本轮真实目标 HTTP 重放全部相符。旧候选 02 的结果/失败和 source-08 初次发现均未改写。

这是候选 03 的有限后端与手合适配器结论，不是完整 23/24 已交付：普通非秘密 repo query 清除仍属未批准差异；主树联合运行、当前 dist、官方统计 UI、CI/远端交付由协调者另行验收。本审查没有浏览器、主树或 ledger 写入；不借邻组 S09 验收替代本片结论。

## 绑定与证据有效性

- `final-03/patch-manifest.json` SHA256 `86122b42783a857628b3804c73e69676e768bb04035102258871a70aae3d143c`。
- `final-03/final-receipt.json` SHA256 `277cd54582576e3319c6cc6e29116e04c38e650bed523403a86c65d6d236cb30`。
- 11 精确 delta 全部匹配；候选 02 其余 16 文件逐个未变。候选 03 冻结 normal/race/旧 worker 的 SHA 全部匹配。
- 作者 6116 实际编译输入 before/after 完全相同；本次当前重哈希 6115 匹配，1 个已清理可再生 GOCACHE 导出文件不存在，完整记录在 `target-candidate-03/verified-before.json`。不冒称当前 6116 全部仍存在，也未为其重建。第一次误将全部缓存也要求存在而中止的验证保留在 verify-attempt01.txt；修正验证只允许缺失该可再生缓存，非缓存输入仍必须存在并匹配。
- 主树 integration 清单 SHA256 `096a40d90e2fe66503d2a998260bf82eb108bcf1e4d02d52ed7da1d42f0e3357`；9 个非共享文件等于候选，2 个手合文件匹配独立记录的主树 SHA。重执行前后再次核全部上述绑定，0 漂移。

## 我实际重执行的内容

`target-candidate-03/run.sh` **不编译**，复用冻结 `final-03/bin/app.test` 与 `final-02/bin/worker`，执行 6 根，全部 PASS / 0 SKIP：

- QuerySlashSource116：真实 TCP → App.Handler → SQLite → 普通静态 owned HTTP，关闭自动跟随重定向；实际请求 scope/path/query 与源相同。UID 不打印原值。
- QueryParamsSource38：正式解码单测。
- SourceV1V2Wire 660 / FullSourceHTTP native230：作者原有固定源回归，不冒称这些输入由我独立发现。
- RestartReportProcess / ActualInstallReportsOnce：实际启动函数的 5 个冷子进程、真实 SDK 制品安装与报告；不是完整 daemon / NAS / 业务插件启动。

另核作者冻结 normal/race 各 12 根 0 SKIP/FAIL、无 race 警告，所有 receipt 指向的日志/观察数据/hash 均匹配；本次没有重新执行 race。

## 独立判定 source-08，而非作者自证

正式 source-query-slash.json 的 observations 逐个等于我的 immutable `source-08/result.json`，源 SHA256 仍为 `aa55698a72a6c5ce1f7794f3ce8773cd3dfb769373836a6af61a1d6781a599f1`。

我的 `target-candidate-03/compare.py` 从原源文件与这次新产生的 observations.json 比较：**116 项 / 52 次 peer / 0 差异**。精确比较 status、实际 scope/raw query、peer 原始 URI/method/payload、Location/Allow/Content-Type；完整 JSON 只容许对象键序/数字表示差异，不拆 v2 外包或过滤字段。HEAD、空 307、非 JSON 正文严格字节相等。116 项中 86 项响应原始字节也完全一致；另外 30 项是允许的 JSON 对象排序/数字表示，不伪称全部 raw body 相同。

输出观察只保存 Header.Get 的字符串，故缺省 header 在外部 comparator 中对应空字符串；我单独审查且执行了冻结真实 HTTP 测试内部的“header 必须不存在、不能只是空值”断言，不以字符串归一化掩盖 header 存在性。

## 代码与已有独立算法证据

- canonical 和 Location 都改为 TrimRight 全去尾 `/`，在原 authentication/origin/path guard 后才作重定向，未绕过保护。未抢占 root PUT/DELETE；这些带尾请求仍归 17/29，本片只检查不抢占，**没有验其通用配置/卸载源等价**。
- 新 QueryParams 仅用于精确 `/api/v1/plugin/rating`、`/api/v2/plugin/rating`。native 分支继续旧 URL.Query，未改全局查询或 token 权限。
- 解码 3 函数与 service wire-v2 的已审实现，经准确替换函数符号后**包括函数体注释在内逐字一致**，不是因名称相似而复用结论。核 service 文件 SHA `d7442abe113b379d4e1bee9a49212ffb066429100f06f281739d4cdec60e5f0f`；核 `wire-v2-after` 独审交接文件、16383 实际 Starlette QueryParams 源观察/通过日志及源依赖 SHA。该更宽算法证据明确复用，未称本轮 usage 又跑 16383 项。

## 主树手合复核

主树 remote-api 相比 usage03 候选只保留邻组 service 新 regex/raw wire，以及单独 system/progress native 行为。usage 的多尾 `/` 仅用于分类，实际发送 path/query 未去尾或重编码；原源空 307 由后端决定。

主树 plugin-axios-source 测试相比候选仅把原 native-envelope 示例从现在已属源 wire 的 system/runscheduler 改为真正 native 的 POST plugins/services/run；未删除 usage 多尾或 source-wire 断言。该改动避免把 service 源接口误当 native envelope。

再执行我自己的 `remote-check.mjs`：**134 条实际 Axios 适配器观察通过**，含候选/主树的 usage v1/v2、root/item、GET/HEAD/POST、双/三尾 `/`、原始 query、raw data/request_id 字段不误拆、redirect:error；主树 service 多尾别名仍保留；native business false 仍正常拆 host envelope。是受控 Axios adapter，没有网络/浏览器，不替代主树 server 或官方 UI。

## 未决项保持

非秘密仓库 query 删除仍未批准；不自行取消全 query 去除或其他凭据安全规则。2MiB 预算、会话/同源/路径保护继续披露。候选二进制并非融合后主树/新 dist 运行证据；旧02的220独立API结果仍只属于02，不重新贴成03的220执行。主协调可基于本次03的116与正式660/native230回归继续集成验收，不能以计数宣称无限输入等价。
