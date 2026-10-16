# 完整 MetaInfo / MetaBase 的 Go 移植

本批新增纯 Go 的完整对象、标题入口、对象修改方法和两套拼音依赖，暂未接入 RSS、App 或正式持久化消费者。既有 `releasemeta.Parse` / `Metadata` 保留，两个正式动漫 engine 目录没有复制或改动。不能据此称原版 RSS cachedContext 已完成；原来 28/14 个录制 stub 的局限仍见 `rss-context-go-plan.md`。

## 入口与参数存在性

```go
parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{
    Options: releasemeta.Options{
        SourceMode: "rust", // 必填：rust 或 legacy
        CustomWords: globalWords,
        ReleaseGroups: groups, Customization: customization, MediaExts: extensions,
    },
})
object, err := parser.ParseTitle(title, subtitle, localWords)
```

`CustomWords` 是全局配置帧。`localWords == nil` 对应 None；非 nil 指针即使指向空切片也表示显式 `[]`。Rust 的显式空列表清除全局识别词，legacy 的空列表继续采用全局词。构造器保留 global frame，避免只冻结 effective options 后丢失 `original_name` 的第二次构造：legacy 用全局默认值重建，Rust 用空词重建。匹配后原字符串没有变化，也可能存在 `apply_words`，因此不能靠前后字符串比较判断应用状态。

配置列表和平台表在 New 时复制；合法字符串输入只在 Parse 时复制必要的可选参数。nil 平台表采用实际安装源码的完整 418 项内置表，显式空 map 则保持为空。返回的字段是实际包装器对象；Rust 失败时按原入口重试 legacy，并保留这种源差异。无效 Go UTF-8 返回明确错误，不猜测替代字符。

## 对象、修改与保存

`MetaObject` 保留实例字典的键存在性，34 个公开字段、17 个派生属性及 legacy 私有实例标志。`Has` 区分实例缺键；`Get` 提供公开类默认值；`Number` 返回精确整数的防御性副本。Rust 包装器实际写入的 32 个实例键单独保留，不能补造其未写入的专用 Bangumi/AniList 字段。

- `ToDict` 是源 `MetaBase.to_dict`：vars 的稀疏键加其五个派生项，用于源可见输出。
- `EncodeState` / `DecodeMetaObjectState` 是版本 1 的内部缓存格式，包含 kind 与原实例字段，保留缺键、null、空串、零、列表顺序、私有字段及任意精度整数；解码使用 json.Number，拒绝浮点状态和尾随内容。不能把 ToDict 当成恢复完整对象的格式。
- `ShallowCopy` 产生新实例字典并共享列表、map 值；`DeepCopy` 分离 JSON 形状的全部可变值。数字按 Python 值语义复制。原始 Instance/Get 可变值供内部源状态访问；宿主应使用对象操作，不能直接修改整数或对原切片 append 后假定与 Python 列表追加等效。
- `SetName`、`SetSeason`、`SetEpisode`、`SetEpisodes`、`IsInSeason`、`IsInEpisode`、`InitSubtitle` 保留源修改条件及异常前的部分更新。返回的 Python MetaBase 对象即使由 Rust 构造，其 `InitSubtitle` 仍采用 Python 对象方法。
- `MergeLegacy` 保留 Python truthiness；`MergeRust` 只用于 Rust 构造/目录依赖，按 None 填充、保留 Some("") / Some(0)，不合并 episode_group。二者没有合并成一个近似实现。

识别词偏移新增内部三值 helper（结果、applied、fallback），原有两值 wrapper 保留既有调用契约。完整标题入口复用正式两套动漫原始输出；名称重试、字段赋值和 source None/空值消费独立处理。

## 原执行证据

全部参考为合成数据，没有真实媒体、配置、URL 或凭据，没有调用网络元数据识别。安装文件和服务保持只读。Python / Rust 只用于离线参考，运行时不调用它们。

