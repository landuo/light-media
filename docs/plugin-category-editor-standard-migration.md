# CategoryEditor 1.3：本次标准 worker 迁移记录

> **2026-10-29 NAS 部署补充**：后续独立部署任务已按用户“更新光影”授权，发布宿主提交 `35f4f890`（兼容业务提交 `2b795ebb`，只额外隔离归档探针）。NAS 已恢复 healthy，原安装记录自动修复并加载已发布的 CategoryEditor 1.3；实际浏览器配置表单可打开，未启用或保存策略。配置、密钥、原 category.yaml 与其他服务保持；详见 [本次部署记录](plugin-categoryeditor-host-nas-deployment-20261029.json)。下文“未部署”等文字保留原云端迁移阶段的历史边界。

> 当前状态：**已批准安全适配边界下验收通过；最终独立核对 passed，main 提交推送进行中，未部署**。本记录属于 2026-10-28 新授权的已安装业务插件迁移，不恢复已取消的平台重构。历史 `plugin-category-editor-delivery.md` 记载的旧实现、旧配置导入和部署不能充当本次标准 worker 的验收结果。唯一权威进度为 `migration/ledger.json`。

## 来源与交付边界

- 目标起点：`origin/main` 的 `10d76660fb78595948d89dccaa034c87a2240206`。8 个独立 ZIP 的 11,051 个受版本控制文件逐项 SHA 校验通过；恢复树与远端真实 Git tree `20cf4d1030bd136302932563299bb98465180dbd` 完全相同。
- 已安装插件包 SHA-256：`44703b6523509ee82ea60d841b1a42256f54a4476a64f2efbd8b056d0d040183`。`CategoryEditor/__init__.py` 4291 字节，SHA-256 `24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27`，版本 1.3。不是上游最新版。
- 插件包不含当前 NAS 宿主、私密配置或运行数据。目标 main 内的 `installed-category-helper.py` 是 2026-10-20 归档，SHA-256 `1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2`；基类/manager 也是有日期的归档。未证明它们与当前 NAS 相同。
- 用户2026-10-29明确指示“插件基类可以查看光影代码”：目标基类合同直接复用 main 已交付标准 Go worker SDK/宿主接线，原 MP 基类不再是迁移前置。此指示不代表所有安全/业务差异已批准，也不证明当前 NAS helper 与归档相同。
- 用户指定的两个仓库已实际读网核对：jxxghp/MoviePilot-Plugins 固定 `ce3357c0f2bb4e46f5ff9b72560505a33b26a362` 的 `plugins/categoryeditor/__init__.py` 与上传1.3逐字节相同；landuo/MoviePilot-Plugins 固定 `2e6a90f6c3598c79520640429e4fc4a113152ac0` 完整默认分支树没有 CategoryEditor。仍以已安装快照为业务源，不替换为最新版。
- 开发与受控执行仅在助手云端 Linux。没有访问或改动用户 Mac、NAS、旧 MP、真实媒体、种子、下载器或站点；没有自动部署授权。后续仅在本插件必要验收闭合后独立提交、推送 main 并读回 SHA，才进入下一插件。

## 唯一实现映射

| 原入口/副作用 | 本次目标 |
|---|---|
| 持久 Python 对象的 `_enabled`、`_content`、Python 真值与空配置保留 | `internal/plugins/categoryeditor/categoryeditor.go` 的持久标准 SDK 对象；保留原始 JSON 值，不伪造 bool |
| `get_form()` 的 Vuetify/Ace 节点 | 同包 `form.json`，现有前端解释；不新增前端分支 |
| 读取内置默认策略 | `plugin.category.get_default`，返回 Go 包内固定默认资源；与归档内置文件字节一致，不冒称可动态读取 NAS 文件 |
| `write_text` 后 `CategoryHelper().init()` | `plugin.category.apply`，仅当前宿主数据目录的固定 `category.yaml`；写入、截断/编码失败与缓存状态分别观察 |
| `systemmessage.put` | `plugin.systemmessage.put`，真实现有本地系统消息队列；不是外部通知通道或数据库通知 |
| `_enabled=False` 后 `update_config` | 标准 `plugin.base.update_config`，完整替换当前实例两字段配置；失败前副作用不回滚 |
| 无 service/command/API/page/任务 | 返回原空值，不增加业务动作、调度、workflow/AI 能力或官方等级认证 |
| 分身 | 现有集中分身管理：独立配置/KV/进程与复制制品，仍共享原插件的全局策略文件；不复制第二套业务实现 |

