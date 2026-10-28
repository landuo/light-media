# 固定前端分身派生

`generate_oracle.py` 只执行后端 `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 `PluginManager._modify_federation_files` 与 `_rename_federation_assets` 原函数 AST，在独占临时目录生成9棵实际文件树的原输入/输出。源码与旧服务均不修改；Python仅用于开发回放，不是目标运行依赖。

目标集中实现为 `internal/plugins/artifacts/clone_frontend.go`，接线于 `plugin_instances.go` 的复制后/初始化前、`plugin_original_install.go` 的安装及DEV/仅资源发布后、`plugin_implementation.go` 的旧库存冷加载。底层 `CloneInstalled` 保持物理独立、逐字节复制；平台派生不由具体插件重复实现。

- JS按固定顺序全局替换完整类名、引号类名、css key与小写类名；CSS按固定的小写→原类名顺序处理。全局 `window.MoviePilotAPI`、CSS变量/选择器/普通字符串均在实际文件中派生。运行时拼接字符串保留，Axios不按owner重写URL。
- 递归处理只限小写 `.js` / `.css` 后缀，保留 `errors=replace` 的UTF8解码与通用换行；复用既有 `x/text` UTF8 decoder，未新增依赖。重命名仅限映射dist顶层文件，原版大小写条件/仅小写替换、冲突不覆盖、nested文件只改内容不改名等怪行为均保留。
- 源调用 `core/plugin.py:2044` 使用硬编码 `plugin_dir/dist`；Go制品的显式、已校验 `Manifest.Frontend.Root` 对应该dist根。这是包布局适配，不使用动态 `get_render_mode` 路径或任意代码目录扫描。
- Go可执行文件与编译声明保持原身份和字节。派生收据记录实际前端类名，库存/size/SHA及完整内容pin随实际资源重算；分身的分身以上一次真实派生类为来源。新发布制品按其编译声明canonical类与实际实例ID统一核对，声明已是精确实例ID时不再次替换。仅资源发布保留worker inode/cwd及SDK对象，实际更新/DEV既有流程不额外初始化。
- 复用已有journal资源发布/回滚。源类 `Remote` 会把顶层 `remoteEntry.js` 自身改名；目标保留其旧URL404，同时允许完整校验的后端分身继续加载，不把这个原缺陷改成整次clone失败。只有收据中精确可解释的顶层rename可放行缺失原entry；普通制品仍要求entry存在。

`clone_frontend_test.go` 对比9树源输出、真实包/收据/worker inode、clone-of-clone、冷读、幂等、失败恢复及源坏entry URL。实际SDK `plugin_original_clone_frontend_test.go` 通过应用HTTP核对global literal、保留runtime拼接、CSS类/变量/资源名与冷resolver；普通源码、配置、SDK身份仍由既有分身测试证明。`web/tests/plugin-clone-frontend.test.mjs` 用固定派生后的实际JS驱动实际Axios，证明literal调用copy而runtime拼接仍调用source。

21cbbd9b共享Linux主程序已启动35实际门，前置已证明真实分身CSS/文件名、grandclone、源alias与冷恢复；浏览器仍独立验收，未因此把14/15/31/35升级完成。

2026-10-27 r3（SHA `0afb9d33…`）补充真实 Linux 生产进程：auto=true/DEV=false 门10.07s、auto=false/DEV=true 门10.11s均通过；包含源frontend-only/实际v2、精确clone类更新、clone自身frontend-only、真实坏发布日志/恢复、冷启动和卸载。每次读取真实literal JS、保留runtime拼接、CSS变量及改名文件；PID/init/config/cwd及内容pin均实读。永久证据在 `docs/validation/plugin-clone-frontend-r3-20261027/result.json`，不自行提升整行状态。
