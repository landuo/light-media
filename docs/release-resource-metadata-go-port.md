# 下载资源元数据的 Go 实现

本模块为 SubscribeGroup 的下载后补空提供 `resource_pix`、`resource_type`、`resource_effect`、`resource_team`、`customization` 及下载历史 `season` 投影。资源属性解析不识别 TMDB 身份，不从订阅季号制造资源季号，也不允许文件写入。

## 接口与来源

`internal/releasemeta.Parse(title string, subtitle *string, options Options) (Metadata, error)` 接收一次下载上下文冻结的配置。`source_mode` 支持 `rust`、`legacy`；空值沿用已安装环境的 Rust 模式。`custom_words` 是该次识别使用的完整识别词列表；`release_groups` 是内置制作组之外的自定义表达式；`customization` 保留配置顺序；`media_exts` 可提供实际运行时后缀列表，未提供时使用原版默认列表。空列表与未提供后缀列表不同。

五个资源字段的空字符串承接源 `None` 的判真值语义。`begin_season`、`end_season` 用指针保留零季与缺值区别；`season` 按源属性生成 `S00`、`S04`、`S01-S03` 等字符串，类型为电视剧而季号缺值时返回 `S01`。它与所选订阅季、选择下载的集数相互独立。

主要参考是实际安装文件的只读副本，非本机旧仓库后续版本：

| 原安装文件 | SHA-256 |
|---|---|
| `app/core/metainfo.py` | `7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9` |
| `app/core/meta/metavideo.py` | `ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077` |
| `app/core/meta/metaanime.py` | `d45433c77656bba3203326943c9fe5b3ae6cbe8c7514e631fb16cc7dc1f4afca` |
| `app/core/meta/releasegroup.py` | `adecb60bcfe3772d71fabbf1cbd45146e7577c9be1c22f1ae4af74ab771da13f` |
| `app/core/meta/customization.py` | `6ff9692f267b6d5ec8eac5d2a47523fef22a55f4566f252c86e0595c2213ba67` |

