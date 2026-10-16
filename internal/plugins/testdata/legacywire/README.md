# 固定旧协议参考

这里的四个 Go 文件固定自 `ab41d26` 的 `internal/plugins/{api,jsonrpc,protocol,worker}.go`，仅将包名从 `plugins` 改为 `legacy`，用于新 Runner/旧 SDK 和旧 Runner/新 SDK 的真实子进程兼容测试。复制时已逐文件与该提交核对，除包名外字节一致。

这个包不依赖新协议编解码器，不能随新实现同步修改。它位于 testdata 中，仅被测试导入，生产程序不引用它。参考不包含用户配置或凭据。
