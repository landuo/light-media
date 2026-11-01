# S11 zh-hans：可实现的完整纯 Go 依赖闭包（静态冻结）

## 裁定

可以独立迁移，不依赖站点/下载历史数据选择、TMDB 网络或原名称 parser；但必须是 **固定 zhconv-rs 0.4.2 默认 MediaWiki 数据 + 默认 wikitext 语义**，不是单字替换、OpenCC、English-only 或 plain 模式。该组件闭合后只补名称依赖，不单独宣称无 ID recognize_media 搜索整条完成。本轮没有产品修改、vendor/native import、构建、原函数执行、下载或网络。

原 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 `app/utils/zhconv.py` 直接两参数 `_zhconv(text,target)`。`pyo3/src/lib.rs` 声明 `wikitext=true`，随后 `src/lib.rs::zhconv_mw` 调 `convert_as_wikitext_extended`。本报告只读官方已恢复包；wheel `.so` 与旧 S05 同 SHA 是来源连续性，不是本轮执行证明。Cargo 默认 `compress,mediawiki`；`zhconv-rs-opencc` 是另一发行域，不替换本域。

## 数据已经精确齐备

- sdist SHA `c1efa15dfecfbbb5cf0e7d2a0be04b62e2a7aeae434fc09844dd537304d3baad`。
- 仅从已验证 sdist 读取 4 个精确 regular 文本成员到 `reference/`：`data/ZhConversion.php`、根 README、data README、LICENSE-GPL；逐成员长度/路径/SHA 见 `reference/member-bindings.json`。没有 whole-extract、PHP 执行或源码更新脚本。
- `ZhConversion.php` 497,388 B，SHA **`5cb0019b32bb39ec5c6e662029f90bd166f7a844efb3bc877f9be41fdd511bf2`**，与 `build.rs::read_and_validate_file` 对原 UTF-8 全字节的 SHA 完全一致；MediaWiki pin `ecf4342132cf089ac0c42436827e9038a738bb6f`。
- Hans 只选 **ZH_TO_HANS**，不附加 ZH_TO_CN。按原两个正则做静态文本清点得到 **4,685 对、无重复 source、671 个多码点 source、202 个含非 BMP 的 source、最长 4 码点/12 UTF-8 字节**。见 `hans-static-inventory.json`；这是数据清点，不是原转换 oracle。未生成目标大表。
- 原 build 稳定按 source UTF-8 字节长度降序、再字典序排序，同 source 先出现者优先。解析原文本，不做 PHP 字符串解释；本 Hans 数据没有引号/反斜杠转义字段。压缩表/DAAC/zstd 是实现载体；可从同一精确 pair 数据构造 Go trie，不能换规则库。

## 必须迁移的算法，不按注释“修正”

| 部分 | 精确源闭包与可观察语义 |
|---|---|
| 普通文字 | `converter.rs::convert_to` 的 leftmost-longest 非重叠替换；未匹配 UTF-8 原字节保留，替换值不再转换。固定 Hans trie 最大源长 4 码点，可逐位置最长匹配，不需要 Rust DAAC 序列化或新 native 依赖。 |
| 双遍 wikitext | `convert_to_as_wikitext` 先扫整页 global rules，再转换正文，因此声明后规则也影响其前文字。全局提取 regex `-\{.+?\}-` 不跨换行、不是完整嵌套语法；不能改成“更正确”的 MediaWiki 引擎。 |
| inline/nesting | 完整 `ConvRule`、`Conv`、相关 `VariantMap` 路径。flags `+ - R N D H S A T` 的顺序副作用保留，`R` 立即 literal 返回，未知规则按原 infallible 路径保留 inner 文本。深度上限原为 10；超过时跳过 opening token、不推栈；未闭合 pieces 的重建/转换顺序照原。已渲染 rule 文本不再普通转换。 |
| variant/fallback | 只公开 Hans，但规则内必须解析原 9 个 variant 名（ASCII 大小写不敏感）。Hans inline fallback 是 **Hans→CN→SG→MY**，不是 Hant 或 Zh。bidirectional global pairs 对 Hans 原实现为空；unidirectional `from=>zh-hans:to` 才按 exact target 生效，不能把 regional 行为套来。 |
| page add/remove | 新 secondary map 同 source 后写覆盖；removes 独立保留，故被 remove 的 source 后续 add 也不能恢复。secondary 与 builtin 取最早/最长，起点与长度相同时 secondary 优先。**builtin 禁用判断实际是 `removes.contains(builtinReplacement)`，不是注释所写 source**；禁用后原文前进一个码点再查，不补选同起点短词。 |
| 词法细节 | 保留原 trim/variant trim 与 from/to 空白差异；分号扫描的 `&…;` 状态只影响分隔，**不解码 HTML entity**。重复 variant 覆盖和 unidirectional 同 variant 列表顺序保留。必要 Rust Unicode White_Space 与 UTF-8 边界应明确实现，不把所有字符串随意 TrimSpace。 |
| HTML 排除 | 原大小写敏感、dot 不跨行的 `<script.*?>.*?</script>` / style / `<code>…</code>` / pre 模式原样跳过；code 带属性并不匹配该排除模式。不是 DOM/HTML sanitizer，不增加完整浏览器依赖。 |

