# S02 本机备份材料：去除额外 16 MiB 材料限制

本白名单仅含 8 个 S02 consumer、12 个 material/v1 框架生产文件和 1 个私有 spool 文件。没有纳入 S03 native 流式 Add、S12 HTTP/cache、Meta25、Calendar38 或其他未完成父项。公开 LocalTorrentBackup.Prepare 的 []byte 返回保留，最终仍 O(n) 内存；不是任意文件 RPC 或无限一般消息。

协商后的私有材料通道采用固定有界帧、完整长度/SHA、真实调用范围/代际/精确 capability、一次消费和 scope 终态清理。未协商旧 peer 的旧 prepare/read/release 路径及普通消息/帧/调用/时间预算保持。只有 S02 普通宿主启用该协商。spool 只删除本 owner 新创建文件，不清 S02 确定 hash.torrent、原备份或 S12 cache。原只读备份/重建/年龄清理信用见既有 migration/evidence/s02-local-backup。

新原基准确为完整原 transfer + 原 C fast-bencode 两次 decode/一次 encode，输入 torrent16777333B/resume16777309B，重建16777450B。唯一原 raw 位于 internal/app/testdata/source-material-large-s02.json，SHA d65cfcde5d0f71a9c331a46e06d065bbefd5d48696c0e63e7964b77f84487f7e；test 仅按原配方恢复输入、解压原 expected，全 bytes 比较，不用 Go codec 生成期望。完整历史原插件仍在 testdata/plugins/torrent-transfer-1.10.3/torrenttransfer.py；core8a83 与外部插件 hash 分开记录。无新增第三方运行依赖/许可证替换，既有来源及许可证保留。

实际门：spool normal4/race2；框架真实 serial/concurrent child normal8/race4；S02 App normal2、旧未协商 SDK2、新 App worker race1、受影响既有 installed-worker normal2；新普通 server + 安装 worker 两进程冷启 CLI 13 控制调用/4 业务/2 artifact GET、host23→51自然退出0。CLI原result SHA 869ef30205d55d7a02f3c8d1c5cdbdc51385a52f23ce978f984f2b4d11918f9b。框架/组件/普通CLI各层证据分开，不冒真实下载器/系统浏览器。自动 vet 在这些虚拟overlay门明确 NOT_RUN。初版测试上界2却安排3请求为静态HOLD，未执行；02仅测试夹具2→3修正，原件外存保留。

可重跑的新 Go 数据/测试均相对路径，不依赖审计目录：
```
go test ./internal/materialio
go test ./internal/plugins -run '^(TestLocalTorrentBackupWireAndBoundary|TestLocalTorrentBackupPagedBytesAndCleanup)$'
go test ./internal/app -run '^TestOriginalLocalBackupLargeSourceFilesystem$'
go build -o /tmp/owned-local-backup-fixture ./cmd/owned-local-torrent-backup-fixture
OWNED_LOCAL_TORRENT_BACKUP_WORKER=/tmp/owned-local-backup-fixture go test ./internal/app -run '^(TestSharedLocalBackupMaterialLargeWorkerAndCold|TestSharedLocalTorrentBackupActualWorkerRecoveryAndScopes|TestSharedLocalTorrentBackupActualWorkerPagedAndCold)$'
```
最后一门仅自有临时 App/SQLite/备份和显式 tcp4 两GET artifact peer，无真实下载器。两个既有 installed-worker roots 已按当前真实 material/v1 协议维护其共享计量 helper 并实际补跑：完整 root 业务/权限/源失败/备份/temp/cold 函数体不变，要求准确 grant 摘要、prepare_material 普通 callback=1、调用终态自有 spool 为空和旧 HostCaller 拒绝。当前不分配 legacy handle，因此旧 released-handle 观测必须为空，不能伪造 true。旧 SDK 两根继续以明确未协商路径验证 legacy read/release/cleanup。普通callback只记录prepare_material，私有帧不冒普通read/release。

recoverable/ 内各 gzip 解压即原始证据字节，evidence-index.json给原/压缩SHA；不含DB、私密host日志、登录响应、凭据、程序/.so或整sourceworkspace。历史绝对路径仅来源/编译闭包证据，不是假称可在任意机器直接重放旧工具命令。父项最终准入/采用/ledger由协调者及独审决定，本准备不改ACTIVE、不提交/推子片。

发布前曾发现旧 helper 与当前 fixture 协商不匹配；publication02 的豁免文案不可作为保留已知坏测试的依据。03 仅新增维护后的旧测试文件和相关实际 normal2/独审证据；生产21件、fixture f97b8335…、新large根及原普通CLI字节不改，没有重跑无关全套。原 publication02/先前准入及暂停记录保持时点原件。
