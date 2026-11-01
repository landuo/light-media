# S03 bytes 材料：当前材料协议与保留证据

白名单准确为 8 个 S03 consumer/native 产品加 3 个共享 material packet/runtime/transport 产品，共 11 个；另有 8 个必要可编译支持输入。S02 已交的 spool/其它框架继承，不夹带 S12 HTTP/cache、Meta25、Calendar38 或别的未完成片。

当前协商 material/v1 的 typed []byte Add 去除额外16MiB材料限制；普通请求、普通帧/回调/30秒时间预算及 selected 无重放仍保持。公开 []byte 和必要快照/最终材料仍 O(n)。不支持的任意Python对象/任意路径并未被伪造成新入口。

唯一新原raw在 internal/app/testdata/source-material-large01.json（e03f9261…）；合法torrent16777378B按原配方恢复，完整bytes/SHA由真实owned qB/TR HTTP检查。TR id901是夹具为真实协议补充字段，独立断言后才比较原recording字段，不是源expected。旧八操作及Cookie同步信用保留其原边界。

实际新小包 normal9/race4、S03 normal大材料链、逐进程host-race/worker-race完整同材料同3组链及S02受影响ToWorker installed单根分层留证。普通CLI用新normalserver/worker跑完整链。旧双race02、旧cross03与优化后双race03均真实FAIL，仍保存；批准的逐进程race覆盖不能写成同时双race通过，也不保证时延余量。所有API通用502未输出精确异常类的地方不补造timeout code。

初版owned TR fixture缺id导致新root失败，旧2root该次实际PASS；02只补固定id901和原字段投影。CLI02另在启动前因3个旧路径guard失败；CLI03仅纠正精确新程序/output字面值，产品不改。没有丢弃这些失败，也没有改payload、分组或放宽30秒。

原S02 ordinary CLI仅继承未变的外层启动/监听/auth/install/正常双host生命周期；当前共享框架的S02方向由新installed单根实测，S03的ToHost不是其替代。依赖关系与父项最终裁定见独立报告。

可移植复跑输入使用Go包内相对testdata；示例（仅自有临时目录、显式tcp4 owned peers，无真实daemon）:
```
go test ./internal/plugins -run '^(TestMaterialPacketOldDecoderEquivalence|TestMaterialPreparedRequestOwnsImmutableMetadata)$'
go build -o /tmp/owned-downloader-command-fixture ./cmd/owned-downloader-command-fixture
OWNED_DOWNLOADER_COMMAND_WORKER=/tmp/owned-downloader-command-fixture go test ./internal/app -run '^TestSharedDownloaderMaterialOriginalLargeWorkerRecoveryAndCold$'
```
不同进程race须真实编相应程序，不能只给文案标签；已有原执行与精确buildinfo/argv绑定保留。本次overlay测试自动vet明确NOT_RUN，发布后是否补vet由协调者另记。

recoverable按原SHA去重gzip；evidence-index每个logical_name映射原字节/压缩SHA，重复输入图共享一个可恢复对象而不伪造缺失快照。旧失败race只有before/aftercompile时明确无aftertest。原raw只有一份，不含程序/DB/凭据/私密host日志/登录响应/全sourceworkspace。绝对路径仅历史来源，不保证旧外部runner在任意机器可直接运行。
