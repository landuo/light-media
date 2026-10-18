# 来源与许可状态

固定输入为已安装的 `CloudflareSpeedTest` 插件 1.5.1 的 `__init__.py`，作者字段为 thsrite，作者主页为 https://github.com/thsrite。原文件及 SHA-256 位于 `reference/original_plugin.py`、`reference/source-manifest.json`。该固定导出没有改写或脱敏，源清单记录 redactions=0；本包只读消费。

这个源快照未附许可证文件或文件头授权文本。目前没有核对过可适用的插件库许可证，不推测其许可条件。当前工作是本地私有移植，没有发布/再分发步骤；外部分发前必须核对实际来源许可并保留应有声明。原 CLI 的 URL 来自该文件，指向 XIU2/CloudflareSpeedTest；当前没有复制、分发或执行其实际 release binary，也未声称原 CLI 许可已经验证。

`reference/probe.py` 在离线隔离 CPython 中只执行该固定插件类及受控替身，不导入旧项目、现有配置或用户数据。辅助 ip.py、system.py、http.py 只读本地参考不是固定已安装运行时快照，不能据此声称整个 MoviePilot 工具层的网络/locale行为完全等价。实现使用 Go 标准库；无外部 Go dependency、Python runtime、CGO 或 Python bridge。Python 脚本仅为开发验证工具，产品只包含 Go 文件和 form.json。
