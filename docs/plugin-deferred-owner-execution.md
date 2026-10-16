# 插件终态宿主调用

本切片已接入正式代码并完成 App 联合、并发与 NAS 隔离验证；尚未部署。它不新增已迁移插件数量。

RemoveLink 的 configure/apply 和 PluginUnInstall 的 apply 是固定版本 SDK 的终态调用：工作器必须只提交一次当前执行绑定的请求，并以宿主的私有握手原样完成 SDK 协议和进程退出。握手不执行清理或卸载，也不保存为业务结果。工作器失败、吞掉额外请求的错误、重复调用、篡改结果或退出前未完成协议，均不得进入实际宿主。

Runner 返回后先释放唯一全局子进程槽，再进入连续 owner。实际 owner 不在该槽或外层生命周期锁内排队；嵌套媒体刷新仍需自己的真实进程槽，不使用绕过上限的豁免。各实际业务仍重新获取生命周期、下载和整理锁、核对当前配置与文件身份。

`plugin_deferred_operations` 是已有 `plugin_runs` 的私有加密效果收据，不是第二套任务队列。收据绑定原 RunID、实例、版本、generation、revision、action、完整输入摘要、固定方法/载荷和私有握手；阶段为 prepared / started / confirmed / uncertain。开始前在同一 SQLite 事务重新核验实际 running run 与当前安装/config。实际调用前持久写 started；结果未明时不根据计数或工作器回复假定成功。已确认结果独立保存，最终 run 结果写入失败可从收据修复。

重启时仅可重试证明尚未进入宿主的 prepared 中断工作器。started/uncertain 保留核对状态，且阻止相同配置通过新建 init RunID 逃逸。confirmed 收据可完成原执行，即使原插件已自卸载或目标后来重装；不得重新执行旧操作。既有卸载 SQL 收据仍负责实际提交后的自配置 revision 更新及自卸载边界。

已失败的 prepared 执行可通过受限重试接口使用原 RunID/Input，不能换成 reload 或一条新的任务。只有当前安装与配置完全相同、没有其它未完成执行，且 RemoveLink 旧 transition 没有 Active/Blocked/未知结果时允许重试。开始过、配置变化、generation变化或无法证明时明确拒绝。

App 全包 normal 通过（85.267s）；RemoveLink / Deferred / Uninstall 合并目标 race 通过（126.917s），ExpandRoots / AfterAction 相关宿主 race 通过（4.101s）；App、宿主与 builtin 的 vet 退出0。正式前端 build/typecheck、137项测试及格式检查通过。最后新增的恢复目标 normal 合并通过（15.388s），不将此前失败轮次计为成功。

正式 `CGO_ENABLED=0`、modernconly App 在 NAS 61 个顶层目标全部通过，0跳过/失败、退出0、未OOM；宿主全包 Linux 44个顶层目标同样通过。完整 SHA、隔离及清理证据见 [App报告](plugin-remove-link-owner-nas-verification.json) 与 [宿主报告](plugin-remove-link-owner-host-nas-verification.json)。容器/远端独占目录均已删除，light-media、moviepilot、qb 的状态/启动时间/重启次数未变。

恢复仅从不可变 sealed lifecycle/event 完成收据及核心文件索引收据认定；后续正常 shutdown 可以覆盖 current Transition，但不能抹去旧 RunID 的完整已确认收据。即使最后 run 已落 failed，也只有完整已确认结果或真实业务完成证明可以修复成功；prepared 与无证明的未知结果不自动重放。保存配置前的只读路径 preflight 失败会恢复旧监听，保留原 revision、配置及任务，允许直接纠正后再保存。

RemoveLink 的下载文件删除事件与通知目前仍只进入私有 pending outbox，尚无实际消费者；这不是已删种子或通知已送达的证明。23个插件的配置/KV/文件仍未正式迁入，连续 owner 接线不等于完整1:1迁移交付。
