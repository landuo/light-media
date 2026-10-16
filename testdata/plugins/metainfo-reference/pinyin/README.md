# 两套拼音谓词的完整 Go 依赖切片

仅在 ignored `work/_metainfo_full_port/pinyin/` 内，无独立子 go.mod；父原型可直接 import `light-media/work/metainfofull/pinyin`。没有修改主项目/父 module/构造器/既有正式 fixture。Python/Rust 只用于源码核验及离线 oracle，Go 运行时不调用 Python、Rust、原服务或外部 API。

## 可接入接口

```go
LegacyName(name string) (bool, error)
RustName(name string) (bool, error)
LegacySyllable(value string) bool
RustSyllable(value string) bool
Segment(input string) ([]Segmentation, error)
LegacyInventory() []string
RustInventory() []string
```

`LegacyName`/`RustName` 包含原调用方包装，不是只查单个词表。合法 UTF-8 名称无需外部状态，返回 nil error；非法 Go UTF-8 明确返回 ErrInvalidUTF8，不把字节替换为另一个可接受的标题。若调用原 Rust 的底层 Segment 会遇到非字符边界切片 panic，Go `Segment` 明确返回 ErrRustUTF8Boundary；真实 RustName 先门禁 ASCII，因此不走这个错误分支。

## 实际来源与依赖闭包

| 来源 | 已核证据 |
|---|---|
| 安装 Pinyin2Hanzi 0.1.1 util.py | SHA-256 `5e863ce8c401e27ce820fba275ac1f215b26c0f427906da793684b9c660212b7`；完整 `__pinyin` 411 个成员，`is_pinyin` 只有直接 membership |
| 安装 MetaVideo 包装 | `/app/app/core/meta/metavideo.py` SHA `ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077`；原 `__is_pinyin` AST 保存在 legacy-installed-source.json |
| Python Unicode | 安装运行时 Unicode15.0.0，完整扫描全部标量得到29个 isspace 字符和唯一非ASCII→ASCII lower 例外 U+212A→k；这些实际运行时表一同保留 |
| MoviePilot-Rust wrapper | 提交 `017b3a0e4ce2415d641c0eeb84ac907f9f10450c`，parser.rs SHA `252b7e50adde31f7a96ce676c16a88dcb1f269fdad10737d1e4da9d1aa80795f`；原两函数文本在 rust_oracle/name_wrapper.rs，未用 Go 重新解释的 wrapper 产生参考 |
| golia_pinyin alias | 固定 Cargo.lock 的 inputx-pinyin1.0.2，官方 crate SHA `4fa1bf26d5923bb73e34c63e7ef21fc976c37e016ea13872045bf72335f0a43c`；403 音节全集，完整 segmenter.rs DP/回溯/稳定排序 |
| 实际 Rust 依赖 | 原 crate 经 cargo 编译，phf0.11.3/fst0.4.7；本包实际消费闭包是 syllable + segmenter。IME/FST汉字字典、候选词排序/学习/Session不参与这两个谓词，没有用FST词典去近似音节判断；完整原 crate/许可证仍保留供核查 |

legacy 与 Rust **不能互换**。Legacy 独有 `chua,dia,fiao,jve,kei,lo,null,nun,qve,xve,yo,yve,zhei`；Rust 独有 `eng,jue,que,xue,yue`。

## 精确保留的语义

- Legacy 首先 `if not name_str: return False`，随后逐个 `name_str.lower().split()` 成员检查：空串 false，非空纯空白 true。不做 tone removal、不把 ue 转 ve、不接受连续多音节、标点不裁剪。Unicode lower 只要结果包含非ASCII就不可能匹配ASCII词表；利用实际运行时完整扫描的例外表保留 Kelvin sign 等转换，不用 Go 的简单 Unicode lower 冒充 Python 完整 lower。
- Python split 还把 U+001C–U+001F 视作空白；Rust split_whitespace 不包含这些字符，两者分别处理。
- Rust 逐词 trim 首尾非ASCII字母，ASCII lower，删掉清洗后为空的词；清洗后至少有一词，且每词所有字节都是ASCII字母并符合完整音节或完整串联分词。内部标点、非ASCII字母不被悄悄删掉。`中ni好` 可清洗为 ni；`ni😀hao` 仍失败；`!!!` 不会变为 true。
- 完整 Segment 严格保留六字节候选窗口、前驱插入顺序、从末尾向前 DFS 回溯、反转路径，以及按音节数的稳定升序排序。空输入得到一个空 segmentation，无法完整切分得到空列表，不接受只切出前缀。
- RustName 仅消费原 `segment(word).first().syllables` 是否非空；非空ASCII输入上这与同一个DP图终点可达严格等价。因此布尔接口保留完整可达性而不物化指数多条路径，完整 Segment 接口仍枚举全部原结果。没有加入英语词典、拼音打分、贪心猜测或启发式。长 `xian` 连续串与尾部非法q有单独判别测试。

