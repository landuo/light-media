# S10 原源有限组件观测结果

**协调者实际执行 warm / cold 两个独立进程，均 exit 0，stderr 均为空。** 本报告只封存源组件观测，不授予迁移版等价、完整 S10、SDK/CLI/browser 或完整源宿主验收。

- 固定源：`8a83efeacd3e26b82be495e17d50146e31c96a60`；源方法范围仍为六个 Manager + 两个 Scheduler 旧方法。
- 新隔离依赖环境：SQLAlchemy 2.0.50 / SQLite、Pydantic 2.13.4、APScheduler 3.11.2。不是旧生产环境，也不是历史三查询采样环境。
- warm：2026-10-31 15:07:25–15:07:26 UTC；协调者报告 session 84498 / tool 00b06e。stdout SHA `c5a54c6d5f238b61d8016635581506281e0cbd82b860b7b833d379c7a6201d48`。
- cold：15:07:55–15:07:56 UTC；协调者报告 session 55866 / tool c7cf86。stdout SHA `62f700a28082b48675f5f4f831ba7daa827a6cb4d8e022bb9cb5e29719dba975`。
- 原命令、start/end、exit、stdout、stderr 分别保留为 `execution01.{warm,cold}.*`，未改写原始输出。driver `b407c020…` 与输入 manifest `5d3eb9a2…` 均未改动。

## 实测结果，而非 Go 反造预期

| 源方法/组件 | 本轮原程序实际观测 |
|---|---|
| `init_plugin` | missing 返回 None 且无 callback/cache 变化；enabled/disabled 为 init→get_state→事件开关，agent cache 清空、revision 7→8。init 抛 ValueError 后其余步骤未发生，原 cache/revision 保留。 |
| `remove_plugin` | running 顺序为 disable→close→stop_service；close 失败被原 helper 记录且不再 stop_service，仍继续移除缓存。class-only 项也被移除；Other 类保留，agent cache 清空/revision 增加。没有匹配真实 module entry，非空模块删除未实测。 |
| `get_plugin_state` | missing 为 False；`{"active":[1,"owned"]}` 原样返回，类型为 dict；callback ValueError 原样传播。不是强制 truthy/bool。 |
| `get_local_plugins` / 实际 Plugin model | model_config 实际为 `{}`，全部默认字段已原样输出。`state` 赋值后仍为同一普通 JSON dict；状态 callback 失败在 descriptor 中变为 False。空实现 get_page 为 has_page=False，返回空列表的已实现方法为 True。相同 plugin_order 保持 Empty→Owned；提高 owned auth-level 输入后 Restricted 按较小 order 排到前面。这里只是原权限阈值算法，非真实站点认证。 |
| `delete_plugin_config` / `delete_plugin_data` / 真实 SQLite | 无 class 且不 force 均 False，行不变；force 均 True，仅删除 Owned 数据，Other 与 unrelated 保留；已知 class 但无匹配行仍 True。真实 SQLite 删除触发器产生 IntegrityError：原配置 cache 已变 None，SQL 行仍保留。 |
| 独立 cold SQLite | 第二个进程使用只读 URL，读取的全表投影与 warm 终态逐值相同。原 SystemConfigOper 冷加载重新读到 `{"must_remain_in_db":true}`，而 warm 失败后的 cache 曾为 None。不是同一进程清缓存模拟冷启。 |
| `remove_plugin_job` / 实际停止态 APScheduler | inactive 为 None。显式 job_id=`cross` 即使调用 pid 为 Owned，也移除原 pid=Other 的服务及 cross\|a / cross\|b 两个 pending job；其他旧任务保留。最后 pid-wide removal 清空所属注册表与 pending jobs。 |
| `update_plugin_job` / 实际停止态 APScheduler | 空 pid 为 None。先删除旧任务，同前缀 alpha\|one 被 alpha\|two 替换；真正的 max_instances=0 验证失败后 Owned_broken 注册表项保留而无对应 pending job，原通知 sink 收到错误，later 服务继续成功加入。 |

原始 scheduler error log 含原 Python function repr 的进程地址；保留 raw，不将它伪造成稳定业务值。注册表投影只把 callback 身份记录为函数名，未删除影响状态/顺序的观察。

## 终态核回

- 619 个冻结输入当前逐字节 SHA 核回；此前 preparation/collector 冻结文档也全部核回，未被本报告重写。
- 两输出均记录 86 个选定原 AST 节点及行字节/AST SHA；warm/cold 节点列表相同。**86 包含辅助定义、字段和文档节点，不是 86 个新增方法。**
- 两输出各 385 项实际已载入模块文件 SHA 均对当前文件核回；不是仅 vendor 主文件摘要。
- warm 终态数据和 cold 读取结果相同；本地 owned SQLite 20,480 bytes，SHA `949450cc2e9cd4196515b79f95567c98bce6bf16549bf6617b88a3a276e4052c`。保留原文件，**不打包/不外传 DB**。warm receipt SHA `7dbb0e76eb67886cdb30151cd75bd7b4f779d49e3a0853e36059c3b1f879775c`。
- 两次观测前后均只有 MainThread；APS 状态实际为 0、thread_created=False，没有调用 scheduler.start 或 timer firing。不是调度执行/并发证明。

## 准确限制

真实执行的是选定不变原方法体 + 真实 SQLite/原 ORM与 Oper、原 Plugin/Pydantic、实际未启动 APScheduler pending jobs；构造/事件/消息/业务回调/数值 auth-level 为显式 owned 输入。没有完整原 Manager/Scheduler 构造启动、真实插件、真实 SitesHelper/RSA、非空模块驱逐、timer firing 或完整源 HTTP。

两次输出均实测 `socket_module_loaded=true`、`requests_urllib3_absent=true`。前者来自正常 stdlib/SQLAlchemy asyncio 依赖路线，**不能据此推断 bind/connect 已发生或被拒绝**；本采集没有系统调用级追踪，不把未观测的内部副作用写成已发生/未发生。已审代码没有发起 socket/HTTP/IPv6 probe 的调用，也没有执行新的 requests/urllib3 import、假模块或跳过探针 hook。原受限探针/新 requests import 暂停约束仍有效。

本轮与已交付三查询方法交集仍为零；保留历史信用但不重算。观察可交给 S10 实现 owner 作独立原源基准，不能用迁移 Go 运行结果补写这些源输出。独立 reviewer 的接受结论另行保存，本 owner seal 仅绑定源观测与当前输入，不代替独审或产品验收。
