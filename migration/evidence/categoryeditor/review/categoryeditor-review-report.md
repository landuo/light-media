# CategoryEditor 独立差分审查（收束版）

**状态：`needs_changes`，不是 `passed`。** 审查者：`category_source_audit`，Astra / xhigh。范围仅此次固定 CategoryEditor 1.3 与当前云端 light-media；未改产品、未运行旧 MP 服务、未接触真实媒体/下载器/外部通知。本报告不维护权威账本。

## 0. 证据范围与读法

- 原插件 P 是本次 NAS 完整源码，SHA 与已归档 1.3 精确相同；不是上游最新版。原 helper H 是 **2026-10-20 归档宿主**，不宣称本次 NAS 仍是该字节。原源 oracle 均先于目标比较，未以目标代码计算预期。
- 已独立实跑：完整原插件 **78 案 / 220 步**；真实归档 helper + ruamel.yaml 0.19.1 **35 输入**；补充原 helper **30 输入**（初版 29 已被最终 30 版取代）；4 个真实、隔离、owned 文件系统对照。完整输入、原值、异常、成员、字节、调用顺序保留在 JSON。
- 已独立实跑目标：真实 `Plugin.Handle → categoryEditorResource → parser`，**59 输入 × 冷/热 + 4 个文件系统对照**。使用真实 owned 文件、真实本地 message helper；`BaseUpdateConfig` 仅受控记录器，scope.validate 为显式测试适配，**不是 SQLite、身份授权、stdio、真实安装或浏览器验收**。测试 PASS 只表示记录器完成，不表示两端相等。
- 本轮目标 overlay 执行时 17 文件前后 SHA 完全一致。worker 当时 SHA 为 `881ddc11074a183c96d722e77e29d7858af2d89c9bf98c5fc4fb02427c090d31`；当前 worker W 为 `b7f16a9609a8cff0816034896066fc52d376209aa63dac54c1cf92f2f7bb4069`，后续仅 getForm 包资源注释改动由 worker 报告、当前文本已读；**没有冒称 overlay 在新 SHA 上重跑**。当前 W 的真实 worker-only 差分另有实现者最终 `-race` 68 案 / 196 步记录（10 案 / 24 步 not_run），本审查读取并核绑定，不把它扩张为全链验收。
- `categoryeditor-review-comparison.json` 逐输入绑定原输入、原观察和目标观察；所有判断中的“原”均指该固定 source replay。原方法错误类与 Go 错误消息不要求字面相同，但错误**是否发生、在哪一步发生及后续状态**必须分开比较。
- 下表引用 P/H/W/R/M/C/Y/D/Q 等别名，完整路径与 SHA 见文末绑定表。M=media_category.go，R=runtime，C=conditions，Y=YAML mapping，D=资源适配，Q=通用资源解码。
- `malformed_country_items` 从最终目标 probe 排除：任意坏 TMDB 原结构不能无损映射到 catalog.Media。初版 target-probe-initial 中该项不作为差异证据。`invalid_utf8_replacement` 的目标 overlay 经 JSON 字符串替换后写入，不证明原始坏 UTF-8 文件读入等价；该原始文件边界另列静态缺口。cycle 仅在原源有界子进程中执行，未加入无界目标探针。

## ① 已修复并有原源对照的 condition 错误时序