安装的 `moviepilot-rust` 是 0.2.6，PyPI 发布证明对应 [MoviePilot-Rust 提交 017b3a0](https://github.com/jxxghp/MoviePilot-Rust/tree/017b3a0e4ce2415d641c0eeb84ac907f9f10450c)。其 Cargo.lock 固定 `anitomy-pure` 0.1.0，源码包校验和为 `33f525032668d2aff5dff115958157db7aecf1dc2fd5f5df93cf1be1452dfd4a`。运行时不加载这些 Rust/Python 包。

Rust 动漫由 `internal/releasemeta/rustanime/` 中的纯 Go 状态机执行，逐个翻译固定 `anitomy-pure` 0.1.0 的单标题流水线：原序元素和重复值、185 个关键词及其标志、括号及弱括号 token、Found/Unknown、数字与分辨率、三轮集号识别、别名集号、片名、发布组、集标题、1,760 项文件后缀以及 builder 选项。Rust 路径不再调用 Anitogo，也不再用标题正则修补原引擎的季集优先级。MPL-2.0 和 Unicode 数据许可证随代码保留。

实际 wheel 的 SHA-256 为 `ab27c023a3863ac20a8221317e3b609d2fb18de8669527c81b1997cdf086c729`；唯一编译器提交为 `8bab26f4f68e0e26f0bb7960be334d5b520ea452`（Rust 1.97.1）。对应官方 `unicode_data.rs` 的 SHA-256 为 `4438831f7c584a35457022c4016ab8a295f242ff809b81a5c81b9bb14dc5e166`。这份原表经全部标量与正式 Go 范围/映射比较后差异为零，参考行流 SHA-256 为 `ce00fe872143a58108354429e951d063b1d54684fdab6242975e8100eaafbc04`，因此无需更换现有 Unicode 17 表。`verify_installed_unicode.py` 可只读重建这个检查。

`anime_rust.go` 消费真正的有序元素，独立解析 prepared、original 和必要的 `[ANIME]` 名称候选。重试只替换名称，资源、季和集始终读取第一次 prepared 的结果；发布组兜底读取 original。原引擎 panic 经原 wrapper 的 fallback 规则重新执行 legacy，包括重新应用原始输入的识别词。空解析错误仍按源 `.ok()` 处理。

legacy 动漫现在由 `internal/releasemeta/legacyanime/` 的独立纯 Go Anitopy 2.1.1 引擎执行，已移除 Anitogo 过渡包。来源是实际安装的九个 Python 文件，SHA 清单保存在 `release-meta-legacy-python-provenance.json`。完整单标题流水线保留初始化/后缀/忽略字符串、7 种括号、preidentified 关键词与相交区间、token 分类和身份、分隔符校验、关键词、独立数字、各类季集/卷号、别名集号、标题/发布组/集标题及校验，包含原 `find_previous` 在索引零的切片行为。176 个关键词、29 个后缀及每项标志均取自实际安装包。

legacy 采用独立 Python 3.12.13 / Unicode 15 数据：`isdigit`、`isalnum`、Unicode 名称/LATIN 名称、NFKD 去除 combining 类别后完整大写、十进制值、大小写和 title 映射、Cased/CaseIgnorable；没有复用 Rust Unicode 17 表。全标量提取脚本已在实际环境重跑，所有捕获数据对象完全一致。Go 测试逐个核对生成范围/映射与关键词。Python 的任意精度整数及安装环境 4,300 位十进制转换上限独立实现；超出 Go `int` 的季号仍用精确十进制字符串生成 `season`，`*int` 诊断字段仅表示可装入本机整数的值。

`anime_legacy.go` 按实际 MetaAnime 的赋值顺序消费原始、预处理和 `[ANIME]` 重试字典；重试替换整份字典，名称先拆中英文，Python `str.title` 保留数字词首及 final Sigma 语义。UNKNOWN 枚举为真值，不套用 Rust 默认电影规则。`StringUtils.get_keyword` 的 cn2an 校验异常、原始解析为空、matcher 异常均保留前序字段并跳过后续赋值。中文数字使用 cn2an 0.5.24 的必要完整捕获字母表与 smart/normal/口语校验，未把“十十”误算为 20；该依赖带 MIT 许可证。legacy 字幕处理独立，Rust 仍沿用自己的规则。MetaInfo 入口则使用实际安装第三方 regex 2026.5.9：其 `\d` 包括 Garay/Tolong 等新数字，而 Anitopy 自身不包括。路由的 `\d`、`\s` 和涉及字母的 IGNORECASE 集合已在实际 extension 上逐标量提取并独立固定，不能用 Python15 表或 Go 默认正则替代。

原 Anitopy 的集标题遍历存在可证明不改变状态的死循环：`end is None`、候选为单个连字符且 `continue` 后再次选到同一 token。七个实际 wrapper 合成输入已在隔离子进程中超时核实。Go 在该重复状态返回明确 `source_non_terminating`，不会挂住宿主或静默返回空 metadata；这是明确记录的源行为差异。

## 必须保留的源差异

- Rust 按出现顺序保留并规范化所有片源标记；legacy 保留首片源及特定组合。原安装 legacy 的 `_last_token` 仅首次片源赋值时更新，本机旧仓库后来的修复不能混入。
- 特效保留标题中的大小写、去除相同值后反转连接。`BluRay` 的 DIY 追加沿用源字符串判定。
- 制作组只在原规定前后边界匹配，顺序去重并以 `@` 连接。legacy 配置包含捕获组时使用 `findall` 的首捕获列；Rust 使用完整制作组匹配。
- 自定义占位符区分大小写。legacy 保留所有不同的匹配值并按捕获索引排序；Rust 每个捕获索引最多保留一个值，后续匹配可覆盖。
- Rust 自定义识别词使用检查溢出的 i64 算术、向零截断的除法和取余，替换字符串的转义规则也与 Python 不同。负中文偏移导致 Rust 异常时，原 wrapper 回退 legacy；本模块保留该分支。
- 资源为文件时，普通视频的多季范围与非文件标题有不同规则。Unicode 数字转换保留 legacy Python 与 Rust ASCII 整数转换的差异。
- legacy MetaAnime 捕获制作组/占位符错误后保留已赋值的字段，跳过之后的副标题解析；普通 MetaVideo 对同类错误会失败。Rust 对无效匹配器配置忽略该表达式并继续。

历史数据的 `Customization` 若为字符串，迁移边界还需沿源规则规范化：换行和 `|` 替换为 `;`，去掉首尾分号后拆分、过滤空项；不额外 trim 每项。本接口接收已经规范化的列表。

## 验证与尚未证明的范围

`testdata/plugins/release-meta-original-oracle.json` 保存安装版解析器在合成配置、合成标题上的离线参考，以及所有来源 SHA、三方版本和 wrapper 来源标识。测试同时对照 Rust 与 legacy，比较五资源字段、开始/结束季及 `season`；使用真实 `MetaInfo/rust_accel` wrapper 的附加样本覆盖异常、部分初始化和回退。Python 仅生成离线样本，不是产品依赖。并发测试检查多个事件共享不可变表达式时的结果与竞态，边界测试检查限制和错误不回显配置表达式。

既有 624 个合成输入、两种模式共 1,248 个消费投影在先前检查点已通过；样本 SHA-256 为 `ed0252ae52b4e1cd3ca18dc42abf4ee90d0feaf5ca50aa3e4380453711b44498`。

新引擎的 `release-meta-rust-anitomy-oracle.json.gz` 保留 10,103 个实际 Rust 执行的最终有序元素/错误和 779 个代表输入的逐阶段状态；SHA-256 为 `5f8ae52c93cea740e680a2b2e96ea2db710a35967150300943c2d66489ef2b3b`。fixture 包括 43 个源错误，保留错误语义而非静默空值。实际安装 wrapper 的新增 139 个动漫参考（其中 12 个保留 nullable 制作组的 Some("")/None 区别，39 个来自实际安装 wheel 的 Unicode 字母/数字/大小写行为）进一步断言名称/类型/季集/资源，SHA-256 为 `c3e92edebfc82411adbd1f8024ab10b7956bb2c80017be346d649fa4a6e03ea0`。离线重建和来源说明见 `testdata/plugins/releasemeta-rust-reference/README.md`。

新增 legacy 参考包括 10,271 个实际原执行的每阶段状态/最终字典/错误（10,215 正常、51 AttributeError、4 ValueError、1 re.error）、461 个实际安装完整 MetaInfo/MetaAnime wrapper 参考（含 7 个源非终止输入）和 1,073 个实际 cn2an 转换参考。另保存实际 `is_anime` 的四条完整表达式/flags 及 10,864 个路由结果，覆盖源语料和括号/破折号/季集结构、长度、分隔符、Unicode 组合；Go 逐项比较完整路由决策。引擎测试用 gzip + json.Decoder 逐 case 读取，避免展开约 140 MB 的完整源阶段 JSON。来源、离线脚本与数据表核对见 `testdata/plugins/releasemeta-legacy-reference/README.md`。

本轮最终全仓普通和 modernconly 通过；`releasemeta`、`legacyanime`、`rustanime` 竞态分别通过 16.998/32.217/11.539 秒。三个最终 CGO0 linux/amd64 测试程序均在 NAS Alpine 3.22 的 UID 1000/GID 1001、无外网、只读挂载、cap-drop=ALL/no-new-privileges、256 MiB 内存、128 MiB tmpfs、GOMEMLIMIT=64MiB 条件下执行各自全集，终态 PASS/exit0、无跳过和 OOM。其本机/NAS SHA-256 分别为 `800191b92c6a3dc47b2073ff21b00f1d9b499327833d95bc83c05d71c17d0f1e`、`f10b52211f94718bf653e4e743de2e0aa165f046dc679995bec03405d446b474`、`08a858f1ed965141edc501c43e6a3224ab783f3f072a6587cb110ab064681f25`。独占容器/目录已清理；未挂载真实媒体或原版配置。

此前 Rust 检查点在上述 fixture 上通过本机普通及 race 测试、全仓普通与 modernconly 检查；元数据包和独立 Rust 动漫 Go 包又分别在 NAS Alpine 3.22 隔离容器终态 PASS，包含 Unicode 全标量摘要核验。容器使用 UID 1000/GID 1001、无外网、只读挂载、256 MiB 内存及 128 MiB 临时目录，未挂载真实媒体或原版配置。两个最终 NAS 测试程序 SHA 分别为 `6bed9aaaf05a3a6d57f7b511787edbd9a15238b7ae4cc1b73f0fa7a699605cf2`、`3d4a6dc079f886ac742d2e95bfc702f3a1e1e71427528cad30fa5dbbd9227a5d`，与本机一致。

这些证据不构成完整 MetaInfo 或全部插件已迁移的证明。五资源字段和 `season` 在全部原支持输入上的 1:1 仍不能用当前样本通过替代。仍需推进：

1. Rust 与 legacy 的完整单标题引擎已分别按固定来源移植，但有限语料通过不等于任意输入都已穷举证明。Rust 的实际编译器为 1.97.1 / Unicode 17，精确官方原表与 Go 全 1,112,064 个标量核对差异为零；legacy 独立采用实际 CPython 3.12.13 / Unicode 15 全标量提取和逐表核对。此证据没有扩大为所有正则表达式、全部 stdlib 或任意 MetaInfo 输入已等效。
2. Python `regex`、标准库 `re`、Rust `fancy_regex` 与 Go 所用正则引擎的通用 Unicode casefold、全部表达式语法尚未全量等效。编译不了的 legacy 配置返回明确错误；不能将受限表达式未验证称作匹配器等效。
3. `Name`、`Kind`、`Type` 及集号在此接口中是解析过程上下文，不能用于替代原版身份识别。新增 Rust 与 legacy 动漫 wrapper 参考覆盖这些依赖字段；既有普通视频参考仍以资源与季投影为断言，不能扩大为其完整身份识别已验证。legacy 名称重试替换整个 Anitopy 结果，Rust 名称重试只替换名称，两者按各自源码接线。
4. `MetaInfoPath` 的父目录、祖父目录合并不在这个标题入口中，尚未实现。
5. 合成配置测试没有完成真实数据库配置、历史字符串、文件和引用的迁移验证。

正式插件启用和迁移验收仍须结合平台清单及实际数据完成，不能用上述样本通过代替。

原先用于过渡的 Rust 分辨率/season/decimal 扫描已删除，相关行为现在来自完整 `parsing_single_token`、`parse_episode_number`、`parse_anime_title`、`parse_release_group` 等状态机。后续差异必须先定位真实源阶段，扩充实际参考，再修正算法；不能恢复标题黑名单或把未知行为一律变成空值。

本轮真实完整对象参考发现并修正了 Rust 消费路径的两个具体遗漏：`cn_number_to_i64` 会覆写连续中文数字，因此“第一一季”是 S01；Rust 的四条动漫入口路由及预处理数字类来自 `regex-syntax` 0.8.10 / Unicode 16，Garay/Tolong 和全角数字不能用 Go 默认类处理。新增 `releasemeta-rust-reference/routing-numbers.json` 的 36 个实际安装 wrapper 与 1,531 个实际 wheel 路由参考，对应离线 AST 脚本、固定 crate 源 SHA、完整范围核对脚本和数据许可证均已保存。原 139 个 Rust wrapper 参考仍保留。此补丁没有把 work 中正在移植的完整 MetaBase/MetaInfo 构造器算作正式功能，也没有扩大 legacy 普通视频旧投影的等效声明。