入口为 `cmd/category-editor/main.go`，宿主契约为 `light-media.sdk-resources/v1`。只有四个明确能力：默认策略、应用策略、本地消息、实例配置替换。没有旧 native `invoke` 回退。

### 已修的实测缺陷（含 2026-10-29 恢复后的非安全修复）

1. 非字符串非空**条件值**原先在保存期提前报错。现保留至实际匹配相应非空媒体字段时才报错；前面条件失配不提前中断同规则，已匹配的更早规则仍短路。两个生产分类调用点传播错误，严格独立解析接口和安全限制保持。
2. 标准外部 CategoryEditor 分身卸载原先因命中历史 builtin 名单而残留复制的可执行包。现仅使用导入时已验证的 `implementation.External` 身份补充分支判断；只删除当前分身制品目录。旧 compiled-alias 分支保持不删目录；合成负控不冒称有已适配 native worker。
3. 缓存 getter 与显式 helper init 分离：错误根写入后的暖分类仍可用；普通读 IO 错误按原时序吞掉，不阻止通知/自动关闭。保留失败根状态，后续读 IO 失败仍重现原 `.get` 错误；语法失败/空根清除此状态。缺失用户策略创建精确默认字节；外部文件变更不再自动生效。
4. 非空 scalar 分区/规则保留至 `.keys`/`.items` 消费时出错；Unicode decimal 范围、superscript 的 `isdigit`/`int` 差异、YAML 1.1 隐式布尔和 True/1 重复键按固定原源校正。严格解析接口与安全限额未放宽。
5. JSON 编码放大不再使 24,019-byte 合法策略提前撞通用 128 KiB 限制。仅 CategoryApply 单字段解码器按最大六倍 JSON 转义设置 393,280-byte 有界输入；解码后 64 KiB、通用资源 128 KiB、stdio 与路径限制保持。实际 worker/App 已验证 HTML 字符及 64 KiB 引号、反斜杠、控制字符输入，并验证 64 KiB+1 写前拒绝；没有保留 SDK 或 worker 序列化特例。

## 已有证据及尚不能推出的结论

- 固定原插件完整模块受控回放：78 场景、220 步；依赖替身/故障注入被明确标注，不是旧 MP 实机。
- 真实 Go worker 对象与独立原源结果的受控宿主边界自动差分：68 场景、196 步通过；另 10 场景、24 步未运行，文件/默认模板/真实 helper/DB/stdio/UI 不在该差分证明内。固定 oracle 已纳入仓库，缺失或 SHA 不符直接失败；`-race` 与该包 `go vet` 通过。
- 历史 helper 原源使用同指纹 ruamel.yaml 0.19.1 回放：35 组输入分别观察热/冷态，另有独立边界探针。不是当前 NAS 同源认证。
- 实际标准 Go worker 进程、真实 App/SQLite、独占 loopback 制品服务已经用于安装、配置、完整替换、消息、分类、异常、重载/冷恢复及分身隔离测试。最终结果以绑定文件/二进制哈希的测试报告为准，不能把早期通过项延用到修改后的代码。
- 原整理消费者测试走真实 HTTP 预览、原模型解码、分类器及 SQLite 计划。另于 2026-10-29 补充真正 COPY 执行：真实标准 worker 应用规则后，通过正式 `/api/files/organize` 提交任务，由生产后台 worker 把新生成的 904 字节、1 秒合成 MP4 复制到归档源 oracle 预期的“中文动画”目录；核对 plan/job/receipt/history 成功终态、目标 SHA 与独立 inode，并确认源 inode/mtime/bytes 保留。元数据为严格受控响应，刮削关闭；不是当前 NAS、真实下载、移动、硬链接或生产媒体迁移验收。
- 浏览器验收已取得**真实部分通过，整体仍未闭合**：正式 Sites managed-linux 私有预览打通最新真实服务，用户通过安全输入完成测试账号创建，登录阻塞已解除。CUA 已实际完成本地制品安装、原 Ace 表单编辑、保存后自动关闭与内容持久化、真实系统消息、错误根失败/恢复、集中分身及配置隔离、normal 模式冷重启、分身重置。未读取密码、未绕过认证。分身卸载首次被删除审查阻止；用户随后明确授权后，唯一正式 UI 重试成功，非空合成配置及复制程序被清理，主配置/程序和全局策略均未变。KV 本次浏览器案例为 0→0，非空 KV 删除另有后端证据。固定 1.3 没有可合法匹配的分身新版本候选，不把同版本重装称为升级。原网络及安全输入失败记录均保留。详见唯一 ledger、`categoryeditor-ui-auth-current.json` 和 `categoryeditor-real-ui-observations-20261029.json`。