| 输入 ID / 输入摘要 | 原源实值 | 目标实值 | 影响与证据 / 定位 |
|---|---|---|---|
| `numeric_exact`, `condition_integer`：`genre_ids: 16`，媒体有 genre 16 | 写入、通知、保存完整 `{enabled:false,content}` 成功；列表含 Numeric；空 media 返回空；真正匹配到此字段才抛 `int.split` AttributeError | apply 成功、enabled=false、完整 update_config；列表 Numeric；匹配返回“条件 genre_ids 不是字符串，无法执行 split”错误 | **先前提前拒绝已修**；H:44–59,149–191；W:122–141；M:160–163,225–240；C:118–127。目标 overlay 未直接测试原 helper 的空字典接口，不把 typed Media 的默认值冒充空字典。 |
| `condition_bool`, `condition_list`, `condition_mapping`：true / [16] / {id:16} | 同上，分别 bool/list/CommentedMap split 错误 | 同上，均 apply 成功，匹配阶段错误 | 三种条件值类型已实跑冷/热。H:165–191；M:160–163；C:118–127。 |
| `earlier_mismatch_then_bool/list/mapping`：前项语言 xx 不匹配，后项坏类型 | 不短路；后项仍报 split 错误 | 不短路；后项仍报错 | M:227–236 已保留原循环；不能将 AND 优化成提前返回。 |
| `earlier_rule_shields_bool/list/mapping`：前一个规则 First 无条件命中 | 返回 First，后规则错误不触发 | 返回 First，无匹配错误 | H:161–165,223–225；M:225–240。 |
| `truthy_empty_string_list_deferred`：origin_country=[""]，条件 16 | 非空 list 在 Python 为真，达到 split 并错误 | 原始 presence 用 len(list)>0，匹配阶段错误 | H:175–191；M:215–223；C:118–127。这是直接 matcher 测试；正常 provider 的空国家代码受 catalog 校验阻挡，不能声称常规 TMDB 响应可达。 |
| `blank/comments/null/empty_map/empty_sequence/false/zero/empty_string/parser_failure/duplicate_key/multiple_documents` | 成功关闭开关；warm 保留旧分类“缓存电影”，cold 空 | 相同观察 | 已实跑普通空值/普通语法错误缓存路径；**不覆盖下节 root/IO/特殊重复键差异**。H:44–59；R:35–64,127–143。 |

纯 worker 的 JSON truthiness、缺键/raw enabled、重复 init、message/update 异常边界、update false/None 忽略等，源 78 案的最终 worker-only 差分覆盖 68 案 / 196 步。已读取其 coverage 明确列出的 10 案 / 24 步未跑，不以 78/78 报告。所有文件 IO、真实 helper、消息去重/呈现、DB、transport、browser 仍不由该差分证明。

## ② 剩余可恢复的非安全语义差异

这些输入并不要求移除路径防护或无限 YAML 展开。建议优先修复错误时序、缓存访问和编码体积计算；若选择有意改变原行为，需逐项明确范围，不能笼统称“宿主适配”而消失。

