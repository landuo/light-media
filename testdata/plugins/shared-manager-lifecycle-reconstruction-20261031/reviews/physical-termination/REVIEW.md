# S10 host-only termination — independent review

**有限 SDK 独审通过：无产品阻断；normal7 / race3 原始实测证据读回一致，0 fail / 0 skip。不是 S10/App 集成通过。**

## 范围与独立性

先独立读取固定 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 remove/stop、job/API 对象捕获与现 SDK 实现，再读 owner 方案。产品仅审两份新增文件及直接依赖/既有邻接测试；未改产品、ledger、worktree 布局，未执行 Go、任何 fixture、外连或进程探针。报告只写当前目录。当前 Go HEAD 为 `11f922896377cb0d02bf481ef0df10f11a1cb4c4`，不把保留的 dirty 候选当成已提交。

冻结输入：
- `internal/plugins/terminate_transport.go`：`6f015c11caacbee9579a0b9d056683bc11134ec1878f52fdd33f59830f68be5b`
- `internal/plugins/terminate_transport_test.go`：`c500fa972b3ac42191a9e9b703ed2d1ac13c88ec2e2b8671c3c59935e437b776`
- owner checkpoint03：`5c4f8c66e4f9e9091c964fdd3ac48f2e319d3feea62344d2ca2290ac27adbc20`

## 原源与实现核对

1. **逻辑删除不增业务 stop。** `MoviePilot/app/core/plugin.py:156–192,619–639` 是 remove→stop→close→stop_service；close 异常会跳过后项。删除 manager 映射不是销毁所有外部 bound method 引用；`get_plugin_services/get_plugin_apis` 返回捕获对象的方法，scheduler/router 独立保存。现 SDK `protocol.go:939–985` 的 Stop 确实发业务 `stop`，不能当作这条 remove 的新物理清理。这里只证明新原语可避免增发，不证明 App retained jobs/API 实现已验收。
2. **只终止原有 exact child，无帧。** 新文件没有 `exchange`、`call`、`transport.write`、新命令或 PID 操作。serial gate / concurrent state 取得唯一 claimant 后，仅调用该 Client 原有 cancel/session.abort，并等待 `c.done`；`protocol.go:718–719` 明确仅真实 `cmd.Wait` 后关闭 done。Concurrent 另等 readerDone，再有界观察已存在 writes/hosts。等待不持 m.mu、call.mu 或 transport.writeMu。
3. **复用现终态。** serial 复用 gate/stopped/terminal，concurrent 复用 stopDone/stopErr，后来的旧 Stop/CloseTransport 观察同一终态。已保存的非 intentional 错误不覆盖。只有新入口映射私有 `transport_terminated` 为 nil；旧 Stop 仍看到该标记。该 nil 是物理回收收据，不是 child exit 0、不等于 graceful 协商或业务成功。取消附近的独立 crash 不作健康归因。
4. **pending 与已 Wait 分开。** nil/invalid 返回 invalid_invocation 无操作；预取消返回 transport_termination_pending 无状态改写。未取得 serial gate 或观察其他 claimant 超时也返回 pending，不能标回收完成。最终策略②让这些等待仅用 caller ctx；runner.Timeout 只用于 claimant 后的 hosts/writes 收敛。真正 claimant 即使 ctx 到期仍先等真实 child/reader。host 未 drain 时的 execution_canceled 也不是 host drain 收据。
5. **兼容边界不改。** 初读与冻结后核同 protocol/concurrent_session/worker/close_transport、transport、fixture、邻接测试的已记录 SHA。旧 legacy SupportsTransportClose=false、CloseTransport unsupported 保持；新方法直接区分 Client 是否已有 concurrent 状态，不依赖 transport-close 广告，不造权限/协议。未伪造旧 concurrent 握手或声称所有 SDK 版本实测兼容。

## 审查提出并关闭的两个有限问题

