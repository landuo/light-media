# light-media

独立的 Go 影视自动化应用，以 `docs/design-v1.4.txt` 的 P0 为需求基线。Vue 页面嵌入 Go 服务；运行时使用 Go + SQLite，无 Python、Node、Redis 或独立数据库依赖。P1 插件、辅种与转移做种仍按后续阶段实施。

## 本次 NAS 使用入口

访问 [Light Media](http://192.168.8.129:3120)，管理员登录沿用原 MoviePilot 的账号和密码；新应用仍使用独立数据库和项目目录 `~/projects/light-media`。这条说明不包含任何凭据值。新建其他空白实例时，仍按下方流程首次创建管理员。

NAS 已完成实际一致备份/恢复演练：旧会话失效、重新登录正常，恢复后保持自动化暂停，并恢复备份时的订阅及飞牛库配置。首次开始前建议先核对已有库与不确定候选，再用小范围下载验证路径，最后建立订阅。最终部署包已通过健康和五站材料预览核对，已恢复全局自动化；72小时稳定性结果以 [P0验收记录](docs/p0-acceptance.md) 为准，不因恢复演练成功而视为已完成。

## 使用应用

```sh
make build
./bin/server --data-dir ./data --listen 127.0.0.1:3120
```

打开 `http://127.0.0.1:3120`，首次创建管理员，然后在设置中配置 TMDB、站点、下载器、媒体目录与飞牛影视。构建前端需要 Node；已构建的程序启动时无需 Node。Docker 构建及挂载示例见 `Dockerfile` 和 `compose.yaml`；容器运行用户需具备配置盘与媒体目录的权限。

页面包括发现榜单、影视/演员详情与相关推荐、五站资源搜索和筛选解释、qBittorrent/Transmission 多实例下载、订阅与周/月日历、手动整理、已有库、任务、通知、日志和设置。

v0.2.2 将下载与订阅的目标改为“下载器 + 下载目录”，由“目录管理”独立配置目录监听、媒体库目标和整理方式。新增文件稳定性检查、下载完成核对与持久化监听记录；五站搜索并发执行，结果逐站显示。配置方法与验证边界见 [目录与搜索说明](docs/directory-search-v0.2.2.md)。

v0.2.1 增加 Lucky HTTPS 子路径入口 `/light-media/`，保留内网根路径访问；统一发现页、海报卡片、侧栏、文件管理、历史与移动端视觉。反代配置约定见 [HTTPS 入口说明](docs/reverse-proxy.md)。

v0.2 按 MoviePilot v2 的主要操作重新调整：全局电影/剧集混合搜索、直接关键词搜索资源、季订阅与起始集数、多季分别订阅、独立站点管理和账户统计、下载器手动配置、文件管理内识别整理及逐文件整理历史。支持的五种站点适配器可手动建立多个实例；这不等于任意站点模板都受支持。上一版的功能测试不能证明操作体验已对齐，原来的“P0完成”判断过早。当前对照、验收与明确差异见 [v0.2 操作对照](docs/operation-parity-v0.2.md)。

开始订阅前，在已有库页面扫描目标目录。能确认 NFO 身份的文件直接登记，未识别或矛盾项进入待确认列表，可选择影视及官方季集后原位登记。原位登记只更新本应用索引，不改视频或旁车。

搜索资源后选择下载器与下载目录，确认实际文件清单、大小及复核提示，再提交下载。启用匹配的目录规则后，下载完成由监听任务核验并整理到规则的媒体库；无规则或规则停用时仅下载。订阅保存同样的下载器与下载目录。需要编号或身份纠错时从文件管理或下载记录打开手动整理。默认硬链接，跨文件系统会停下供核对；复制或自动回退复制须显式选择。所有方式均保留下载源文件。

订阅的预计更新时间与检查频率分别配置。自定义星期/周期、停更/延期/加更不会凭空创建已播集；窗口外仍按保底频率检查已知缺集。暂停、取消、内容排除和全局暂停各自保留语义。

日志中心提供实时跟随、历史游标分页、站点/模块/流程/任务筛选、当前筛选导出，以及可自动到期的局部 DEBUG。后台失败保留通知与任务时间线。诊断导出不包含登录凭据。

## 配置与恢复

- 设置中的密码、Cookie 和令牌以 AES-GCM 密文保存在配置数据库。`data/secret.key` 为恢复所需密钥，权限为 `0600`；不要加入 Git 或诊断包。
- 页面创建的数据库备份是 SQLite 一致快照，可下载。另行保管匹配的 `secret.key`；仅有数据库无法恢复凭据。
- 页面恢复前须暂停自动化。服务会校验备份并自动重启，恢复后清除会话并保持暂停；重新登录、核对下载器和文件状态后再恢复自动化。数据库恢复不回滚外部文件与下载器。
- 如需代理，在部署环境设置 `HTTPS_PROXY`/`HTTP_PROXY` 及 `NO_PROXY`；局域网下载器和飞牛应走直连。TMDB 图片由应用缓存供浏览器访问。
- 飞牛同步会验证实际文件路径、媒体库与季集父链。请求扫描成功仅表示已受理，尚未核实的结果保持待核对。
- 原始种子材料仅以AES-GCM密文归档，默认256MiB，设置范围32–2048MiB；未确认孤儿24小时后清理，已关联下载的材料不自动过期。满额会明确拒绝新预览，不自动删在用材料。P0只供核心解析，不开放插件/HTTP原材料下载。
- 本次 TMDB 配置按授权取用了原 MoviePilot 项目的共享内置 Key，未在文档中保存其值；长期使用建议在设置中换成自己的 TMDB 凭据，避免依赖共享额度。

既有 M0 探针与协议证据仍保留供开发复现。P0 的实机交付、版本和边界以 `docs/p0-acceptance.md` 为准；72 小时稳定性指标须以实际持续测量为准。

## 开发环境

Go 1.26；本轮使用 Go 1.26.2。默认包含纯 Go SQLite 与 cgo 对照驱动；纯 Go NAS 构建使用 `-tags modernconly`、`CGO_ENABLED=0`。

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
```

## 验证程序

| 命令 | 用途 | 实机副作用 |
| --- | --- | --- |
| `go run ./cmd/integrationcheck` | 从 stdin 接收授权配置，探测 qB/Transmission 与五站鉴权、有限搜索 | 登录、只读接口与单页搜索；不加种/暂停/修改设置 |
| `go run ./cmd/metainfocheck` | 五站各最多一页搜索、一份种子材料，验证 bencode 与文件结构 | 登录、搜索及获取材料；仅在内存检查，不保存私有种子、不提交下载器 |
| `go run ./cmd/metadatacheck` | 三作品与两单集的标准 TMDB 身份；stdin 提供授权凭据 | 最多五次固定官方只读 GET；未配置为 unverified，零请求 |
| `go run ./cmd/fnoscheck` | 从 stdin 接收影视账号配置，读取版本、库与少量作品 | 登录与只读查询；不扫描、写入或删除媒体 |
| `go run ./cmd/fnosprovidercheck` | 核对飞牛同源搜索是否返回明确的标准作品/单集身份 | 最多五次版本/登录/查询；不重新刮削，不把 Trim ID 推断为 TMDB ID |
| `go run ./cmd/mediacheck` | 文件名、人工集号映射及 NFO 编码 | 默认离线；显式 `--out-dir` 只在给定工作目录创建新文件 |
| `go run ./cmd/pathcheck` | 明确两个目录后验证硬链接 | 创建并清理自己独占的随机小测试文件 |
| `go run ./cmd/sqlitebench` | SQLite 驱动/事务/异常退出恢复/快照基线 | 新建独立合成数据库，默认清理；不打开业务数据库 |
| `go run ./cmd/nasbench` | Linux 受限容器中测量合成 SQLite 子进程与 cgroup | 固定 `/data` 测试目录；仅适用于隔离实验 |

合成样本可直接复现：

```sh
go run ./cmd/mediacheck < testdata/media/synthetic.json
go run ./cmd/sqlitebench --driver modernc
go run ./cmd/sqlitebench --driver mattn
```

三类真实文件名在 `testdata/media/real-filenames.json`。该输入只验证名称，没有伪造元数据身份；待确认样本使用非零退出码。配置契约见 `docs/integration-contract.md`、`docs/metainfo-contract.md`、`docs/metadata-contract.md`、`docs/media-contract.md`、`docs/logging-extension-contract.md`。

实机凭据由本地密码库/现有授权配置提供，经内存和 stdin 传入。不要将实际输入放入命令行、环境变量、Git、普通配置或报告；`work/` 不纳入版本控制。测试失败时检查结构化原因码，不要关闭 TLS 校验或把接口错误当成空结果。

新 NFO 的隔离实验工具在 `experiments/fnos-nfo`，本地合成样本生成器在 `experiments/nfo-fixtures`，离线字段比较器在 `experiments/nfo-compare`；可用 `make experiments` 构建。前两者默认 dry-run，比较器无网络和写入。飞牛实验只认领新 UUID 库及创建前收据，允许显式操作自己的临时库；它不是生产写入接口，也不证明全局演员/图片缓存隔离。实测结果及未覆盖的字段见 `docs/nfo-verification-plan.md`，下载器、宿主、建议应用的路径映射见 `docs/path-baseline.md`。

HTTP 验证 CLI 的 stdout 为结果 JSON，stderr 为每次运行独立、有总量限制的 JSONL；通过生成的 run、target、request ID 关联。凭据与完整 URL 不进入日志。日志接线验证不能代替业务成功判定。

`experiments/metadata-nfo` 为三代表样本生成标准 NFO：只接受固定五条完整标准元数据核对报告，默认输出计划，显式 apply 才在独占新 work 子目录创建五份 XML。合法 TMDB 身份同时写入同值 `tmdbid` 与 `uniqueid`，单集使用自身 ID。它不联网、不创建视频或演员、不写 NAS；保留 supplied evidence 与输入/NFO 哈希，不把报告中的 `live_request` 声明当成独立的来源真实性证明。未配置报告会在落盘前拒绝。

`experiments/real-nfo-compare` 离线核对五条报告、实际 XML、飞牛七节点和三条服务返回文件路径；固定支持历史 uniqueid-only 与新 tmdbid+uniqueid 编码。真实官方请求及两轮实际关联各 30 项检查均通过。比较器不独立认证来源，也不声称飞牛直接回传标准 ID；真实执行证据与兼容限制见 `docs/m0-final-steps.md`。

## M0 证据

详细结果及兼容限制见 `docs/m0-report.md`，逐项状态见 `docs/m0-acceptance-matrix.md`。SQLite 独立基线见 `docs/sqlite-baseline.md`。这些文件保留 M0 当时的结论；其中“完整应用/自动化闭环尚待实现或验收”仅描述历史技术验证范围，不表示本次 P0 应用仍只有探针。当前应用的浏览器、下载器、整理刮削、飞牛和恢复演练证据以 `docs/p0-acceptance.md` 为准。最终部署包已通过核对，完整应用72小时混合负载仍未完成；P1 插件尚未交付。

所有旧项目保持独立，不导入或改写旧系统业务数据库。实际部署使用新应用目录和端口。
