# M0 命名、集号映射、NFO 与路径实验契约

这组程序只做离线、小范围技术验证，不是整理服务。Go 标准库实现，不依赖 Python；不搜索元数据、不扫描现有媒体目录、不接管下载器种子、不发布媒体文件。

## 样本与证据

`testdata/media/synthetic.json` 全部为 **synthetic**：作品、演员、站点/发布组及 TMDB ID 都是虚构测试数据。`101–112 → S02E01–E12` 只是规则演示，不代表《绝世唐门》的真实结构。不能把这些 NFO 放入正式媒体库用于兼容性验收。

确认作品 ID、目标季集清单尚需核实；飞牛当前版本对这些 NFO 字段的实际读取 **未验证**。CLI 总是输出 `fnos_verification: not_verified_on_device`，写出 XML 不等于已入库。

`testdata/media/real-filenames.json` 当前包含从 NAS 只读采样得到的电影 `夜王 (2026) - 1080p.mkv`、国漫 `诛仙 - S04E05 - 第 5 集.mkv` 与普通剧 `生活大爆炸 - S07E17 - 第 17 集.mkv`，标记 **real_sample**。只验证名称语法，不读取视频内容，不补造元数据 ID。电影名称提取为年份 2026；国漫原始编号为 S04E05，普通剧为 S07E17，各自显示后缀与编号一致。由于没有确认作品 ID 和目标季集清单，两部剧的映射仍返回 `needs_review/unconfirmed_media_identity`，不能据此宣称元数据匹配或已经入库；不生成真实 NFO。

## 保守命名解析

支持媒体扩展名 `.mkv/.mp4/.avi/.m4v/.ts/.mov`，只接受文件名而非绝对或相对目录路径：

- `电影名 (2024).mkv`、`Movie.Name.2024.1080p.WEB-DL.mkv`；年份冲突或尾部含未支持的数字/词汇时待确认。
- `剧名 S01E02.mkv`、`S01E01E03`、`S01E01-E03`；显式范围有 50 集展开上限。
- 单集标准编号后的 `第 5 集` 或 `EP05` 显示后缀须与 E05 相同；不同编号、多个显示编号、未支持的中文数字标记进入待确认。重复 SxxExx 标记即使相同也待确认，避免多段编号误判。
- `剧名 EP101.mkv`、`剧名 第101集.mkv` 提取原始绝对编号，必须经过已确认映射。
- 普通裸数字、特殊季/第零集、重复季集标记和无法唯一解析的名字返回 `needs_review` 及原因码。没有百分比置信度，也没有猜测下一集。

`parsed` 仅表示命名语法可解析，不证明作品身份。CLI 的 `media_id` 由人工或上层已确认元数据提供；当前实验没有远程身份核对。

## 集号映射

规则绑定 `media_id`、目标季、原始编号闭区间、`id/revision` 和人工确认状态，可限定站点、发布组和编号形式。只支持固定偏移与逐集对应之一。输入 `original` 始终保持原始编号，输出 `standard` 分开保存；上层不得将标准编号再次作为原始输入套用规则。

单文件显式纠错优先于规则，但仍要求目标季集在 `known_episodes` 中且不能产生重复目标。已确认、来源条件匹配且覆盖整个文件集号范围的规则才可使用；多条规则匹配、范围跨界/越界、对应表漏项、重复目标、未知目标集及未确认身份都进入待确认。目标季集存在不表示已经播出或存在资源。规则更新不自动改写旧下载/旧入库记录；任务需保存映射版本快照。

首版实验支持单文件对应多集的解析与映射；生成单集 NFO 时要求恰好一个标准季集，多集同文件应待人工处理，不能把它伪报成单集。

## NFO 输出

通过 `encoding/xml` 输出 UTF-8 XML 声明，电影根 `movie`、剧根 `tvshow`、集根 `episodedetails`。字段含 `title/originaltitle/year/plot`、集的 `season/episode/showtitle/aired`、`uniqueid type="tmdb" default="true"` 和 `actor/name/role/order/thumb`。测试覆盖中文、`< > &` 转义及 XML 读回。

每个文档至少一个来源 ID、恰好一个默认 ID。同一 ID 类型不能重复。**单集 uniqueid 应使用单集 ID，不能把剧的 TMDB ID当作集 ID。** CLI 仅校验给定元数据格式及集号一致，作品与远程 ID 的对应关系仍由上层负责。

这套字段是候选互操作契约；飞牛是否读取文件名、ID、演员和旁车需要真实版本的小样本库验证。正式整理命名应为电影同名 `.nfo`、剧目录 `tvshow.nfo`、单集同名 `.nfo`；实验故意按 sample 名写入临时目录，不触碰既有旁车。`--out-dir` 需已存在，文件以 `O_EXCL` 创建，重复运行返回失败，不覆盖。

```sh
go run ./cmd/mediacheck < testdata/media/synthetic.json
mkdir -p work/media-nfo
go run ./cmd/mediacheck --out-dir work/media-nfo < testdata/media/synthetic.json
```

stdin 最多 1 MiB、最多 100 样本，必须标记 `synthetic` 或 `real_sample`。不接受未知 JSON 字段。退出码：0 为已请求的离线检查通过；1 为输入/程序问题；2 为样本待确认或 NFO 创建失败。JSON 保留逐样本状态，不能只看进程未崩溃。

## 硬链接与路径实验

`pathcheck` 只使用显式指定的两个现有测试目录，各创建带随机唯一名称的小文件；不遍历或读取现有媒体。不因同盘名/路径前缀相近就判定可硬链接。实际执行 `link`，检查 `os.SameFile` 文件身份和源内容；以 `link(new-name)` 加 `unlink(old-name)` 完成不覆盖的目标改名，再解除目标链接，验证源名称、身份及内容仍在。最终只清理身份仍匹配的自身临时文件。

```sh
mkdir -p work/path-source work/path-target
go run ./cmd/pathcheck --source-dir work/path-source --target-dir work/path-target
```

报告证据为 `actual_filesystem_probe`。`EXDEV`、权限不足及目录不可用均为非成功，绝不静默复制。通过只证明当前两个测试目录及挂载；**不承诺其他子卷、目录、映射或 NAS 掉线后的状态**。本机测试与 NAS 实机证据分别记录：NAS 宿主授权目录已测试，另在 UID 1000 / GID 1001 的一次性容器内完成 `/media/download` 和 Transmission 默认下载子目录到 `/media/国漫` 的两组测试，均清理成功。具体宿主/下载器/飞牛/建议应用映射和限制见 `docs/path-baseline.md`；不将这些有限实测推广到未配置的部署。CLI 不输出绝对路径及原始 OS 错误。

`ContainedPath` 验证相对路径、授权根与符号链接逃逸；测试覆盖指向根外现有文件/新文件、悬空链接、`..` 和允许的根内链接。这是计划阶段检查，不防止其他进程并发替换路径。正式执行还需文件身份核对与操作锁，不能把检查后的一次普通路径写入当成完整安全边界。

硬链接共享文件内容。源与媒体库目标可独立改名/解除链接，但不得原地改写视频；仍有其他链接时解除媒体库路径不保证释放空间。
