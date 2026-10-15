# P0 双下载器真实集成验证

验证时间：2026-10-15 10:49（Asia/Shanghai）。测试位于 `internal/app/isolated_downloaders_test.go`；默认跳过，仅在明确设置专用隔离目录后执行。

本次在用户 NAS 上使用独立的 qBittorrent **5.1.2** 容器和 Transmission **4.1.3** 守护进程，验证通过。下载器使用已有镜像及二进制，但配置、RPC 端口、BT 端口、下载目录、种子任务、影视库和应用数据库均为本次新建资源。

## 实际验证与模拟边界

| 部分 | 证据 |
| --- | --- |
| qB WebAPI | 实际实例完成登录、Add、重复添加、List、Files、pause、verify、start、remove_keep_data；重复添加不迁移目录，不修改现有任务参数。 |
| Transmission JSON-RPC 2.0 | 实际 4.1.3 实例完成会话握手、Add、查重、List、Files、pause、verify、start、remove_keep_data。 |
| 真实网络下载 | qB 使用 FFmpeg 合成的 5 秒测试片作为 seed；Transmission 下载目录起初无完整文件，由应用真正提交任务后，通过明确指定的本机隔离 peer 下载。`downloaded_ever = 1,259,449`、`have_valid = 1,259,449` 字节，文件内容与合成源一致。 |
| qB 完成来源 | qB 的 1,259,449 字节来自预置文件及真实校验；不将其描述为 qB 发生了网络下载。 |
| 应用工作流 | 两个下载器分别通过应用的资源预览、确认、持久下载任务、100% 状态轮询、硬链接整理、资产登记、NFO 生成；每个实例只保留一个预期任务，源文件保留。 |
| 整理文件系统 | 下载目录和测试媒体库同处 `/vol2`，验证默认硬链接行为。跨盘复制由其他测试覆盖，本次不声称默认会静默回退复制。 |
| 站点与元数据 | 原生站点适配器处理本机 HTTP fixture 页面及合成种子；TMDB 使用测试数据库缓存身份。它们不等于五个真实站点或远端 TMDB 的联调证据。 |
| 飞牛影视 | 本项不调用飞牛 API；飞牛库归属和扫描由独立验证负责。 |

专用私有种子没有公网 tracker。临时内存 tracker 仅监听 Docker 宿主网桥，只识别合成种子的 infohash；实际数据传输使用明确添加的自有 peer。Transmission 部分 announce 请求采用 NAS 的 Tailscale 源地址，被测试 tracker 的严格白名单拒绝，因此不声称 tracker 联调完全通过；实际 peer 字节传输和文件校验均通过。没有下载外部影片。

异步校验完成后，测试另行确认 qB 处于 seeding，再连接 Transmission，避免把“文件 100%”误当成“已经启动上传”。

## 隔离与清理

- 工作目录：`/vol2/1000/docker/light-media-verify-479a89db-b14b-4a1c-b236-5ca58e1e3ac9`。
- qB 容器：`light-media-verify-qb-479a89db-b14b-4a1c-b236-5ca58e1e3ac9`；RPC 仅发布到 `127.0.0.1:43185`。
- Transmission：独立配置及 PID，RPC `127.0.0.1:43191`，peer 监听 Docker 宿主网桥 `172.17.0.1:43192`；关闭 DHT、LPD、PEX 和端口映射。
- 使用 `remove_keep_data` 验证删除任务登记后源文件仍存在；没有暴露或执行删除下载数据操作。
- `EXIT` 清理仅针对精确命名的自有容器、命令行匹配此 UUID 配置目录的进程及此 UUID 目录。
- 完成后再次只读核对：UUID 目录不存在、专用容器不存在、43185/43186/43191/43192 无监听；原用户 qB 容器仍运行，原 Transmission 9091 仍监听。

全部 API 写操作仅使用上述专用实例。未读取或修改用户既有下载任务，未改变原下载器配置、现有文件或做种目录，也未占用正式应用的 3120 端口。

最终测试结果：`TestIsolatedDownloadersRealTransferAndImport` **PASS**，15.55 秒；qB 子项 3.86 秒，Transmission 子项 11.35 秒。原始去敏记录保留于本次工作目录的 `isolated-downloaders-verification.log`，不包含账号密码或会话凭据。