|参考|完整保留的范围|
|---|---|
|metainfo-title-oracle.ndjson.gz|1,166 条实际安装完整 MetaInfo：1,158 个完整返回对象、8 个已定位的 legacy 原不终止输入|
|metainfo-video-oracle.ndjson.gz|1,516 个普通视频、名称/顺序/年份/资源/编码/季集/显式身份/拼音完整对象|
|metainfo-mutators-oracle.ndjson.gz|144 组实际 MetaBase、354 个操作步骤，另有 1,176 个位深边界|
|metainfo-rust-merge-oracle.ndjson.gz|162 对真实固定 Rust model 与 merge 函数编译执行|
|metainfo-pinyin-oracle.ndjson.gz|2,629 个单音节/变形、180,608 个真实原 wrapper 名称、2,508 个完整有序分词|

每个正常标题比较 kind、34 属性、17 派生项、完整 ToDict、全部实例键，并再次校验状态持久化恢复后的源字典。legacy 原引擎确定不终止时返回 `source_non_terminating`，不挂死或静默成功。普通视频语料中一次主机忙导致的 2 秒墙钟超时已只读重复执行三次、返回完全相同完整对象，单独记录为重试，不冒称源不终止。

所有正式大参考都是 gzip NDJSON；Go 测试逐记录读取，不展开大 JSON 数组。完整 SHA、条数及原 JSON SHA 在 `testdata/plugins/metainfo-reference/fixture-manifest.json`、各头记录中。原执行脚本、实际 Rust 函数、固定依赖源闭包及全部来源校验和都随参考保存。`rebuild_json.py` 可以离线还原参考内容；安装版脚本可通过现有只读容器会话用 stdin 执行，结果仅 stdout。`prepare_rust_merge.py --source <固定Rust源码目录> --output-dir <独立工作目录>` 重建原 Rust merge 参考。

安装 metainfo.py SHA 为 `7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9`，metavideo.py `ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077`，metabase.py `0156c105bf92991332a8742b9a4af4e1f3a74cf4b6f08107e0ed6de9d546f292`，metaanime.py `d45433c77656bba3203326943c9fe5b3ae6cbe8c7514e631fb16cc7dc1f4afca`。固定 Rust parser.rs 来自提交 `017b3a0e4ce2415d641c0eeb84ac907f9f10450c`，SHA `252b7e50adde31f7a96ce676c16a88dcb1f269fdad10737d1e4da9d1aa80795f`。GPL-3.0、原引擎、cn2an、Unicode 与拼音依赖的许可证和来源说明均保留。

## 宿主接入顺序与边界

下一窗口先接真实 RSS 缓存中的完整 MetaInfo，再接局部词重建与成功/失败识别分支，最后迁移订阅消费与状态保存。core `copy.copy(Context)` 只复制 Context 容器，不应误变为复制嵌套 MetaInfo；仅非空 `apply_words` 才用原 torrent.title/description 重建局部 MetaInfo。插件候选 API 的 deepcopy 需同时深复制其他 Context/MediaInfo 字段。旧五字段快照缺失身份，必须从原始输入和冻结帧重新生成，不能凭空升级。

仍未包含完整 MetaInfoPath、目录选择/合并、MediaInfo 重建、原 RSS producer、宿主事务或任何正式消费者接线；MetaInfoPath 不是 RSS 标题入口前置。有限原执行参考通过不代表所有输入已穷举等效。通用用户配置 regex 的完整 Python regex / fancy-regex 方言仍是既有匹配器边界；本批只独立展开所消费的固定源码类与 ASCII casefold。材化季/集列表超过 100,000 个元素返回 `metainfo_materialization_limit`，这是明确资源策略差异，原 Python 无同样上限。宿主 UI 与资源限额需要在接线时明确采用该错误。

## 当前验证结果

正式包普通检查通过7.738秒、拼音包1.598秒；竞态检查分别49.277秒与6.597秒，两套动漫引擎竞态检查分别30.887秒与10.979秒，vet退出0。后续纳入应用的本地Go/modernconly全量检查均通过。NAS的CGO0 linux/amd64二进制在256MiB容器/GOMEMLIMIT64MiB中逐条读取完整参考：releasemeta17、pinyin4、legacyanime2、rustanime8个顶层用例全部通过、零跳过、无OOM；本机与远端SHA一致。详见 [plugin-status-nas-verification.json](plugin-status-nas-verification.json)。该结果未接通RSS消费者，不代表原缓存链路已经交付。
