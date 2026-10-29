# CategoryEditor 窄范围独立复审（2026-10-29）

**结论：已识别的非安全差异，在受控差分范围内修复；不是全量等价或实机验收通过。**

- 本次独立重跑固定原插件 + 2026-10-20 归档 helper（真实 ruamel 0.19.1、owned 文件 IO）：20 输入 × 冷/热、4 个文件系统对照及 4 步 root/read 状态序列。源、依赖与输入 SHA 均绑定。
- 目标使用真实 `Plugin.Handle → categoryEditorResource → reload/getter/parser`、owned 文件、真实本地消息；`BaseUpdateConfig` 是记录器，scope.validate 是显式适配，不冒称 SQLite、身份验证或 stdio 全链。目标 18 个相关生产文件前后哈希一致，复核时仍相同。
- **294 项可比较观察一致**：暖缓存、只写文件读失败、外部改文件不自动生效、默认文件复制、分区/规则形状延迟错误、Unicode 范围、YAML1.1 `on`、`true/1` 重复键、HTML 字符编码放大及恰好 64 KiB 的引号/反斜线/控制字符边界。比较包含阶段错误、enabled、完整消息/配置回调、文件字节、列表与匹配值；不是只看测试退出码。
- **保留 12 个原始不相等观察**：四种冷启动 truthy 非 mapping 根各有 3 个 getter 对照。原 harness 使用 `__new__` 后显式保留失败 `__init__` 对象；不证明真实 `CategoryHelper()` 会返回失败构造对象。按协调者决定不修改目标生命周期迎合该适配。未找到归档 WeakSingleton 定义，当前 NAS 亦未确认。因此仅将这些观察划为适配器生命周期限制，不称已相等；原比较命令仍退出 1，原始值全部保留。
- 窄静态复核：成功写后显式 reload；暖 getter 只读缓存；普通 IO 与安全错误分类；严格 parser 未放松；目录列表与匹配分别延迟 shape 错误；CategoryApply 编码上限有界，通用 128 KiB、解码后 64 KiB、非普通文件/符号链接等保护仍保留。
- 第一次目标编译碰到在途源码引用未定义 helper，已记为**并发编辑中的编译尝试**，不是固定版本产品失败。后续最终探针通过执行并成功保存观察。

详见同名 JSON、`categoryeditor-independent-narrow-final-target.json` 与 `categoryeditor-independent-narrow-final-comparison.json`。原宿主时效、用户实际配置、浏览器与安全适配取舍仍未由本复审关闭；完整 App/SQLite/stdio 测试由协调者另行绑定证据。