| 输入 ID / 完整输入定位 | 原值 / 阶段 | 目标值 / 阶段 | 影响、可达性、定位 |
|---|---|---|---|
| `write_only`：owned 普通 category.yaml chmod 0200；已有 warm 旧规则，再写 `movie:\n  New:\n` | 实际写成功；helper 读 PermissionError 被 H:54–55 吞掉并 warn；旧规则继续“缓存电影”；插件通知、enabled=false、保存成功 | 文件写成功后 D:102 调 R→M 读失败；apply_error=`无法读取配置文件`，enabled=true，无通知、无 update | **已实跑两端，P1**：原 helper 吞读 IO 而目标传播；用户开关与保存状态不同。H:44–60；D:93–105；M:71–79；R:114–116；P:39–49。chmod 只在 owned fixture，证据读取的临时权限恢复有记录。 |
| `scalar_true/scalar_integer/scalar_string/nonempty_sequence`：truthy 非 mapping 根 | 原 apply 的 `.get` 出错，enabled=true，但 warm 已有 `_movie_categorys` 仍可供 getter 返回“缓存电影” | apply 同样错误，但下次公共 currentMediaCategoryPolicy 每次重新解析坏文件，继续 query_error；内部 policy 实际仍“缓存电影” | **已实跑，P1**：不是“都错误所以等价”；失败后的消费者可用性不同。H:57–59,114–138；R:114–143；Y:67–69。插件分类列表调用 PR:18–29、整理 M:245–272 受影响。 |
| `section_scalar` / `movie_scalar`：`movie: 5`（另一输入为非空字符串） | init 成功、通知、enabled=false；list 到 `.keys` 才错，match 到 `.items` 才错；空 media 返回空 | init/apply 提前“分区、目录及条件必须为映射或空值”；enabled=true；无通知/保存；列表路径直接 query_error | **已实跑，P1**：condition 修复未覆盖 section 形状。H:58,119–121,156–165；M:127–129；Y:67–69；D:102。 |
| `rule_scalar`：`movie:\n  ScalarRule: 5\n` | apply 成功关闭开关；列表 `[ScalarRule]` 正常，匹配此规则才 `int.items` 错误 | apply 提前错误，enabled=true，列表也错误 | **已实跑，P1**：规则层延迟错误仍未闭合。H:161–165；M:146–148；Y:67–69。 |
| `unicode_decimal_range`：genre_ids `'١-٣'`；媒体 genre 2 | Python isdigit+int 范围匹配，返回 `ArabicRange` | ASCII-only categoryDigits，按两个字面端点处理，返回 `Fallback` | **已实跑，P1**：有效小范围、不涉资源扩张；H:207–214；C:13–23,50–65。 |
| `unicode_superscript_range`：genre_ids `'²-³'`；媒体 genre 2 | init 成功；到匹配 Python isdigit 为真而 int('²') 抛 ValueError | init 成功，匹配无错误并返回 `Fallback` | **已实跑，P1**：错误被改为普通不匹配；H:207–209；C:13–23,50–65。 |
| `yaml11_boolean_condition`：`%YAML 1.1\n---\nmovie:\n  LegacyOn:\n    original_language: on\n`；媒体 zh | ruamel 遵循版本指令，on 为 bool；apply 成功；匹配报 bool.split | 目标将 on 视作字符串；apply 成功；匹配返回空，无错 | **已实跑，P1**：版本/隐式类型差异，并非非字符串修复失效；H:49–50,191；R:35–64；M:160–171。不能声称原 YAML 版本语义已全覆盖。 |
| `duplicate_true_and_one`：`movie:\n  true:\n  1:\n` | Python 键 True==1，ruamel DuplicateKeyError 被捕获；apply 成功关闭；warm 旧“缓存电影”、cold 空 | node 重复检查以 tag+字面值区分；后续非字符串 key 拒绝；apply 错、enabled=true，公共查询也错 | **已实跑**：重复键错误归类及缓存不同。H:49–59；R:89–100；Y:100–102。若维持非字符串 key 安全/形状限制，也应明确“原 parser failure”到目标 fatal 的时序取舍。 |
| `missing_file`（对照原 `missing_user_copy_default`） | H constructor 用 shutil.copy 建立真实用户 category.yaml，随后可列默认规则 | M:65–66 返回 embedded 默认内容但不创建文件；target `file_created_by_read=false` | **已实跑，P2**：默认读取结果可同，落盘/后续工具观察不同。H:45–47；M:62–80。默认只读包资源合理性见④，不能等同于用户配置文件应否创建。 |
| `external_change_without_init`：已有缓存后仅外部写文件为 New，不调用 init | getter 不读文件，仍“缓存电影” | next currentMediaCategoryPolicy 计算新 hash，返回 New | **已实跑，P2**：自动热读 vs 显式 init；改变分类生效点。H:114–146；R:113–143。不涉实际外部服务。 |
| `html_escaped_payload_under_64k`：`#` + 24000 个 `<` + `\nmovie:\n  Normal:\n`，**24019 UTF-8 bytes** | 写入成功，通知关闭，匹配 Normal | `json.Marshal` HTML 转义后资源对象 >128 KiB；Q 在文件写入前拒绝 `插件资源参数必须为有界对象`；enabled=true，warm 旧规则 | **已实跑，P1**：小于已声明 64 KiB 的合法注释策略也失败，是传输编码放大导致的隐藏更小界限。W:127,162–170；Q:23–25；D:48–62。可在保留明确 bounded contract 的前提下处理，不建议无界放宽。 |
| `truthy_empty_string_list_string_condition`：origin_country=[""]，条件 `','` | split 后条件集空，原 info list 为真；返回 First | matches 跳过空值，present=false；返回 Fallback | **直接 matcher 两端实跑；常规 provider 可达性未证明**。H:175–191,219–224；C:86–115；catalog metadata K:7–18 拒绝空国家代码。不得把此项渲染为正常 TMDB 请求已证实故障。 |

