# MergeMessageNotify：Go 主流程与 SMTP 依赖

原版聚合消息通知 **1.20.1** 的主流程、17 个渠道的构造与分发、原始配置表单，以及 SMTP 依赖已迁入正式 Go 源码。代码位于 `internal/plugins/mergemessagenotify` 和子包 `smtp`。**尚未注册到插件目录、部署、导入用户配置或发送真实通知，不能作为整个插件已完成的证明。**

## 已实现与来源

原安装的 22 个源文件共 185,035 字节，主文件 SHA256 为 `d3df56cb08a826968c00376a93643090fe982eb6742862286bb53aacdb561d68`，执行 AST SHA256 为 `675e7db346c40cdca880039dd56cf8e0830431a409c4c61a761cf56b55de474a`。原文件、实际依赖源码及离线对照保留在 `testdata/plugins/merge-message-notify-1.20.1/input`；产品运行不依赖 Python。正式迁入清单 SHA256 为 `d33d9a3b5f271c011496b4fcb25a3a86ba29450d9e4290c4d5471f7ecb015c2b`，覆盖 32 个正式文件及 425 个输入材料。清单自身和验证它的测试不自我包含。

主流程保留渠道顺序、重复项、缺失渠道跳过、通知来源排除、首个成功即停和单渠道异常后继续执行。配置区分运行快照与持久快照，保留未知字段及 missing/None/False/0 的差异；一次性测试只接受可信保存来源，冷启动不重放。原版 Page/API/Command/Service 均返回 None，没有额外添加这些入口。

17 个渠道为 IYUU、Bark、Chanify、Ntfy、PushDeer、PushPlus、ServerChan、Gotify、PushMe、Pushover、DingTalk、Qiyeweixin、Feishu、OneBot-11、Apprise、SMTP 邮件及自定义 HTTP。保留原版 Gotify 参数遗漏、钉钉双重转义、Bark AES 模式及加密失败降级、部分 dict.update 后失败等可观察行为。来自 Python set 的收件人顺序采用首次出现顺序去重，不能宣称复现特定 Python 进程的随机集合顺序。

原表单由实际 get_form 生成，18 份结果共 655 个节点。随后已补齐这些原表单所需的页签、窗口、子弹窗、字面点号键与链接，并用实际 PluginConfigDialog 验证切换、子弹窗和一次保存，见 [表单交互记录](plugin-merge-message-form-ui.md)。插件尚未注册或迁移，不能据表单夹具通过宣称可安装使用。

SMTP 包使用可信宿主冻结的连接、账号、发件人及收件人集合；逐次发送校验请求与绑定一致。它实现原版 MIME 编码/头部/地址解析、AUTH CRAM-MD5→PLAIN→LOGIN、EHLO/HELO、SSL/STARTTLS、MAIL/RCPT/DATA/RSET/SIZE、点转义及 QUIT 的异常边界。取消会关闭本次连接，不自动重试不确定的投递。错误仅包含固定类别、阶段和数值状态，不含服务器正文、账号、消息或密码。Go 时区数据随二进制嵌入，不依赖容器的时区文件。

## 验证证据

| 对照 | 样本 | 实际证明 |
| --- | ---: | --- |
| 原版事件分发 | 192 | 调用顺序、重复项、状态及配置 |
| 原版具体渠道方法 | 4,720 | 返回/异常及 HTTP、Apprise、SMTP 参数 |
| 生命周期 | 8 | 配置读取/重置/表单/Stop 序列 |
| 一次性测试来源 | 102 | 原来源谓词及配置重置；此组渠道发送为受控返回 |
| SMTP finally 路径 | 18 | 连接、TLS、登录、发送、QUIT 的错误优先级 |
| 实际 Mako 探针 | 19 | **6 个简单表达式等价；13 个明确不支持** |
| 原 EmailChannel MIME | 364 | 356 条 LF 原始消息字节/SHA 一致，8 条原头部错误 |
| 实际 Header.encode | 216 | 编码/折行阶段及错误 |
| 严格地址/quoteaddr | 6,237 | 原解析与信封地址 |
| 实际 binascii/Base64 | 8,651 | 解码字节或原错误类别 |
| 实际回环 SMTP/TLS | 44 | 每条 SMTP 应用命令、DATA 字节及返回/异常 |

不能将 13 个拒绝的 Mako 探针计为等价通过。SMTP 的 44 组使用自建回环服务，真实 SDK 子进程也走实际邮件适配器；没有向用户服务器或真实收件人发送。TLS 加密记录受随机数和库影响，不宣称记录流字节一致。原 Header 的百万字符上限通过同算法的小界限阶段验证，没有执行百万字符原始测量。

本机两包共 **28 个顶层测试**：普通 core **1.570s**、SMTP **1.330s**；race **7.221s / 4.369s**，vet 与构建通过。见 [本机记录](plugin-merge-message-local-verification.json)。

NAS linux/amd64、CGO_ENABLED=0 同 **28 项全部通过，0 跳过、0 失败，exit 0，无 OOM**。独占容器使用 256 MiB、GOMEMLIMIT 64 MiB、网络 none、只读根、UID 1000:1001、drop ALL 和 no-new-privileges；独占容器及远端目录已清理，原 light-media/moviepilot/qb 状态前后不变。二进制 SHA 与远端一致，见 [NAS 记录](plugin-merge-message-core-nas-verification.json)。隔离测试通过不代表真实长期负载或账户投递已验证。

## 仍须完成的原范围

- **完整 Mako 1.4.1**：当前 RenderBasic 仅覆盖默认模板使用的简单替换和 if。调用/运算/过滤器/循环/defs/注释转义、Python 语句及对象表示、继承/include/namespace/cache 等仍待 Go 实现；未知语法明确失败，不发送猜测文本。
- **完整 Apprise 1.9.9**：当前保留提取 URL、添加顺序及 notify 契约；URL 解析、全部通知后端、附件和协议响应仍待实现，不能用一个通用 webhook 替代。
- **动态类型边界**：NaN/Infinity、大整数限制、任意对象 repr 及非表单类型的 Python 错误顺序尚未全量实现。SMTP 的类型化请求丢失部分原非字符串输入，不能据正常表单样本宣称全部类型等价。
- **宿主与平台接线**：实际 HTTP 能力授权、通知事件生产者、加密消息封套、持久效果收据/恢复、配置 CAS、owner 吊销、循环防护、注册及正式数据迁移尚未完成。原表单需要的控件已另批验证，但正式实例及配置生命周期仍需联调。
- **环境差异**：SMTP 主机仅接受 ASCII/数值地址和正整数端口；IDNA/服务名端口未实现。地址解析超过 400 层明确拒绝。默认 EHLO 依赖容器解析环境，时区数据不等于完整 pytz。原版 SMTP 默认不校验证书；Go 使用系统或可信宿主 CA 校验，不允许关闭校验，也不扩展飞牛的局部证书例外。

原插件 hotlcc / MoviePilot-Plugins-Third 的 GPL-3.0 及依赖许可证均保留。输入材料中的旧 README 描述冻结候选当时状态；本文件及正式验证清单描述迁入后的范围。
