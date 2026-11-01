# 本机备份材料 SDK

```go
result, err := (plugins.LocalTorrentBackup{Host: host}).Prepare(ctx,
    plugins.LocalTorrentBackupRequest{DownloaderID: selectedID, Directory: authorizedRoot, Hash: exactHash})
```

请求必须使用实际当前调用的 ctx/HostCaller。目录仍由宿主按当前 AllowedRoots 与 library roots 并集、私有/no-follow/普通文件规则验证；下载器类型取当前选中配置。它不是任意 ReadFile、Downloader Add 或删除接口。

- 新宿主和新 SDK 在真实已安装 worker 会话协商 material/v1 后，通过 prepare_material 获得同 invocation 的私有材料 grant；有限页传输、完整长度/SHA检查和 finish 后才返回完整 Content。此 S02 分支已去除额外材料总长 16 MiB 限制，普通控制 JSON、帧、wire、callback 和时间预算不变。
- 公共 Content 仍是 []byte，最终 O(n) 物化；不承诺无限资源或 O(1) 内存。成功空材料为非 nil 空 slice；Skipped=true 时 Content=nil 且没有 grant。
- 旧未协商 peer 仍使用原有限 prepare/read/release；新通道失败不回退重做源 IO。私有材料帧不能计作普通 HostCaller read/release。
- finish 消费流但不新增公开 release RPC。实际 invocation 终态撤销并关闭 owner，只删除其精确自建 spool；同调用多次 Prepare 的 spool 最迟保留到该调用结束。不会清零已返回 slice。
- qB 原 tracker 重建仍先写、Sync、close、parentSync，再最终读确定 hash.torrent。失败可能残留源重建文件，不能承诺无副作用。spool 与该保留文件以及 S12 持久 cache 完全分离。

本更新仅完成 S02 的原有限父合同。S03/S12 其它消费者或任意 file/path 输入没有由此获得新等价结论。实际源/普通CLI/测试分层与复跑指令见 [S02材料证据](../migration/evidence/s02-material-v1/README.md)。
