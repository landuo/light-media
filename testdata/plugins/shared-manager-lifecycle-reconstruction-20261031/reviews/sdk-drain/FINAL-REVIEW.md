# S10 SDK drain：有限最终独审

**结论：ACCEPT SDK drain 三路径增量；blocking = []。** 当前产品与 checkpoint02 同字节；normal 4 根、race 4 根实际通过。不是 App retained/remove/API 集成、完整 S10 或发布验收完成。

## 实际执行与连续性

| 门 | 实际终态 UTC | 独立输入 | 运行结果 |
|---|---|---:|---|
| normal01 | 2026-10-31 15:48:33.494213 | 1919 | 4 根 + 4 子例 PASS；无 Skip/Fail |
| race01 | 2026-10-31 15:49:13.217981 | 1956 | 同 4 根 + 4 子例 PASS；无 Skip/Fail/数据竞争警告 |

本审查读取 result、所有命令 exit、test2json 原始事件及冻结输入；两门的 before / after-compile / after-test 内容分别完全相同，并逐项核回当前输入 SHA/bytes，零漂移。各自 fresh worker/test 共 4 个产物 SHA 与结果一致；缓存生成的 test main 原件与独立保留副本均匹配输入。完整绑定见 normal-race-readback.json 与 final-seal.json。

协调者另提供外层工具终态 normal session 92795 / tool0406bb exit 0、race session 31336 / tool4f2be0 exit 0；本审查并未重新执行或独立 poll 这些 session。原 test2json 时间带 +09:00，与 result 的上述 UTC 一致。

两个 driver 字节相同，按实际参数分别 normal / race；先构建 owned worker，再仅编译一次 SDK test program，最后执行精确 4 根。runner 复制既有环境，仅设置 GOMAXPROCS=2、CGO_ENABLED=1 及精确 LIGHT_MEDIA_CONCURRENT_FIXTURE 路径；不清洗 proxy，没有新增 HTTP 客户端或代理旁路；不据此推断 Go 工具所有潜在依赖访问。源码闭包包含 driver、Go/模块输入及新测试；没有用缺 fixture 的 Skip 充当通过。资源数据为逐阶段采样和 1 GiB 执行前 floor，不冒称连续峰值监控。

## 可接受的机制与测试层级

- caller Wait 返回与 actual drain 被拆开。有效 normal result 路径在 host callback 等待完成、结果发布及 life 结束后一次通知；不要求整个常驻 worker 退出。
- terminal 路径在锁外等 Client.done（由真实 cmd.Wait 后关闭）及所有该调用 host callback 返回，再通知；并发通知由 sync.Once 保证。没有在 mutex 内新增 Wait/回调，也未改变旧 nil observer、wire 或 admission/fence 行为。
- 实际独立 worker 验证：成功、多订阅、复制结果、已接纳调用的零字节本地 message_limit、caller cancel 后仍存活的 worker handler / host callback，以及 terminal abort 后真实进程回收但 callback 尚未退出时 drain 不能关闭。
- actual_transport 的 worker_io_failed/非零 worker 退出是自建 fixture 的预期故障注入，测试显式保留并断言该终态，不把它伪称 graceful exit。
- controlled_reaper_and_once 是明确标注的内部同步单元：手动 held reaper channel 与通知竞争。它不是实际系统延迟 cmd.Wait 的收据，也不声称穷举 m.complete/EOF 所有竞态；真正进程层由 actual_transport 子例覆盖。
- nil/零值、取消 admission、实际 owned legacy fixture 的现有 StartCall 拒绝、普通 nil-observer Call、BeginShutdown fence、CallCleanup 与 Stop/reap 的旧约束均由本轮根覆盖；没有授权放宽或拒绝真实业务插件新行为。

## 对后续 App 的必要边界

App 引用释放必须同时观察 caller/phase 结束及 Drained，不能把单个信号替代另一事实。强制 App.Close/替换必须先触发实际 stop，不能在触发 stop 前等待所有 terminal drain 引用；不合作 callback 不返回就不能宣称 drained。

本结论只接受 SDK 通知机制和上述精确测试。未证明 self-remove 完整宿主链、保留 job/API bound object、App normal/race/CLI/cold、真实业务插件、浏览器、全 S10/全 61、CI 或 Git/远端交付。后续 App 的改动不反向改写本次已冻结 SDK 历史证据，也不能借本结论免除 App 自身验收。

本审查只读源码/原始证据并写本审计目录；没有 Go/build/test、服务、Python/source import、网络或产品/ledger 写入。历史静审保留，不覆盖其“当时尚未执行”状态。