- 候选曾用 runner.Timeout 包整个新 API；仅 App 改 Background 不能避免 nonclaimant pending。最终只用原 ctx 等 admission/stopDone，runner.Timeout 留在 claimant 后收敛。App 必须在独立 exact-object retirement claimant、无 owners 与 actual drain 两事实成立后调用，并使用不受普通请求取消的收尾 ctx；本审查未替 App 自动证明这些前置。
- 初版第三根仅测 nonclaimant deadline，未覆盖 claimant 超时分支。最终在同根增加 `claimant_deadline_still_reaps`：实际 child/reader 已结束、held-host 的 Drained 仍未结束、重复新法/Stop 保持错误，最后释放 host 并观察实际 Drained。不增加测试根或 fixture。

## 实测门：原始输出已独立读回

新 3 根：LegacyNoBusiness、ExistingConcurrentNoBusiness、TerminalAndDeadline（完整名称见 owner checkpoint03）。代码断言包含透明 writer 的写尝试/字节数为零、close→stop_service 仅一次、nil/预取消无操作、unsupported 不改变 legacy 对象、真实 Wait 非零退出与 intentional 收据分离、重复终态、4-way concurrent claimant、先前 serial 错误保留、busy gate pending、concurrent observer pending、claimant deadline/host drain 分层。

normal 加 4 邻接根，race 仅新 3 根。新 3 与旧 PendingCall 2 使用独立 `owned-concurrent-fixture`（legacy 为 --legacy）；旧 PersistentSession 2 使用既有 self-exec `TestPersistentWorkerFixture`。不能把 normal7 都称为独立 public artifact 流程。实际已核 exact roots 全 PASS、无 SKIP/FAIL、4 个命令各 exit 0 且各 stderr 0，冻结 SDK2 与当前字节一致。race host 与独立 child 构建命令均含 -race，当前两套 runtime 二进制 SHA 与各 result 的运行输入一致；未执行这些制品。

### 实际证据索引

- **normal7**：`evidence/s10-terminate-normal01`，结束 `2026-10-31T16:26:20.606340Z`；7 根 + 第三根 4 子例均 PASS。result SHA `2ff6b21e611e6b001d4f5b3644cae256916419f3c19ca9d2dc366b1e9f40dd2c`，tests.stdout SHA `c3e6f747cabc280988dcc4459a107dcedc0fbab342d4bd7ab428f2e5db4b12c0`。1921 输入的 before/after-compile/after-test JSON 字节 hash 均 `a5ac56ac87f7a035be9850d5f7f7a3b0244bbb5a949d01e37c1a98fb645c16ef`。协调者报告旧外层 session 已不可查询；本报告不虚构其外层 exit 0，使用 4 项原始 exit 文件和 test JSON 判定。
- **race3**：`evidence/s10-terminate-race01`，结束 `2026-10-31T16:32:34.382576Z`；3 根 + 同 4 子例均 PASS，无 race 警告输出。result SHA `489fdb023f29caa2ac76e9a02022665b323a72965af8f7a61bd3f4ef638654d5`，tests.stdout SHA `0ef8ceccdc42d2ce32d47ae0b4c38ae13b6b0fce4344ec1aceab26079f0a0a51`。1958 输入的 3 份 JSON 字节 hash 均 `c6ad40735e44def3a80293e343f505815d494d1600b2aca35c7f9210c73a3ddb`。
- **终态分层**：normal legacy 确实观察到 `exit status 1`，其他强制终止为 `signal: killed`；race 所有该类收据观察到 `signal: killed`。这是新 API 有意物理终止并真实 Wait 的证明，绝不是 child 正常退出、业务 stop 成功或 graceful 兼容的证明。
- **最终只读回查**：当前 SDK2 未漂移；对 owner 实施前清单中的 93 个已有 SDK 输入逐一重新哈希，drift 为 0。详细根列表、命令/输入/二进制 SHA、直接审查文件记录见 `final-readback.json` 和 `input-bindings.json`。

## 停止线

仅 SDK 原语与限定门。未声称 App 最后 owner/drain、retained jobs/API、原源全量等价、真实插件/下载器/NAS、CI、推送或 full S10 已完成。无全历史容量矩阵重跑。