本轮恢复后的最终可行自动验证：冻结当前代码，真实标准 worker 与 App 均启用 race，选定分类/插件全组 **45 个顶层、145 个叶测试通过，274.346 秒**，测试期间源码哈希零漂移，vet 通过。初始组合运行遗漏模块测试 fixture 和通用 SDK 的 500ms 启动超时均保留原失败记录；分别正确复验，不将初始命令涂绿。此自动测试结果不含浏览器或 NAS；后续真实浏览器结果单独记载，不混用证明范围。其中更新相关用例仅证明同版本强制重取/重装、隔离及拒绝错误实例身份，没有新版本升级成功案例。

新增 COPY 用例两次双端 race 通过，最终当前测试文件版本用时 6.40 秒（包 8.102 秒），独立 Astra/xhigh 只读终审通过；这是后续独立运行的一项测试，不把此前 145 叶命令改写为 146 叶同轮运行。实现源码未改，仅新增 `plugin_categoryeditor_organize_copy_test.go`。源、目标、真实 worker 与结果绑定见 `categoryeditor-organize-copy-20261029.*`。

## 必须闭合的剩余差异

独立审查已发现，以下不能用“编译通过”或安全理由自动抹去：

- 前述已知非安全差异已修，独立窄回放将 20 输入×冷/热、3 组文件系统和 4 步恢复序列逐项比较：306 检查中 294 相同。余下 12 项仅来自四种冷态错误根的人工“保留失败构造对象”适配器；真实构造失败对象是否由宿主保留没有证据，因此不修改目标去迎合该适配器，也不把原比较器 exit 1 改写为全通过。
- 内置模板仍是不可变 embed 包资源，与原可变 INNER_CONFIG_PATH 文件的每次读取/IO 错误行为不同。用户策略缺失落盘、外部修改需显式 init 的差异已修；当前 NAS 默认文件尚未确认。
- 64 KiB、每类 64 分类、128 扩展字段、目录名/字段名、普通文件/符号链接、标签/复杂键、循环/深度/展开等既有界限严格保留；源端有接受而目标拒绝的输入。用户于 2026-10-29 明确回复“保留吧”，批准保留此前逐项解释的现有安全边界。这是已批准的行为适配，不是完全无差异等价，也不扩大其他功能删减或权限。
- 真实浏览器代表流程和受控真实整理 COPY 已完成；安全差异处置已获明确保留，最终独立核对已通过，正进行单插件 main 提交推送。原 MP 基类不再作为前置；归档 helper 的版本边界继续如实标注。当前私密配置/全局策略/KV 未作生产迁移，须与代码验收分开核对；本任务未授权部署，旧摘要不能替代本次配置迁移证据。

原始输入、源/目标观察及完整分类由独立审查报告绑定；上述清单不是排除功能的批准，也不是将未完成工作移交用户。必要资料/取舍只阻止依赖它们的验收；不再因此停止可独立实现与自动验证。第一插件全部必要验收闭合前不开始第二个插件。

## 可复现的本地检查入口

```sh
go build -o work/category-editor ./cmd/category-editor
work/category-editor --manifest
CATEGORYEDITOR_WORKER_BINARY="$PWD/work/category-editor" \
  go test ./internal/app -run '^TestCategoryEditorStandard' -count=1 -v
go test ./internal/plugins/categoryeditor -count=1
go test ./internal/app -run '^TestCategoryEditorLegacyAliasUninstall' -count=1
```

测试必须提供真实 worker，不缺失时 skip。双端 race 验证还需用 `go build -race` 构建 worker，并对 App 测试进程同样使用 `-race`。所有输入仅为独占测试数据；禁止把旧服务的 `onlyonce` 或当前生产配置直接重放到测试环境。
