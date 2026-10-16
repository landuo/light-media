# RemoveLink 原事件与站内通知转交

本轮在 Go 正式代码中接通 RemoveLink 已提交 outbox 的两个本地效果：发布 `downloadfile.deleted` 广播事实、写入站内通知历史。原配置/KV/文件尚未导入，尚未部署；这不增加正式迁移插件数量。外部通知渠道与完整消息路由仍未接入。

实际安装版 `DownloadChain.download_file_deleted`（SHA `ecfaaa2b2777809924aeb32632afa53d8eb701c7ee76e333099d6847988fc003`，1809行）只读取 `hash`。RemoveLink 2.17 发出的是 `{src}`，因此核心在查询下载器之前就返回：不会删除任务、数据、下载历史，也不会发出 `DownloadDeleted`。此前“另一个下载器助手负责删除”的文字缺少本安装范围的证据，现已纠正。磁盘上另有 MediaSyncDel 的路径消费者，但不在已确认的23个已安装ID中；其行为可能删数据，不能默认合入 RemoveLink。

原 EventManager 的广播调用将事件放入优先队列后返回，接收器在后台执行。因此本轮的 `published` 仅证明持久事件流已接收，并不证明消费者已完成，更不证明已删种。公开 DomainEvent 只增加严格限定的 `downloadfile.deleted` / `file_event_id` 引用：不含路径、种子hash或推断出的下载/影视身份。路径与原生产者、代次、配置revision、effect身份保存在加密私有表；引用本身不授予文件或下载器写权限。现有事件游标、去重、批次和插件执行队列复用该引用，没有建立第二套任务队列。

通知沿实际 `post_message` 的先后关系处理：原消息默认 `save_history=true`，且保存历史在收件路由、NoticeMessage与外发之前。RemoveLink的类型是 `SiteMessage`，值为“站点”，不是“插件”；source/channel/userid/username未指定。当前历史将原title/text映射为现有通知title/message，保留站点类型和原插件页面的相对link。站内显示按当前配置脱敏，但不使用日志2048字符截断；完整原文仍保留在密文outbox中。此效果记为 `history_saved`，NoticeMessage路由及外发继续待接。原消息队列在允许时段可能同步调用发送器，不能笼统声称原版外发全是异步。

转交只认证已提交的历史意图，不重新取得删除权限。它核对原sealed outbox、完整effect身份、生产者实例/代次/token及精确core receipt；缺失、篡改、未知状态都失败。已停用、卸载或后来重装的生产者仍可转交旧的合法意图，不能借用新代次的权限。每次转交在同一 SQLite 事务中写私有事实、公共事件或本地历史、加密handoff收据和原outbox阶段；任何一步失败都整体回滚。通知确定ID已有不同内容时拒绝覆盖；匹配内容保留已读状态、首次时间与原记录。

转交按原outbox的SQLite rowid排列，避免相同时刻、时钟回退及HMAC排序改变源调用顺序。它不承诺原广播处理器按此顺序完成。现有插件事件工作器每轮最多转交200项，无网络/文件/下载器操作。转交错误仍允许其他已提交事件批次排空。

索引中handoff行的存在不被当作可信回执。每轮还验证最多200项已有handoff，逐项认证原意图、收据、stage/reference及已发布私有/公开事实；站内记录以去掉Read后的HMAC校验，正常标记已读不失效。审计使用固定high-water完成一轮后回到开头，新追加不能使旧记录永久失去复核。审计失败不推进游标、阻止这条outbox继续转交，并保留错误；不会重新发布一条缺失收据的旧事件。此机制是宿主增加的持久可靠性约束，不是原广播提供了exactly-once保证。

来源与离线参考在 `testdata/plugins/remove-link-outbox/`：实际安装源码AST快照、SHA固定的生成器和17项原函数场景（8下载、7通知、1默认值、1广播入队）。原函数使用记录器，未导入原应用/用户配置，未调用真实下载器或通知。带hash的原下载分支和完整消息路由只是参考，不计为本轮实现。Go测试另覆盖真实SDK→原生owner→自有硬链接删除→outbox→既有事件流/站内消息、事务故障、重复/并发、退休后重启、损坏回执、消息冲突及审计回绕。

冻结后的 App 全包 normal 通过（86.903s）；RemoveLink / DomainEvent / PluginEvents / Deferred / Uninstall 目标 race 通过（163.251s）；App 的 vet 退出0。新切片16个顶层测试均包含在联合回归中。NAS上的 CGO_ENABLED=0 / modernconly 二进制92个顶层目标全通过，0跳过/失败、退出0、未OOM；完整SHA和隔离清理证据见[报告](plugin-remove-link-outbox-nas-verification.json)。测试容器及远端独占目录均已删除，light-media、moviepilot、qb的状态/启动时间/重启次数不变。此前两个仅测试代码的编译错误（下载器嵌入字段、audit cursor字段更新）已修正，不计为通过轮次；冻结后无源码变更。运行服务、旧MoviePilot、现有媒体与做种任务不属于这些测试的操作范围。
