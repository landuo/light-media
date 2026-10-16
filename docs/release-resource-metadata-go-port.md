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

纯 Go 动漫词法器来自 [nssteinbrenner/anitogo 提交 260d546](https://github.com/nssteinbrenner/anitogo/tree/260d546976961245b80ae7bd02f0c0b52a3ecc44)，源码与 MPL-2.0 许可证保留在 `internal/releasemeta/anitogo/`。外围适配原 MetaAnime 的标题预处理、分辨率选择、制作组兜底及两个解析模式的已确认差异。

Rust 分辨率路径进一步直接翻译了 `anitomy-pure` 0.1.0 的 `split_raw_data`、`split_token`、`is_resolution`、每个 raw token 后的孤立 `480/720/1080` 判定及原 1,760 项后缀集合，位于 `rust_anime.go` 与 `rust_anime_extensions.go`，许可证保留为 `anitomy-pure.LICENSE.md`。此分支匹配整个 subtoken；`1080p/720p`、`1920x1080p`、`1080px`、`x1080p`、`UHD1080p` 不再因子串扫描产生原版没有的分辨率。legacy 在这些输入上的源行为不同，继续独立解析。Go 动漫词法器另修复了日本括号按 UTF-8 字节截断的问题，对应原 Anitopy 的完整字符 token。

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

624 个合成输入、两种模式共 1,248 个消费投影已通过；样本 SHA-256 为 `ed0252ae52b4e1cd3ca18dc42abf4ee90d0feaf5ca50aa3e4380453711b44498`。这些证据不构成完整 MetaInfo 已逐行等效或插件已迁移的证明。五资源字段和 `season` 在全部原支持输入上的 1:1 仍未完成，不能用当前样本通过替代。仍需推进：

1. 动漫 Go 词法器与安装的 Anitopy 2.1.1、anitomy-pure 0.1.0 的全部 token/关键词/数字遍历状态尚未逐行对应；已验证的 season/range/decimal 修正不能代替完整状态机审计。
2. Python `regex`、标准库 `re`、Rust `fancy_regex` 与 Go 所用正则引擎的通用 Unicode casefold、全部表达式语法尚未全量等效。编译不了的 legacy 配置返回明确错误；不能将受限表达式未验证称作匹配器等效。
3. `Name`、`Kind`、`Type` 及集号在此接口中是解析过程上下文；当前 oracle 只将前述资源字段与季投影作为等效断言，不能将其用于替代原版身份识别。但它们对资源与季投影的内部影响仍须等效，不能因为没有直接比较而省略。尤其 legacy 名称重试会替换整个 Anitopy 结果，Rust 重试仅替换名称，资源仍读取第一次解析结果。
4. `MetaInfoPath` 的父目录、祖父目录合并不在这个标题入口中，尚未实现。
5. 合成配置测试没有完成真实数据库配置、历史字符串、文件和引用的迁移验证。

正式插件启用和迁移验收仍须结合平台清单及实际数据完成，不能用上述样本通过代替。

继续实现时的具体算法依赖是：Rust `parsing_single_token` 的 Found/Unknown、括号/圆括号/日本括号标记，预先消耗 `5.1`、`E-AC-3`、`DTS-ES`、语言和复合关键词的顺序；`parse_episode_number` 的跨 token 多遍优先级；`parse_release_group` 对完整 unknown、前后字母、`BY` 和最后 `codec-GROUP` 的处理。当前 `animeRustFractionalEpisode` 并未完整表示这些状态，`original.ReleaseGroup` 的 Go 来源也尚未逐行替换；它们分别影响默认季和制作组。须继续移植这些真实算法，而不能用更多标题黑名单代替。legacy 同样需核对安装版 Anitopy 的 tokenizer、preidentified keyword 与后续数字遍历顺序。
