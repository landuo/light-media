# 下载器 bytes 材料通道

`SharedQBittorrentCommands.AddTorrent` / `SharedTransmissionCommands.AddTorrent` 的公开参数和返回类型不变。对于真实协商 `material/v1` 的标准 worker，普通 `[]byte` 材料不再有额外 16 MiB 上限；这不是任意路径、文件对象或通用 RPC。URL/magnet 和既有控制命令沿原分支，Cookie 同步信用见独立 S03 Cookie 记录。

## 授权和传输

- 固定已启用下载器、当前设置快照、调用代际和精确 capability；SDK 不指定真实端点或凭据。
- 一次普通 `upload_material_begin` 返回当前调用私有的 ToHost/bytes grant；每页至多 256 KiB，按顺序 offset 传输，完整长度和 SHA-256 校验，finish 后一次 `upload_material_submit`。
- 成功 Add 为两次普通 callback。私有材料帧另行计量，不能把它们称作普通 append/read 回调；普通请求、普通消息/帧、callback 和 30 秒普通调用时间限制没有因此放开。
- Host 不信任 worker 的 finish 声明：独立一次 claim、长度/SHA 复核及终态 tombstone。提前或错误 claim 不可复活；失效调用、配置变更、取消和授权错误不变为源 false。

## 内存与失败

公开 API 仍是 `[]byte`，必要输入快照和最终材料仍 O(n)，不是 O(1) 或无限内存。qB multipart / TR base64 仅在私有 selected Add 路径流式编码，保留固定 ContentLength、无 GetBody 重放、原 TLS/代理/跨域及会话协商边界。

未协商的旧 peer 保持旧有界 upload_begin/append/submit 协议。协商后失败不得降级重试。submit 尝试前的 best-effort abort 只是回收；submit 尝试后无 abort/replay，已委派500等未知效果不能冒作回滚或普通可重试失败。

临时 spool 只归本次调用 owner，作用域结束回收它自己新建的随机文件；不清除 S02 原 hash 重建文件、备份或 S12 cache，不授予任意文件路径访问。

## 证据范围

原大材料来自固定源码采集的合法 16,777,378 字节 torrent，测试按原配方恢复并比较完整 bytes/SHA。真实安装 worker → 私有上传 → 原 native → 固定自有 qB/TR HTTP，以及同 worker 失败恢复、同库冷启分别留证。自有 TR 协议 `id=901` 是额外夹具字段，单独断言后才对照原 recording 回复的 hashString，不伪称源给出了该 id。

普通、逐进程 race 与双进程同时 race 必须分层记录；保留曾发生的 HTTP502/约30秒失败，不将其改写为 PASS 或虚构具体错误码。此文是接口说明，不替代最终独审/发布状态；实际门和旧失败见 migration/evidence/s03-material-v1。没有运行真实下载器、下载、NAS 或新增浏览器验收。