### ②的外部调用链影响

目标写策略不是只在 worker 内存中；D:93 写真实 owned file 后调用 R。因此“后写入拒绝”的 shape/tag/path-like 等差异会把该内容留在用户文件，下一次 PR 分类列表或 M:245 整理读取仍报错。64 KiB 和编码后 128 KiB 拒绝发生在写前，文件保留旧值。所有动作次序在原始 JSON 的 `calls`、`file_base64`、`enabled`、`query_error` 分开记录，不能合并成一个成功/失败布尔。

## ③ 既有安全护栏：保留防护，明确迁移取舍

本表不是要求删除防护；原源“接受”不代表适宜在新宿主无界复制。当前用户授权没有自动裁定所有安全护栏等价，必须明确告知适配范围，且不能把非安全②混入这些护栏免责。

| 输入 ID / 边界 | 原值 | 目标值 | 影响与源码定位 / 证据级别 |
|---|---|---|---|
| `large_comment_65k`：66592 bytes，65 KiB 纯注释+合法 mapping | 成功写/通知/关闭并分类 | 写前 `超过64 KiB`，enabled=true，旧文件未改 | 两端实跑；D:58–62；M:29,68–78；R:32–33。资源限额应显式确认，不称原限制。 |
| `more_than_64_categories`：65 个有序空规则 | 成功，命中 Category00 | 写后 reload 拒绝每类最多64，enabled=true/public query error | 两端实跑；M:131–132。 |
| `more_than_128_extra_fields`：129 个额外条件字段 | 成功关闭，当前媒体未命中，返回空 | 写后 reload 拒绝单类字段超过128 | 两端实跑；M:176–177；额外字段会驱动真实 catalog 请求，保留有界契约。 |
| `empty_condition_name/long_falsey_condition_name/control_condition_name`：空 key、129字符 key、包含换行 key（后两者值 falsey） | 成功；分别 Fallback / LongField / ControlField | 第3行条件名无效，写后提前错误 | 两端实跑；M:153–158,187–189。falsey 条件原本甚至不查询属性；限制仍要明确，不把 name 长度限制归为原行为。 |
| `nonstring_condition_name/numeric_category_name/ignored_nonstring_root_key` | 原分别 Fallback / **整数123** / Normal；root 未用 key 被忽略 | 所有 mapping key 要求 !!str，写后拒绝 | 两端实跑；Y:100–102；M:114–121,137–138。目录类型安全、通用映射约束与根 ignored field 范围要区分。 |
| `path_like_category`：字面 `../escape` | 只解析、匹配返回 `../escape`，未创建目录或整理文件 | 写后安全目录名拒绝 | 两端实跑；M:137–138,183–185。必须保留 traversal 防护；240byte/空白/点边界/Windows保留字符/control 具体规则为静态读源，未逐一探针。 |
| `ignored_timestamp_value/ignored_custom_tag/timestamp_condition` | 前两者原忽略，命中 Normal；日期条件 init 成功，匹配 date.split 错误 | 全树 tag allowlist 不含 timestamp/custom，写后全部提前拒绝 | 两端实跑；R:46–51,83–87；H:49–59,191。不执行自定义构造器是合理防线；忽略域也受限及 timestamp 阶段变化应披露。 |
| `unknown_root_deep`：未使用顶层字段33层；`cycle_bounded`：movie alias cycle | 33层 ignored field 成功 Normal；cycle 在8秒有界子进程实际退出，ruamel 将 Recursive 值观察为 None，可 catchall | 33层目标明确报“循环或展开超过限制”；cycle 目标只静态确认 parents[node]/depth检查，**未实跑 overlay** | R:67–81 全树扫描 depth32/node8192；Y:13–24,58–71,117–118 merge8192/展开1024。具体8192/1024极值仅静态，本轮不继续扩探针。 |
| `symlink`：仅指向本任务 owned-policy.yaml | 原 Path.write_text 跟随 owned symlink，成功 New/通知/关闭 | O_NOFOLLOW 返回 too many levels of symbolic links；无写/通知，enabled=true | 两端实跑；P:41；D:68–82；M:64–69。不得为了等价放开越界文件目标。非普通设备/FIFO过滤、创建0600为静态审查，未触设备或任意文件。 |
| 原始坏 UTF-8 文件输入 `invalid_utf8_replacement` | 原 H open(errors=replace)，读取替换字符继续解析 | R:32、M:91 显式拒绝 invalid UTF-8 | 原侧真实坏字节已测，目标原始文件路径**仅静态**；最终 overlay 的此 ID 经 JSON 替换不能证明该边界。分类是文件格式防护取舍，不是已测相等。 |