## 实际原执行参考和验证

`oracle.json` SHA `fbf23cb596a84858b87f3eddb46621b295d6689d0d69c8c6535833a2a5d977d1`：

- 2,629 个单音节及大小写/标点/空白/Unicode变形，包含两份完整库存。
- 180,608 个名称包装结果：包含403音节全部两两串接、全部26³三字母字符串、边界/特殊词/Unicode；旧分支在实际安装NAS Python执行源AST，Rust分支真实编译固定crate并执行原parser函数。
- 2,508 个完整有序分词结果：两套大小写、歧义词、随机固定种子音节组合和非ASCII输入；其中8个原UTF-8切片panic被明确记录，没有把它们当空结果。
- Go 普通测试 `go test ./pinyin -count=1` PASS1.372s；race PASS4.450s；`go vet ./pinyin` exit0。原日志为 go-test.log/go-race.log/go-vet.log。完整库存/原执行对照、布尔可达性的大输入、非法UTF8以及不可变库存投影均有测试。

上述只证明依赖切片，**不等于完整 MetaInfo、title/subtitle 调用或正式应用已经完成**。父原型应在既定进入该分支的位置传原 en_name，不在这里把资源全标题自行重解析；新增桥接需要继续跑其完整实际 MetaInfo AST/wheel0.2.6参考。

复现顺序：`python3 pinyin/generate_data.py`；`cargo build --manifest-path pinyin/rust_oracle/Cargo.toml --release`；`python3 pinyin/generate_oracle.py`（只执行合成输入，legacy需要只读NAS源码）；最后在父独立module运行 `go test ./pinyin -count=1`。已有 oracle 的 Go 测试不需要SSH或网络。fetch_sources.py/fetch_legacy_license.py仅用于重新核验/取固定源，不在Go测试或运行时触发。

## 许可证与来源保留

inputx-pinyin 原始 MIT 文本及 GOLIA K.K. 2026 copyright 原样保留在 `licenses/inputx-pinyin-1.0.2-LICENSE-MIT`，完整 crate 也保留其全部原许可证。正式复制该Go依赖时应一起保留来源与此许可通知。

Pinyin2Hanzi 安装包及 PyPI0.1.1 sdist 未带独立 LICENSE 文件，PyPI 元数据与上游 README 均明确声明 MIT；这里保留原 README 声明及包/source散列，不编造上游没有提供的 copyright/license 正文。[上游原声明](https://github.com/letiantian/Pinyin2Hanzi#license)；不可变README blob及 sdist SHA 记在 legacy-license-provenance.json，原README在 licenses/Pinyin2Hanzi-0.1.1-README.md。

source-checksums.json 记录直接行为闭包，complete-source-checksums.json 记录完整 vendored crate、原文许可证、Go切片、数据与参考文件的 SHA-256。没有引用其他同名拼音库的词表或许可证来替代实际依赖。


正式位置说明：运行时为 internal/releasemeta/pinyin，完整原执行参考为 testdata/plugins/metainfo-pinyin-oracle.ndjson.gz，测试逐 record 流式读取。原工作目录的构建产物和未调用的 15MB IME 字典未带入产品；此处 source/ 保留实际调用的 syllable/segmenter/lib 源码，完整 crate 来源/全部文件校验和及固定 fetch_sources.py 仍保留，离线重建时可在独立工作目录取回完整原包。本文前面的 work 路径是原记录的来源，不是运行时依赖。
