# v0.3.16 添加下载器修复

问题：在局域网 HTTP 地址进入「设置 → 系统 → 下载器」，点击加号后选择 qBittorrent 或 Transmission，菜单关闭，但没有新增卡片。

原因：前端直接调用仅在安全上下文中提供的 `crypto.randomUUID()`。局域网普通 HTTP 缺少该方法；本机 localhost 属于可信来源，因此原先的 localhost 验证没有覆盖这个问题。

修复：改用在普通 HTTP 中也可用的 `crypto.getRandomValues()` 生成不重复的内部标识。后端仅要求下载器 ID 非空且唯一，不要求 UUID 格式。保留原版操作：选择类型新增停用卡片，点击卡片填写连接参数并启用，最后整体保存下载器分区。

验证使用独立合成实例，没有真实凭据或站点、下载器出站请求。在 Mac 的局域网 HTTP 地址复现旧版失败；修复构建后，分别新增两种下载器，确认两张独立停用卡片可见，点击新卡片能打开配置弹窗。未向生产实例添加测试下载器或改写用户配置。

自动回归模拟只有 `getRandomValues`、没有 `randomUUID` 的浏览器环境，覆盖两种类型的新增、名称与 ID 唯一、配置编辑，以及分区保存保留现有下载器和其他设置。

旧版源码仅在测试内存中编译，确认会触发 `crypto.randomUUID is not a function`，修复后通过。全部 99 项前端测试、类型检查、生产构建、Prettier、差异检查及 Linux amd64 纯 Go 编译通过；18 个已知凭据值的扫描无匹配。

已部署 v0.3.16。Linux 二进制 SHA-256：`fa729421b7e3fa0c2044dee182f205af4e6df428f994e3eb5e8e26ac22d1a27a`。发布资源：`index-CwZC3g3O.js`、`index-Bg1LL4k0.css`。

内外网鉴权状态均返回 v0.3.16，两端 HTML/JS/CSS 与本地构建逐字节哈希一致；容器 running、healthy、重启 0。发布前后 active writes/jobs 均为 0，资产 11、整理计划 19、下载意图 15、订阅 1、原有失败任务 3 的计数一致。保留上一版二进制与 compose，没有新建数据库备份。浏览器整页刷新后加载新版。