全树扫描还意味着原本不会用到的未知根字段可让正常 movie/tv 规则失效。node/key/tag/深度属于当前安全/有界 parser 契约，不应通过偷偷跳过检查解决；若支持更多原语义，应采用不执行标签、可控展开的解析表达，并补相应故障时序证据。

## ④ 包资源适配、原宿主时效和实际配置缺口

| ID / 来源 | 原值与已知事实 | 目标与判断边界 | 定位 / 级别 |
|---|---|---|---|
| `form_reads_bundled_not_user_or_member`, `form_rereads_default`, `form_default_missing/directory/invalid_utf8/read_only` | P.get_form 每次读取 INNER_CONFIG_PATH 文件；原源真实 IO /异常已录。并非读已保存 content | W 每次请求宿主默认资源，D 返回编译 embed 常量；当前资源5298bytes，SHA与2026-10-20 archived `/app/config/category.yaml` 元数据完全相同。Go 单可执行包的不可变资源是合理候选适配，但不是“每次外部文件可变/读取失败”逐字等价；未擅自判用户已经接受 | P:61–117；W:144–159；D:24–29；M:23–27；B(installed-reference.json) 的 policies 证据。资源 SHA见文末。**当前 NAS 默认文件仍未独立核实**。 |
| `current_host_provenance` | 当前插件 P精确已核；H/base/manager/message 等归档分别2026-10-20/18/23或时间未知 | 已用固定 H+真实ruamel0.19.1建立可复现 oracle；不能由同版本/同插件推导当前 NAS host 同源 | H/B/YAML provenance及原source-audit第5/10节。当前 WeakSingleton、SystemConfigOper DB/cache、消息队列与宿主依赖的最新哈希仍缺。 |
| `current_user_configuration` | 2026-10-20 元数据曾有 content5499bytes/SHA61502d…和 enabled=false；没有当前实际 content/DB/yaml/内存快照 | 本次 source-snapshot无配置DB密钥；合成输入不构成用户实际策略迁移；旧元数据不可冒称当前配置或恢复材料 | B:original_metadata.config/policies；已知具体字段但内容与时效不足。不得把默认策略当用户策略，不能声称当前配置已经保全。 |
| `replay_environment` | CPython3.12.14，whole source exec，真实 Path+ruamel；helper WeakSingleton替换为显式固定对象，记录 adapters | 确认的是 controlled source replay；目标probe也有 scope/config DB适配边界 | 原source-manifest/source-audit/replay脚本；当前旧 MP 实机/真实单例/并发重载/崩溃恢复没有本轮证明。 |
| `excluded_extensions` | 原无自定义 service/command/API/page 实现，普通 stop是no-op；原 auth1元数据 | 用户排除 workflow/AI运行依赖、页面/API扩展点与官方等级认证；普通事件/命令/调度基础接入不能由此删掉 | P:54–59,119–126；base默认get_service/dashboard/close等固定源见source-audit。不把用户主动排除项误列功能缺失，也不把扩展点排除当浏览器可免验收。 |

### 最小 clone 卸载 artifact 身份接线复审（父已授权的窄修复）

