# 整理历史普通搜索：固定 jieba-next 来源与验证

## 原版调用与本项目范围

只读参考 MoviePilot：`app/api/endpoints/history.py:188–204` 对没有 `*` / `?` 的关键词调用 `app.utils.jieba.cut(title, HMM=False)`，以 `%` 连接全部 token；标题、源路径、目标路径分别 LIKE。`app/utils/jieba.py:3–10` 实际导入 **jieba_next**，不是经典 Python jieba。`requirements.in:67` 为 `jieba-next~=1.0.0rc1`，本次参考环境安装的是 **1.0.0rc1**。

[纯 Go 实现](<../internal/historywords/cut.go>)仅实现该固定默认词典的 accurate / HMM=False 路径；没有 HMM、用户词典、动态字典或者应用 Python/CGo 依赖。普通输入保留空白与标点 token，绝非 strings.Fields 近似。显式 glob 不分词，仍保留锚定语义；用户输入的 SQL `%`、`_`、反斜线先按 token 转义，再插入分词间 `%`。SQL 三个字段仍各自匹配，不跨字段拼接。

## 上游、许可与校验

- 项目：<https://github.com/mxcoras/jieba-next>
- 版本页：<https://pypi.org/project/jieba-next/1.0.0rc1/>
- 发行元数据：<https://pypi.org/pypi/jieba-next/1.0.0rc1/json>
- 本次实际下载的 sdist：<https://files.pythonhosted.org/packages/ab/c7/82c88518395aa78116d3502362aa2a36399b9a5578583912ec1e17a72822/jieba_next-1.0.0rc1.tar.gz>
- sdist SHA256：`71d33d4f84f7fa826da3806501bf19ac3e496432d71f75fd4a9c5f1c656c67b3`，下载后核对 PyPI 元数据一致。
- 上游 LICENSE：MIT，Copyright (c) 2013 Sun Junyi；[随包完整保留](<../internal/historywords/LICENSE.jieba-next>)。
- 原始 `jieba_next/dict.txt`：5,071,852 bytes，SHA256 `7197c3211ddd98962b036cdf40324d1ea2bfaa12bd028e68faa70111a88e12a8`。
- [上游词典压缩副本](<../internal/historywords/dict.txt.gz>)：原始字节 gzip level 9、mtime=0，1,904,875 bytes，SHA256 `81f61aa0c811ac4bfaadfb23d9d8b9f38ffc9fc8330c2245c300b4fc1e2ebb11`。不删词、不改频率；默认词频总和 **60,101,967**。

2026-11-24 起，服务读取由这份固定词典生成的只读索引。运行 `go generate ./internal/historywords` 可重建 5,840,929 字节的 `dict.idx`。生成器按 UTF-8 字节序排列词条，单独记录原词典累计词频；测试逐个核对 349,045 个不同词的最终频率及总和。服务启动时只解析索引头，词条随查询按需映射。压缩副本用于生成及测试。

sdist 中以下源码和词典均与只读参考环境安装内容逐字节一致：

| 发行包成员 | SHA256 |
| --- | --- |
| `jieba_next/tokenizer.py` | `fc0d180fb7139bad3ee3a29f9d20097d6ce557927e42d00a649b9d755d306453` |
| `jieba_next/lib.rs` | `1737285b2aec91dfcc556754a956cba13883fd1b9deffb5a4e7ffc04f5a8fc73` |
| `jieba_next/_patterns.py` | `a51c7291572117c2a0b612b3472151dfeb7b76762434d767b9ef4763cfe11821` |
| `jieba_next/_dict.py` | `9c39f1fefb20a04e9d710b6837a3941563fc897f229e4c058a29b39677137c05` |
| `LICENSE` | `18ba0984839f85853b29fadaf992f7dba8fd0ca0fbeae34de2b8735222dc7a37` |

## 算法对齐细节

