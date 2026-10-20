# 原版请求与调度配置迁移

2026-10-20：全 Go 专用导入工具已随 v0.4.2 部署，4 条来源记录已原子加密导入正式新服务，重复导入零写入；来源正文仅在内存和 stdin 传递。实际完成并迁移的插件为 3/23，配置迁移不额外增加数量。实际记录见 [共享识别词迁移收据](plugin-remote-identifiers-deployment.json)。

工具只接受已核对的 MoviePilot v2.15.6 安装源码投影，校验四份应用源码和三份 tzlocal 源码指纹。来源采集在原容器执行独立只读脚本：抽取七个设置字段和必要属性，使用选定的 BaseSettings 模型计算，不导入 MoviePilot，不调用完整 Settings 初始化或环境文件写入方法。Python 仅用于原系统的离线对照与来源采集，新程序和导入工具均不依赖 Python。

来源数据只经过内存和 stdin。两次实际采集结果相同，均通过 Go 输入校验；未把凭据、代理、派生 UID 或设置正文保存到普通文件、报告或命令行参数。39 个原字段参考案例和两个新增时区案例覆盖配置优先级、类型转换、空值、大小写、代理、UA 和时区；保留两项原版 ValidationError。

一次事务写入以下四个密文记录，均以 `(kind, system, main)` 绑定 AES-GCM：

| kind | 内容 |
| --- | --- |
| source_statistics_settings | 原统计开关、服务地址、派生 UID、UA、代理和显式代理环境快照 |
| source_network_settings | 原 GitHub 请求头、代理和显式代理环境快照 |
| source_runtime_settings | 分别保留设置中的 Timezone 与本地时区 LocalTimezone |
| source_settings_migration | 来源、摘要、导入时间及核对依据；不向普通 API 返回原值 |

空请求头、`Proxy: null` 和显式空环境数组都表示已取得的原值，不能与尚未采集混为一谈。原 UID 由当前原容器根文件系统按已安装算法推导；LocalTimezone 来自已安装 tzlocal 5.4.4。两者均为冷推导，未声称查询过原服务内存中的缓存。配置 TZ 与本地时区独立，不能互相替代；无效或空的配置 TZ 不会被擅自修正。

`cmd/source-settings-migrate-owned` 没有公共 HTTP 入口。`--validate-input` 只校验私有 stdin，不打开目标。指定现有目标目录时，默认持有实例锁执行只读预检；存在未完成的 WAL 或 journal 时拒绝预检，避免忽略已提交数据。预检已核对数据库、密钥和锁文件的字节、大小、时间与权限均未改变。

`--apply` 仅向停止且无活动工作的目标写入四条记录，不创建任务、事件或计划，也不修改密钥或原系统。完全相同的导入冷启动后再次提交为零写入；部分记录、来源冲突、密文篡改或正在运行的目标均拒绝覆盖。事务失败不留下部分设置；提交结果不明时报告未确认，后续以持久收据核对。

三项应用测试共 37 个含子用例在普通和竞态模式均通过，vet、原生工具、Linux amd64 CGO=0 工具及服务构建、依赖校验通过。3362 个源码包输入和 1442 个本机编译输入均核对无漂移。此前 SQLite 只读模式仍生成 WAL 附属文件、隔离脚本缺少类型名和测试标签冲突的失败证据均保留；没有以通过结果覆盖失败记录。完整应用回归未在这一独立增量上重跑。

对应实现：[导入器](../internal/app/source_settings_migration.go)、[验证](../internal/app/source_settings_migration_test.go)、[专用命令](../cmd/source-settings-migrate-owned/main.go)。统计与网络消费者仍需分别完成业务接线和实际迁移，不能因为记录结构已实现就增加已完成插件数量。