- 已独立静态核三处完整 diff：`originalPluginClass.external`（U1:72–75）；启动时从实际解析出的 `implementation.External` 填入（U2:91）；卸载仅在已有 clone 分支将 package 删除条件改为 `class.external || !compiled`（U3:34–58）。修复的是“历史 builtin 目录名仍同名，但实际实现已经是外部标准 artifact”的识别错误，不恢复 native 业务 worker、不扩平台。
- package 路径仍是受管 packageRoot/instance，普通非clone不走此分支；原有 clone config/data 删除未扩范围；不删除全局 category.yaml。根据 archived manager 的 clone/uninstall 摘录，原 clone 卸载会清自己的复制代码/config/data，但不改全局策略。
- **独立已实跑 negative control**：`TestCategoryEditorLegacyAliasUninstallDoesNotDeletePackagePaths`，合成 `external=false,isClone=true` 编译别名，卸载后本实例/source/other三处 marker package 均保留；日志 `categoryeditor-review-uninstall-negative.log` `COMMAND_EXIT=0`。这只证明兼容分支，不冒称适配旧 native 业务插件已通过。
- 正向真实外部 clone 测试已静态读 `TestCategoryEditorStandardCloneLifecycleAndColdNoReplay` 的 L480–504：检查 clone config/KV/package 删除，source object/KV保留、策略无重放、无通知。父的最终 `verified-*` 集成-race仍在执行；**本报告不提前认领其最终通过**。需最终产物 hash+日志绑定后再独立验收。

## ⑤ 浏览器/最终集成验收仍有阻塞

- 已读取协调者保存的 `categoryeditor-browser-blocker.json`，不是本审查重新启动浏览器：本环境 Playwright Chromium 在 UI 前因 `process_singleton_posix socket() Operation not permitted` 退出；提权启动器在测试进程前因环境只读路径错误退出（未读取受限路径）；独立 cloud CUA 访问本机 loopback `ERR_CONNECTION_REFUSED`。同 exec server `/api/auth/status` 200，仅说明服务活着。
- 这属于 **blocked_not_product_failure**，不等于产品 UI 失败，也不是浏览器验收通过。禁止用另一个环境不能连 loopback 推导页面有bug；禁止以HTTP/Go测试冒充实际浏览器渲染/交互。
- 当前构建、vet、真实stdio+SQLite+HTTP集成、race全组与正向clone最终结果由父继续闭合；早期集成日志曾有 fixture缺失/数值提前拒绝等失败，不能拿旧 PASS/FAIL覆盖新一轮。此报告只冻结已完成的独立源差分和窄 negative control，不对尚未结束的任务做结论。

## 收束与建议次序

1. 先闭合②中可恢复差异，尤其读IO吞错/失败根后缓存、section/rule错误时序、Unicode范围、YAML1.1和编码后体积；不能仅以已修 int.split 宣称完全等价。
2. 对③安全/有界行为与④不可变包资源分别给明确适配取舍；保持安全，不把原接受危险路径当放开授权。对不修项目记录具体输入和用户可见影响。
3. 补当前宿主/配置来源或明确未迁移实际配置；待同环境可用浏览器路径和最终 target build/test evidence 后再验收。**本轮最终状态持续 `needs_changes`**。

## SHA256 绑定表（由收尾脚本追加）