1. 原版 `_patterns.py` 的块范围精确为 `[\u4e00-\u9fd5a-zA-Z0-9+#&._%]+`，不是所有 Unicode 汉字/字母。
2. 默认词典逐行读词频，重复词覆盖最后频率，但 total 累加所有行，与 `_dict.py` 一致。Go 不保存零频前缀，改为枚举至字典最大词长的子串查词频，候选集合等价。
3. 按词频构建 DAG。Rust `PrefixDict.scan_from` 每个起点最多收录 **12 个正频终点**，按长度递增；无候选则单字频率 1。
4. 从后往前 DP：`log(freq) - log(total) + route[end]`，同分用 `>=` 选择后遇到的更长词尾。
5. 只有连续被 DP 切为**单个 ASCII 字母/数字**的片段才合并；已命中的多字符词不随意合并。未登录词不启用 HMM。
6. 块外字符逐 Unicode code point 输出；CRLF 是一个 token，其他空格、换行、制表符、Unicode 空白及标点均保留。

## 实跑证据与重放

[golden](<../internal/historywords/testdata/jieba-next-1.0.0rc1.json>) 是本次真实安装的 `jieba_next.Tokenizer` / Rust PrefixDict 输出，不是 Go 实现自生成的期望值。共 **328** 个输入：28 个中文、英语/混合、歧义词、符号、空白、未知字/扩展汉字示例，加 seed=20261115 从同词典抽词拼接的300组样本。golden SHA256：`e23f96a797830d582ee980f98fbde5450f921bf3651ea812ee6d997e90f1ffec`。

参考运行只读旧安装，Python 使用 `-B` 禁止写 pyc、显式 cache_dir 指向新工作树 work；不修改旧项目。Python仅为一次性 oracle 工具，不加入应用构建/启动/测试依赖。

已实际验证：

- `go test ./internal/historywords -count=1`：328组真实oracle输出逐项相等，并校验解压字典SHA。
- 自造小词典测试同分取更长词尾、12候选上限；并行读取测试。
- [数据库列表行为测试](<../internal/app/history_search_words_test.go>)：`流浪地球` 命中标题或单个路径中的 `流浪 地球`；标题只有 `流浪`、路径只有 `地球` 的记录不命中；反序不命中；显式glob锚定、SQL字面 `%_` 保持。
- 扩展 `HistorySearch / HistoryParityV031 / HistoryMP / TransferHistory` 回归通过：分词包1.217s、app17.809s，见[本地执行日志](<../work/history-jieba-regression.log>)。
- `go test -race ./internal/historywords -count=1` 通过，2.728s。Apple M1 Pro / darwin-arm64 热词典 benchmark：`流浪地球2.2023.1080p.WEB-DL` **9,776 ns/op、912 B/op、18 allocs/op**；只代表本地微基准，不代表生产请求延迟。词典为首次需要普通分词时惰性加载、之后只读复用，空查询/显式glob不会加载。
- 独立冷加载微基准 `go test ./internal/historywords -run '^$' -bench '^BenchmarkLoadDictionary$' -benchtime=1x -benchmem -count=1`：**131.13ms**，累计分配 **36,699,736 bytes / 699,568次**，加载后GC保留堆增量 **19,850,864 bytes（约18.93MiB）**。此数不含二进制中1.90MB压缩词典的静态存储，且只为本机一次实测，不是服务冷启动SLA。

Go 回归命令：

```sh
go test ./internal/historywords ./internal/app -run 'Test(PinnedDictionaryAndOracle|RouteTieCandidateLimitAndASCIIGrouping|CutConcurrent|HistorySearch)' -count=1
go test -race ./internal/historywords -count=1
```

外部 oracle 复查（仅开发时，使用已安装固定 `jieba-next==1.0.0rc1` 的独立 Python 环境）：

```python
# python -B；从仓库根运行，缓存只写 work。
import json
from pathlib import Path
from jieba_next import Tokenizer
cache = Path('work/jieba-oracle-cache')
cache.mkdir(exist_ok=True)
t = Tokenizer(cache_dir=cache.resolve())
data = json.loads(Path('internal/historywords/testdata/jieba-next-1.0.0rc1.json').read_text())
for case in data['cases']:
    assert list(t.cut(case['text'], HMM=False, cut_all=False)) == case['tokens']
assert t._rust_prefix is not None
assert t.total == 60101967
```

这是固定版本算法/数据及本地合成数据库证据，不是NAS/生产历史验收，也不声称涵盖原jieba全部功能。
