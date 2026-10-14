# light-media

独立的 Go 影视自动化项目，以 `docs/design-v1.4.txt` 为需求基线。当前阶段为 **M0 技术验证**，尚未实现可用的影视管理应用。

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
| `go run ./cmd/metadatacheck` | 三作品与两单集的标准 TMDB 身份；stdin 提供自用凭据 | 最多五次固定官方只读 GET；未配置为 unverified，零请求 |
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

`experiments/metadata-nfo` 为剩余三代表样本准备标准 NFO：只接受固定五条完整标准元数据核对报告，默认输出计划，显式 apply 才在独占新 work 子目录创建五份 XML。它不联网、不创建视频或演员、不写 NAS；保留 supplied evidence 与输入/NFO 哈希，不把报告中的 `live_request` 声明当成独立的来源真实性证明。未配置报告会在落盘前拒绝。真实来源及飞牛读回验收仍须另行完成，见 `docs/m0-final-steps.md`。

## M0 证据

详细结果与仍待验证的门槛见 `docs/m0-report.md`，逐项状态见 `docs/m0-acceptance-matrix.md`。SQLite 独立基线见 `docs/sqlite-baseline.md`。实测结果只对应标明的版本、路径、样本和负载；完整应用内存、长时间稳定性、自动化闭环及 P1 插件尚须后续验收。

所有旧项目保持独立。本项目没有旧系统数据库迁移、Python 运行依赖或生产服务部署。