| 别名 | 当前路径（相对此任务目录） | SHA256 |
|---|---|---|
| P | `source-snapshot/installed-plugin-sources/categoryeditor/__init__.py` | `24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27` |
| H | `light-media/testdata/plugins/category-editor-1.3/installed-category-helper.py` | `1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2` |
| B | `light-media/testdata/plugins/category-editor-1.3/installed-reference.json` | `eef07642049d763fd39016ac8660c3ec0b92a54e365a382a822aa195ff09ff62` |
| W | `light-media/internal/plugins/categoryeditor/categoryeditor.go` | `b7f16a9609a8cff0816034896066fc52d376209aa63dac54c1cf92f2f7bb4069` |
| R | `light-media/internal/app/media_category_runtime.go` | `793a5d4b87f1cd8e101b5208fc8682b95a09d3001a3c273a0808ec4e0e603544` |
| M | `light-media/internal/app/media_category.go` | `97c1622f98173b83f6a6c63d594f205a459d768b0f4d39557e7da8bf56f11041` |
| C | `light-media/internal/app/category_conditions.go` | `9672398179c19b1a4a0458f9029e56bf7ebfe0ff49d73e8f4521b5d3f2a9ae91` |
| Y | `light-media/internal/app/category_yaml.go` | `e86f35596e8823b3229e9a3bcb47664e30c3195c1ace4945c535800d70a35368` |
| D | `light-media/internal/app/plugin_categoryeditor_resources.go` | `c9c9a009c1e4edcd5a1c6b09263fc227f5cd916931d500889e457da43fd83420` |
| Q | `light-media/internal/app/plugin_original_resources.go` | `25c62dab5b5fe553b844118cce0035dc5b7a96dcf4d5642bba2f21a5b6590b28` |
| PR | `light-media/internal/app/plugin_original_media_categories.go` | `bd0c849a464f51e71223986b4aa6de9e1e4f6a64ab2c7cb7d29d6d3fc942d4fb` |
| K | `light-media/internal/catalog/category_metadata.go` | `3723dc0d9181fa9d588196a4bb791b2da81e9080c210b67a6f96044522644366` |
| DEFAULT | `light-media/internal/app/defaults/category.yaml` | `1095b3aeb315569f541fded31bc77f43fa625cb9ac4844e3a28ba7d7868b2303` |
| U1 | `light-media/internal/app/plugin_original_registry.go` | `496d1b2547eea2b7032dd3ff8ac2da9bdd080f2e018e53f004677e07f8833318` |
| U2 | `light-media/internal/app/plugin_original_lifecycle.go` | `a479ad6c0c7fdd7d3f24fddf676f956588c94030be1cace2cd72f4476d4cabcd` |
| U3 | `light-media/internal/app/plugin_original_uninstall.go` | `54f334aace4cb3b91ab4dce76cea8a3ae8276ec2502fa3a3dde222178f0d4d05` |
| UNEG | `light-media/internal/app/plugin_categoryeditor_uninstall_identity_test.go` | `034b70cacc720279a19588a7f5062be003f8ce3c5485cd6095e8169c1cdbef8b` |
| UPOS | `light-media/internal/app/plugin_categoryeditor_standard_test.go` | `19dc76892fa4b1fbd0db22a7c8afd052c22a73264e72126b561d32b85f75cde0` |

本表 W 为当前注释修订后 SHA；原 overlay 的执行期 SHA 独立保留在 target-probe.json，未覆盖。UPOS 仅静态审查，不将该哈希下尚未结束的父测试标 passed。

关键 raw 证据哈希：

- `categoryeditor-source-oracle.json`：`a92f0462e1708e293baa21203029e78071512a4afc78a3a038b3774733b02aca`
- `categoryeditor-source-helper-oracle.json`：`9fa95d762b18da0ee90973f4b494bf508a4ce68546834d11d214795df66d78a6`
- `categoryeditor-review-source-extra-oracle.json`：`148b80c44e632626d8379e786fee4603a1adefcc7ade9b9b2d6f07a6ffd6dbb5`
- `categoryeditor-review-target-probe.json`：`8d1edeed3f204957efcf450e239190fd04e6c4530acb901d5b8ac8d7ae270e11`
- `categoryeditor-review-filesystem-source.json`：`9691bc9e4251f7b4a545f350275bacb962cadc2ce60762e42ee30d948a4e5da9`
- `categoryeditor-review-uninstall-negative.log`：`0ad222b2b82ea73e16985ea5515b0035fe0967008dad7fed971ba668f0574f3e`
- `categoryeditor-browser-blocker.json`：`b499f0988d2402b4f5f3eb00894616714a31d14b4632c160400881257f83b5cf`
- `categoryeditor-worker-differential-final-coverage.json`：`d8784d363a70c2a41fcccdbb16d9bc2a22f0de2697b41fb553c2a92d2a424839`

`categoryeditor-review-comparison.json` 是各输入 ID 到原始输入/原观察/目标观察的引用索引，不重复打包原 oracle，不是新一轮执行。全部详细值仍读取对应 raw JSON 的同名 ID。
