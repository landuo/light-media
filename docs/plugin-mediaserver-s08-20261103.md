# S08：媒体服务器配置、实例查询与刷新

## 本项范围

本项实现固定 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 所界定的六个操作：`get_configs`、`get_services`、`get_service`、`is_inactive`、`refresh_library_by_items`、`refresh_root_library`。实现为原生 Go，不在运行时调用 Python。六个真实 factory 为 Emby、Jellyfin、Plex、TrimeMedia、Ugreen、ZSpace；没有以空 registry、配置 map 或固定布尔值代替服务实例。

| 操作 | 当前实现与必要语义 |
|---|---|
| `get_configs` | 读取显式提交的 MediaServers 原始配置；六字段及 presence/nullable 语义、非 ASCII 内容、None 与空名字的区别保持。对外 ordered entries 不把 None 键转换成空字符串。原列表行与按名字投影分离，不从已去重字典反造行。 |
| `get_services` | 从持久 Manager/Owner 的实际实例取得结果，按原过滤、顺序和同名规则处理；不是临时新建实例或返回配置对象。 |
| `get_service` | 使用原指定选择语义，保留所选实例的真实身份。worker 取得同 owner、同生命周期的私有引用；后续操作不按名字重新查找新对象。 |
| `is_inactive` | 由实际 vendor 状态及原身份决定，不写死成功；reload 不凭新配置代际撤销仍可用的 retained 对象。App 关闭、插件调用权限失效与实例自己的 close 状态分别处理。 |
| `refresh_library_by_items` | 五字段 typed carrier 保留真实 MediaType Enum、None 与 Path 语义。各 vendor 保留首匹配、fallback、失败前缀与原返回值。Jellyfin 原无该方法，不伪造成功。 |
| `refresh_root_library` | 调用真实 vendor/API；None、False、True 与 Plex 返回的实际 Library 对象不可互换。Library 以同 owner 的私有句柄保留真实身份，不泄露序列化内部对象。 |

### 普通宿主链

普通 `cmd/server` 构造真实六 factory，读取自有密封 MediaServers 配置，再执行 Manager discovery/load/reload/stop 和 Services 发布。管理员已认证的精确 MediaServers 写入口是新值 producer；不开放任意 key/raw 凭据 GET。原 async 提交、缓存与事件顺序依原实际基准实现；普通事件只发布安全字段，不广播凭据。未建立显式配置来源时等待配置，不能把空来源冒充就绪 registry。

worker 经平台原有 capability、认证、进程身份与生命周期校验进入 SDK/dispatcher。每次请求复核当前权限；构造/后台 context 来自可信 App 生命周期，不能由 worker JSON 伪造。物理路径操作使用既有 browserRoots/no-follow 边界；拒绝不会转换成全库刷新 fallback。共享 Requests 内容解压、JSON/text/apparent encoding 和 CPython string-set 顺序使用已绑定 Go 实现，而非 Chardet 近似或固定样本表。

插件私有 HTTPS 按用户已批准的统一要求不验证证书；不改变系统 CA、Git、浏览器、环境代理或凭据 origin 边界。无真实站点、下载器、NAS、旧用户凭据或旧数据库写入。本项不增加媒体库内容 UI，不自动导入旧 FNOS 凭据。

## 实际验收与可继承边界

发布以 `0a56e77e3a61b7f2dc14b5f4ce3f168a22d2a590` 为基线，精确文件及后继差分以独审 source whitelist 为准。已运行的 144-map App05 快照含 125 个产品/测试/资源路径；原执行信用不改称后继二进制已运行。 当前发布后继为 126 个路径：原 125 中仅替换 12 个 Plex/Trime 私有构造与原值消费文件，并新增 constructor_json.go；其余 113 个路径逐字继承。新增 2 个构造差分测试文件及 3 个原始观测文件仅作为公开证据，不混入产品白名单。17 个隐藏视图、2 个 HEAD 恢复视图均不发布。另附 14 项许可证/出处文件。CLI 专用 fixture worker 仅存证据，产品树采用已验 App fixture worker。