两个须明确保留的验收边界：
1. `D` description 的 `VariantMap` 直接遍历 Rust HashMap，源未提供多 variant 稳定顺序；原 raw 必须保留，不得自行排序后冒“逐字相同”。将来若采用只限该分支的排列比较，要由独审依据实际源不稳定证据明确裁定；正常名称转换仍逐字对照。
2. 原 `Conv::from_str` 对空 `from` 的 `=>` 有 assert，部分 malformed 输入不是普通 parse-error fallback。不能把所有坏规则统一改成“成功返回原文”；异常映射要沿协调者实际原基准事实，不能在此静态报告伪造 Python PanicException 观测。

## 当前目标可继承／不可冒充

- `internal/releasemeta/anime.go` 现 inverse-character regex（历史 map SHA `8867f0f2…`）只支撑原分类标记 predicate；已有 S05 oracle 仍保留原交集，不重算/清零。它没有多字词输出、global/inline rules 或任意名称转换能力，本片不改它。
- 已有 `releasemeta.MetaObject` 状态、Name getter、原 cn_name/en_name 可以原样消费；无需重跑 title parser 或从目标结果反造源输入。
- 实际下一名称入口是 `TheMovieDbModule._prepare_search_names`：有真值 cn_name 才转换；按 `[cn_name, converted, en_name]` 原顺序去假值/去重。`TmdbApi.__update_tmdbinfo_cn_title` 的另一真实消费者以 `zhconv(title)==title` 判断候选中文别名；**此只记依赖，不在本轮偷改旧模型/提供方路径**。旧 MetaAnime 的相邻消费者亦不自动重迁。
- 当前 noMeta/Meta 显式 ID 已交 selector/provider/cache/report 的信用只继承未变部分。完成 Hans 不是完成无 ID 搜索、中文别名增强或报告 pending 门。

## 最小完整代码计划（未授权写产品、未执行）

建议一个内部 Go 包，固定 `Hans(text string) (string,error)`，不公开 variant/CLI/网络/任意规则注册 API：

1. `internal/zhconv/hans.go`：UTF-8 输入边界、immutable builtin trie、最长匹配及 secondary precedence；普通完整字符串域，不作 English-only 限域。
2. `internal/zhconv/rules.go`：原 flags、Conv、9 名称/局部 fallback、global action 与 description 语义；普通 parse fallback 和真实 exceptional branch 分开。
3. `internal/zhconv/wikitext.go`：原双遍、受限 HTML 跳过、10 层/未闭合规则状态机。
4. `internal/zhconv/data/zh_to_hans.json`（或等价固定可审格式）+ `provenance.json`：只由固定 PHP 的原解析/排序规则生成 4,685 对，完整 pair 对账并绑定源 SHA；生成器为离线标准库工具，不运行 Rust/PHP，不改 go.mod。
5. `internal/zhconv/hans_test.go`、`testdata/`：逐字保存协调者原基准一次；普通串/短语/非 BMP/重叠、不级联、inline/global/HTML/嵌套/错误的同输入差分。仅 D 多项序列比较另明示，不全局 normalize。并发调用只读 trie 的有限 race 根。
6. `internal/zhconv/licenses/` 保原 MIT、Apache、GPL 全文和 notices、数据出处/commit/SHA；后续名称 consumer 只一个明确 hunk 或新私有 helper，验证原顺序去重，不能同时解锁尚未实现的搜索路径。

初次交付应一次包含数据、词法和默认 wikitext，而非先把 plain-only 标为完整函数。未来有限验收层：原实际二参数输出 → 纯 Go 同值 → `_prepare_search_names` 顺序/重复/假值边界；需要端到端名称检索时另闭真实 provider/search 依赖，不用 fixed-success 替身。协调者当前原基准准备/执行独立，本报告不增加 source 执行权限或重复采集。

## 许可证保留要求（仅原 artifact 事实）

根 README/Cargo 明确：算法代码（不含表）MIT OR Apache-2.0；默认 MediaWiki 表 GPL-2.0-or-later；发行 metadata 因含表为 GPL-2.0-or-later。应保原作者声明、完整许可和数据 pin；衍生 Hans 数据及原表不得重标 MIT，也不能为绕许可换 OpenCC 后称同源。data README 说明 cgroups 不被库/CLI 使用，所以本闭包不需要下载/纳入它们。这里未作目标项目发行许可证兼容性法律结论。

**终态：STATIC SCOPE / IMPLEMENTATION PLAN READY；不是 Go 或 native 运行 PASS。** 原 App01 报告 fixture 待用户答复，所有该片输入继续冻结；本目录工作未改变其代理或测试路径。
