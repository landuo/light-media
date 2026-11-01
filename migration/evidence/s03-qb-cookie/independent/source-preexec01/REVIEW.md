# S03 qB URL + truthy Cookie 原 collector 独立准入

**READY：准入 COMMAND.txt 中固定 interpreter 的一次 `-I -S -B` 有限原采集。** 无本次范围内实质阻断。此处没有执行 collector/packaging/source/Go/network，没有形成原行为 oracle，更未验收目标或真实 daemon。

## 固定源与节点

`input-bindings.json` SHA256 `c71ac0091a841618ce3cc4fb4d0a8467874b6da568d0f04cb1e5decfefa6761e`、collector `dbdc91b4e271ab01b83d1e19fb8f590a0349ca8adb2ad117fe9c100d89a0beb1`、作者 seal `ac96fe41cec1e4175efbde51935db39772877f93564741c88339c1a2a8356844` 均准确。119 个独立绑定当前 SHA/大小一致，drift `[]`。

两原源文件当前完整 bytes 与 Git commit `8a83efeacd3e26b82be495e17d50146e31c96a60` 相同。9 个完整方法的原 segment、AST、含 decorator 的完整文本逐项独核，并与独立 scope 的同 9 节点吻合。Qbittorrent / StringUtils 原类名、方法 body、staticmethod decorator 和 private name mangling 保持；不执行原类的 constructor、类级其它赋值、模块 imports、settings/login/vendor。

collector 构造完整选中 ClassDef，而非把关键 helper 换为桩；仅既有有限 harness 的 future annotations 延迟类型注解求值。Qb 类先定义、StringUtils 后定义，但 method 调用发生在二者完成后，共享同一 globals namespace，不会改变实际 `StringUtils.is_magnet_link` 选择。对象只用 `object.__new__`，注入该分支实际读取的 recording qbc 与三个明确 owned instance 字段。

旧 S03 source34 的 collector 仍明确 `cookie is None`，并以 BaseException sentinel 阻断 is_magnet_link/sync；不改或重跑旧证据，不能把旧信用升级为本次 Cookie 分支。

## 实际纯依赖与启动边界

- 固定 `source-runtime/s10-venv/bin/python3.12` resolve 到已绑定 30894944 字节 Python executable。命令精确含 `-I -S -B`，collector 同时断言 isolated/no_site/dont_write_bytecode 和无额外 argv；没有 site/.pth/site-packages 全路径注入或环境/proxy 清除。
- 53 个 stdlib source 与 40 个可读取 pycache 各自散列均已绑定。`-B` 不等于禁读已有 stdlib pycache：此处如实核了缓存 bytes，不虚称只从源码启动。stdlib-import preparation 不是原程序执行信用。
- packaging 26.3 的完整 `__init__.py`/`version.py` 当前分别为 `6e758cdd…` / `558b2fb5…`，直接与已交 S05 原 raw 的依赖散列记录吻合，不只是引用作者声明；没有新下载。两 source 仅在审后通过固定路径 spec + compile/exec 加入真实 packaging 名称，未使用 loader 执行/读写其 bytecode cache。
- 本地静读 Version 的实际 runtime imports 为预载纯 stdlib re/sys/typing/functools/warnings；TYPE_CHECKING 分支不入 typing_extensions。`_structures` 仅旧 pickle 注释/状态兼容背景，不是实际 import，无 unpickle。NamedTuple/typing 的纯代码生成属于已绑定 stdlib 的必要行为，不伪称 compile/exec 全禁。
- SimpleCookie 是实际 `http.cookies.SimpleCookie`；URL hostname 来自真实 `urllib.parse.urlparse`。两模块的 import 链仅纯 stdlib，`ipaddress` 是纯解析，不是 socket/IPv6 探针。无 requests/urllib3/http.client/urllib.request/socket/ssl、完整 app、qB/TR vendor 导入。

## Audit 的真实强度与不可吞边界

audit 在所有预载、已审 stdlib imports 和初次输入校验之后、在任何 packaging 或选中原节点执行之前安装。它显式拒绝网络/process/native-load/write 类事件和禁用模块导入；发生拒绝先写 stderr/保留内存 rejection，再抛 **CollectorBoundary(BaseException)**。选中原方法和 run_case 仅捕 Exception，不能把此边界吞成原 False/None；recording 缺方法、超预算、域错误亦走同边界。代码没有卸载 hook、吞掉其拒绝、修改环境或用外部执行路径继续。

这不是任意代码沙箱，也不是 compile/exec 身份白名单或任意只读路径白名单。准入依据是固定 hash 的 collector/两个 pure dependency 文件/完整原 AST 和普通 JSON 输入构成的受限闭包；源码中无任意执行、URL fetch、凭据读取或源码输出转 eval 的路径。后续 actual final 仍须核 before/after module origins、119 输入、audit rejection/退出码；非空拒绝不能擦掉后再记通过。

## 17 案 / 18 完整 add 的观察独立性

所有输入由普通 JSON 组成，只有 synthetic `.invalid` URL/Cookie、一个 relative URL 和一个 synthetic magnet。协议控制是原入口外部回复或 owned RuntimeError，不是 expected 原结果。版本阈值/低于阈值/rc/local/坏值/None/查询错误、顺序合并/缺值/空 store、Cookie 无 morsel/非法 name、无 hostname、read/set/add 错误与 magnet 分支按自然节点采集。非法 Cookie 的实际异常/返回不在 collector 里预判。

四个 recording 端口是 app_web_api_version / app_cookies / app_set_cookies / torrents_add。每次实际入端口先记录 call/kwargs，然后真实抛指定普通异常或记录 return；成功 setter deep-copy 更新 owned store 并记 mutation。setter 注入错误是明确 **pre-effect** 模型，不作为 daemon error atomicity 证据。普通原 sync catch 后是否 fallback、原 Cookie 是否改为 None、后续 add 异常后的 store 状态、原 tuple 值/type/truthiness均取实际原执行，不由结果表拼装。

最后一案两个完整 add 复用同一个原实例和 RecordingQbc/store；只重置单次 trace/count，保留 store。每次最多四个外层 protocol calls（version/read/set/add）限制与选中闭包相符；vendor 自有版本 gate/HTTP 未执行，不能将这些 calls 称为物理 HTTP 次数。17 案 shape/18 calls 不扩大为任意 Python 对象、全 SimpleCookie/PEP440 域或全 AddTorrent 支持。

## 执行决定

只由协调者执行固定一次原采集，stdout/stderr/命令/工具真实终态分别保留，不覆盖旧失败或已有 oracle。不重采旧 source34/S05，不运行 vendor/daemon/Go/network。若输入漂移、audit 拒绝或 runtime 失败，保留原终态并先报精确问题，不静默替换依赖/放宽 hook/重试。后续只有独立实际读回通过后，这份 raw 才可成为目标差分基准。

审查者仅写 `audits/s03-qb-cookie-independent/source-preexec01`；ACTIVE、ledger、collector 与旧证据均未改。