1. **原行为及组件。** 原 source collectors、真实原依赖、返回/状态/顺序与异常结果保留在公开证据恢复索引；Go 差分只继承其准确范围。固定官方依赖来源不冒充未知旧 installed package 的全字节身份；有限编码样本不宣称全编码域穷尽。
2. **五个 App 根，normal 与 dual-race 均通过。** `NativeInputsAndAuthority`、`NativeAssemblyProducerSDKAndCold`、`RefreshSDKConcreteResultsAndPaths`、`TrimeZSpaceHostSDKAndCold`、`UgreenAppSealedSessionRefreshAndCold`。跨各独立根合计 71 个自有 TLS 请求、8 个自有 artifact GET，不是一条共享会话。覆盖真实六 factory、管理员 producer、SQLite、SDK/dispatcher、guard、typed 返回、失败恢复及同库新 App/worker。UG 密封 session 的首次保存、热缓存/SQL ABORT、等值重试与冷恢复分别有实际断言。
3. **普通 server 两代 CLI。** 真正 `cmd/server` supervisor/服务进程自然退出后，以同数据库启动不同服务/worker PID；E/J 14 个自有 TLS 请求和 index/worker 各 1 GET，无冷启动 refetch。实际 15 个控制请求含 2 次初始空轮询及预期未认证 401，不声称固定 13 次。密封原值/来源摘要不变，环境不变，无 force-kill。其余四 vendor 的宿主 IO 由 App 根验证，不拼接成 CLI 六者均联网。
4. **构造参数后继。** 在原早退顺序缺口被发现后，重新执行完整原 23 个构造案例、9 个直接消费者、10 个 Unicode 消费者和 6 个 SecretsFilter 观察，均保留完整原输出。新产品的单根 normal/race 各通过 40 个子例：23 构造、6 secret helper、10 Unicode 和 1 个 native 组合；原 9 个消费者在其中分层对照。真实 TrimeNativeAPI 每模式只有 1 个自有 TLS login POST，保留 raw username/password；真实 PlexSDKFactory 按 add_secret、URL、header 的消费顺序保留原异常与独立 Session。Secret6 不宣称目标执行了 Python set 插入/计数；一般 JSON 状态对照不冒 Unicode 全域等价。
5. **受影响宿主后继。** 只重新执行 `RefreshSDKConcreteResultsAndPaths` 和 `TrimeZSpaceHostSDKAndCold` 两根，normal 与 App/worker dual-race 均通过。每模式两个独立根分别为 13 与 28 个自有 HTTPS 请求、各 2 个 artifact GET；不是一条共享会话。其余三个旧 App 根、共享生命周期及 E/J 普通两代 CLI 在已核未变切点继承，不声称新构造候选执行了一个新的普通 server CLI。
6. **静态门。** 原 App05 快照的三包 `mediaservervendors`、`mediaserverruntime`、`plugins` 的有限 vet 通过；这不是全 App vet 或全项目 CI。

App05 独审 seal：`7d57e2ea41c8e46b5b8b9c82b3420ad7c1d28438cf3cbc0aa19570d794f6cf0c`。

CLI02 独审 seal：`4fe109fd9c556856901aecfb6ef53bf891e84eb6b57dee57bbf728e0b628759e`。

构造 leaf05 独审：`cd6603570c3a3617807de06cc82f8ebb8b44447b00d9883775f78a5c674afffd`；两根 App06 独审：`146cd22e2d4655643d0c9b09156417fb8a4ba16e7ee05cd9b7e13b72ed2767a5`。当前整项组合独审：`95561c9177406d975f7a764b29436fa6ad7d833124efc3b0758e295826531842`。

Plex 历史实际安装的 SDK 精确版本仍为 **UNKNOWN**。已保留的官方 PlexAPI 4.18.1 是固定兼容参考，不伪称旧环境锁定包；现有六操作的构造、对象、会话、默认 profile、刷新及必要原值消费按各自原基准与实际目标门组合接受，不外推整个 SDK 或未知旧版本的全部行为。

### 原失败与未宣称事项

App04 在 go-list 阶段因四项精确离线依赖缺失失败，没有运行五个业务根；恢复原 go.sum 对应依赖后 App05 是新执行。早期 vet 的纯 overlay 目录 chdir 失败、原 collector/fixture 错误及对应窄修均保留，不改成绿。恢复缺失的旧 09:21–09:29 日志仍标缺失，不把后继执行称作找回旧日志。

旧 144-map 构造负对照真实编译后在 P01 失败，证明原 eager parser 缺陷。新 normal04 的 native-consumers 因测试 nonce=1 不符合原 `randint(100000,999999)` 而失败；该原失败保留，产品未为夹具放宽，唯一测试修正为 nonce=123456 后 normal05/race05 重新通过。旧失败不改绿，原 source 不重采。

App 预期 `MEDIA_SERVER_CONFIG_NOT_ESTABLISHED` 启动失败记录、注入 SQL ABORT 和故意 guard 拒绝均按根内明确断言计证，不等于整个门失败。普通 CLI 没有完整 Settings snapshot diff 主张。自有合成 HTTP/TLS 不冒充真实厂商服务联调、全协议/性能矩阵、浏览器验收或全项目完成。

本说明和仓内证据是本次整项采用候选；实际提交与远端交付以协调者的 exact-tree/commit/ref 读回收据为准，不以本文件存在即宣称已推送。
