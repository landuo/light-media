# CategoryEditor 1.3：独立原源码发现审查

## 0. 审查身份、范围与证据边界

- 审查任务：`/root/migrate_installed_plugins/category_source_audit`；独立于实现者，未接收实现者功能清单或验收结论。只发现范围、资料缺口与建议场景，**不作验收通过或完整等价判断**。
- 仅访问当前云端 Linux 的本任务目录及指定技能文件。未访问用户 Mac、旧候选或旧 dev 分支，未启动 MoviePilot 或光影服务，未执行 onlyonce，未触媒体、种子、下载器或外部通知；未修改产品代码或权威账本。独立发现后，按追加任务仅在本任务证据目录真实回放固定插件与归档 helper，见第 10 节。
- 已完整读取指定技能 `SKILL.md`、`references/ledger-and-evidence.md` 和 256 行 `scripts/check_ledger.py`。检查器是结构门禁，不是本报告中的行为证据；本次没有替协调者运行或维护账本。
- 固定原源码：`source-snapshot/installed-plugin-sources/categoryeditor/__init__.py`，126 行、4291 字节，SHA-256 `24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27`，已实际计算一致。
- 固定清单：`source-snapshot/installed-plugins.json`，SHA-256 `0514b47a0aedbf82e4753d9797c9f180bc5868652f7ab0e1ca1acfd05a3ccc71`；记录 CategoryEditor 1.3 只有上述一个插件源码文件。清单观察时间为 `2026-10-28T17:34:14.563030+00:00`，全安装清单为 23 个实例；本报告只处理 CategoryEditor。
- `source-snapshot/README.txt`（SHA-256 `09e1024c3f0150fe46a61c5fa420ae17f78e048bfc35ac5269c0900c23c141fe`）明确来自现运行容器的只读收集，未包含运行配置、数据库或凭据。因此不得假称已检查用户当前策略、当前配置值或实际运行缓存。
- 原宿主源码、内置 `category.yaml`、原 Python/依赖版本均不在本次 NAS 源快照。后续从已交付 main 定位到有日期/哈希的归档原宿主证据，补充见第 9 节；归档源码不自动等于现用 NAS 同版本。下文严格区分“插件源码直接确定”和“宿主资料补齐后才能确定”。不以上游最新版或已有目标代码充当原行为证据。
- 目标身份现场：任务单指定 `main` 基线 `10d76660fb78595948d89dccaa034c87a2240206`；本审查早期两次只读观察中，`git -C light-media branch --show-current` 为 `main`，`rev-parse --verify HEAD` 报 `fatal: Needed a single revision`，树文件显示 untracked。已告知协调者，可能正处恢复过程；本报告不将该目录自行认证为指定提交，不作任何 Git 修复。目标只读 SDK 观察不影响独立源发现。
- 后续恢复确认：协调者完成恢复后，本审查实际读到 HEAD=`10d76660fb78595948d89dccaa034c87a2240206`，当时 `git status --short` 为空；上述“无 HEAD”是恢复中的历史观察，不再是当前 blocker。之后其他实现者的在途修改不属于本审查的改动。

> 定位约定：以下 `P:Lx–Ly` 均指固定 CategoryEditor 原文件，不指目标实现；描述 Python 标准方法行为时另标明依赖运行时，实际异常文本/文件细节仍需固定原解释器观测。

## 1. 全文件归属与入口清单（插件自身可确定）

| 入口 / 构件 | 原定位 | 直接可确定的行为 |
|---|---|---|
| 模块装配 | P:L1–5 | 导入 `settings`、`CategoryHelper`、`_PluginBase`；其导入副作用不在该文件中。类型注解不会自动校验配置值。 |
| 类元数据 | P:L8–26 | 名称“二级分类策略”，描述针对下载和整理自动二级分类，图标 `Bookstack_A.png`，版本 `1.3`，作者 `jxxghp`，主页 `https://github.com/jxxghp`，前缀 `categoryeditor_`，加载顺序 `5`，`auth_level=1`。官方等级认证为用户已排除范围，但不应丢失其源事实。 |
| 类级默认成员 | P:L28–33 | `_enabled=False`、`_content=""`；`user_yaml=settings.CONFIG_PATH / "category.yaml"`，`default_yaml=settings.INNER_CONFIG_PATH / "category.yaml"`。路径在类定义/模块导入时计算，不从每次配置输入计算，也不含插件实例 ID。 |
| `init_plugin(config=None)` | P:L35–49 | 唯一插件定义的业务写入口，详细状态机见第 2 节。普通配置初始化本身可触发写入，不存在独立 `onlyonce` 标志。 |
| `get_state()` | P:L51–52 | 返回实际 `self._enabled`，**没有 `bool()` 转换**；`-> bool` 只是注解。 |
| `get_command()` | P:L54–56 | 静态方法，`pass`，返回 `None`，不是空数组；无插件命令声明。 |
| `get_api()` | P:L58–59 | `pass`，返回 `None`；无插件 API 声明。用户排除 API 扩展点不会删去此插件的业务写入口。 |
| `get_form()` | P:L61–117 | 返回表单结构与默认模型二元组；每次调用读取内置默认 YAML。详见第 3 节。配置表单与用户排除的独立插件页面扩展点不可混为一谈。 |
| `get_page()` | P:L119–120 | `pass`，返回 `None`；无插件页面。 |
| `stop_service()` | P:L122–126 | `pass`，返回 `None`；插件自身不改成员、不撤销文件、不恢复默认策略、不发送通知。 |
| 未定义入口 | 全文件 | 未定义构造器、事件处理器、定时器、`get_service`、任务执行方法、数据重置、reload/update/uninstall/clone 钩子、工作流/AI 方法。继承入口是否存在必须读 `_PluginBase`，不能据“此文件没有”推断宿主完全没有相关行为。 |

全文件仅一个行为文件，无插件自带依赖清单、默认策略文件、测试或额外资源。元数据引用的图标和内置 YAML 属于原宿主/资源缺口，不等于已被快照覆盖。

## 2. 初始化状态机、数据契约与副作用顺序

### 2.1 条件和默认值

原代码是 `if config:`，不是“若参数存在”；Python 假值配置（例如 `None`、`{}`；若宿主允许非对象，亦含 `False`、`0`、空字符串/列表）完全跳过该方法主体。**同一个已存在实例不会重置 `_enabled` 或 `_content`**。新实例的插件定义默认回退值为 `False`/空串；基类构造是否还设置状态须补源。

非空配置按顺序执行：

1. `self._enabled = config.get("enabled")`（P:L37）。缺键得到 `None`；显式 `null` 也是 `None`；原值可以是数字、字符串、容器，并不限定 JSON 布尔值。
2. `self._content = config.get("content") or ""`（P:L38）。缺失、`None`、空串和其他 Python 假值归一为空串；真值非字符串不会在这里转为字符串。即便 disabled，也会更新这两个内存成员。
3. 用 `_enabled` 的 Python 真值决定是否进入写入（P:L40）。例如字符串 `"false"` 是真值；字典缺 `enabled` 与显式 `false` 在 `get_state()` 可有 `None`/`False` 区别。

配置类型注解不会强制输入为字典：真值非映射在第一个 `.get` 处可能报错；具体 HTTP/宿主是否允许这些输入需补源。未知配置键在本文件没有读取；成功时传给 `update_config` 的新对象只含两个键，**最终存储是否全量替换/合并不能凭本文件断言**。

### 2.2 启用分支的严格顺序

| 次序 | 原定位 | 操作 | 重要边界 |
|---|---|---|---|
| A | P:L41 | `user_yaml.write_text(self._content, encoding="utf-8")` | 直接写宿主全局 `category.yaml`；没有 YAML 解析/校验、创建父目录、临时文件替换、备份、锁或事务代码。返回字符数被忽略。 |
| B | P:L43 | `CategoryHelper().init()` | 包含先调用构造/获取实例，再调用 `.init()`；不是只写文件。构造器是否初始化、单例/缓存行为、实际重载和错误处理均需原宿主确认。返回值不检查。 |
| C | P:L44 | `self.systemmessage.put(...)` | 精确正文：“二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！”；精确标题：“二级分类策略”。这是 `systemmessage` 调用，不能擅自等同于外部推送/下载通知。返回值不检查。 |
| D | P:L45 | `self._enabled = False` | 在消息调用返回之后、持久化调用之前关闭内存开关。 |
| E | P:L46–49 | `self.update_config({"enabled": False, "content": self._content})` | 调用不包含其它原配置键；调用返回 `False`/`None` 也未检测，只有抛出的异常会打断。是否持久化、缓存更新、引发宿主事件需原基类/存储链确认。 |

方法没有显式返回值，正常返回 `None`；未调用 `get_form`、未读取当前 user YAML、未直接触媒体路径/下载器/外部网站。成功后的关闭并不撤销已生效的分类策略，这是“写入一次后关闭”的插件实现，而不是持续启用守护进程。

### 2.3 失败、部分结果与恢复

本文件没有 `try/except/finally`、重试、补偿、回滚或持久执行收据。以下是**方法内部**的可推导结果，宿主如何上报/保存失败仍待补源：

| 失败阶段 | 已发生/可能保留的结果 | 尚未执行 |
|---|---|---|
| A 文件写入失败 | 配置已赋给成员；文件可能未改、已截断或部分写入，取决于具体失败点与原运行时。没有恢复旧文件。 | B/C/D/E 全不执行；enabled 尚未自动关闭。 |
| B 构造或 helper init 抛错 | 文件已写；helper 可能还存在部分副作用，待其源码判定；enabled 保持输入值。 | C/D/E 不执行。 |
| C systemmessage 抛错 | 文件及 helper 调用已完成；消息是否部分入队取决于其实现；enabled 保持输入值。 | D/E 不执行。 |
| E update_config 抛错 | 文件/helper/message 调用都已发生，内存 enabled 已为 `False`。 | 无本插件补偿；存储状态/缓存可能不同步，须观察原链。 |
| B/C/E 返回失败值但不抛错 | 本插件忽略返回值并继续后续步骤。 | 不能以“返回 False”自动转换为本插件异常。 |

重启是否重放取决于**实际保存的配置**与宿主初始化链：若持久层仍是 enabled 真值，重新初始化可能重复写入/通知；若已保存为 False，插件本身不会再写。不得增加“见到相同内容就不执行”的去重行为，因为插件源没有内容比较。停止/卸载不含本地恢复文件动作，但宿主自身行为还不能断言。

### 2.4 文件、并发与安全边界

- 原路径为类属性，不使用配置内容作路径；所有同设置路径的普通实例/分身共享 `category.yaml`，不是天然每实例独立策略文件。是否在 clone 时改写这段源码/设置仍要补宿主 clone 实现。
- 使用标准 `Path.write_text`：文本 UTF-8、非原子覆盖、没有本插件路径安全或符号链接检查；具体换行处理、权限、符号链接跟随、无效代理字符导致的截断/异常需要按实际 Python/OS 做隔离观察。不能拿目标安全写法自动当成原语义；若保留方式有安全取舍，应单列决策。
- 成员及共享文件未见插件级锁；一旦宿主允许并发，成员赋值、写入、重载、通知和配置保存可以交错。不能由本文件断言原宿主一定并发或一定串行。
- YAML 语法/标签/正则/目录匹配风险均在 helper/消费者链，本插件未做限制。不能在未补源前擅加合法 YAML 才允许写入、自动恢复默认、空内容拒绝等“修正”。

## 3. 配置表单与读取语义

`get_form()`（P:L65–117）结构完整归属：

- 外层 `VForm`，两行 `VRow`。
- 第一行 `VCol` 为 `cols:12, md:6`，内容为 `VSwitch`，`model:"enabled"`，label `启用插件（写入配置文件）`。
- 第二行 `VCol` 为 `cols:12`，`VAceEditor` 的属性为 `modelvalue:"content"`、`lang:"yaml"`、`theme:"monokai"`、`style:"height: 30rem"`。
- 返回默认模型 `{"enabled": False, "content": self.default_yaml.read_text(encoding="utf-8")}`。

这里每次读的是内置默认策略文件，不是已写用户策略文件、当前成员或配置 DB。文件缺失、不可读或 UTF-8 解码失败会传播异常；没有缓存或 fallback。`Path.read_text` 的通用换行转换需在源解释器下观测，不能假设读回字节与原文件完全一致。此方法没有写入或初始化 helper 的代码；宿主是否把默认模型与已保存值覆盖/合并、预览是否建实例、读取表单是否自动补存配置均是资料缺口。

## 4. 生命周期和完整外部调用链：已知段 / 未知段

### 4.1 可以直接绘出的源链

```text
[原宿主启动 / 保存配置 / 重载 / 更新 / clone：触发与次数未知]
  -> [原 _PluginBase 构造和资源装配未知]
  -> CategoryEditor.init_plugin(config)
     -> enabled/content 成员赋值
     -> CONFIG_PATH/category.yaml UTF-8 覆盖
     -> CategoryHelper() -> init()
        -> [原 YAML 解析、规则缓存、下载/整理分类消费者未知]
     -> self.systemmessage.put(固定正文, title=固定标题)
        -> [原系统消息持久化/队列/呈现未知]
     -> enabled=False
     -> self.update_config({enabled:false, content:...})
        -> [原实例配置命名空间、存储、缓存及异常处理未知]
  -> [原宿主 get_state / 注册事件命令调度 / 响应未知]

[原宿主配置表单请求]
  -> CategoryEditor.get_form()
     -> INNER_CONFIG_PATH/category.yaml UTF-8 读取
     -> (精确表单结构, 默认模型)
  -> [原宿主配置合并/保存及前端处理未知]

[原宿主停止 / 重载 / 更新 / 卸载]
  -> CategoryEditor.stop_service() -> None（本地 no-op）
  -> [宿主资源/注册/配置/代码目录处理未知]
```

### 4.2 各生命周期的结论边界

| 生命周期 | 已发现 | 不能自行宣称 |
|---|---|---|
| init / 普通 call | 只有 init 是业务写入口；getters 不会自行 init；get_state 返回当前成员；命令/API/页面无声明。 | 目标 `invoke`/`run` 不能作为原本存在的独立业务入口；宿主能否按名字调用任意方法、如何传配置待核对。 |
| stop | plugin stop no-op。 | 不能把 stop 自动等同于 enabled=False、删除配置文件或恢复内置规则；宿主清理注册待核对。 |
| reload | 重复调用同实例 init 与构造新实例有不同内存初值；空配置尤为可见。 | 原宿主 reload 是同实例还是新实例、调用次数、异常后保留对象及事件顺序未知。 |
| update | 此版本没有自定义迁移/更新函数。 | 不得发明 1.2→1.3 数据迁移、升级前/后初始化次数或更新失败回滚规则。需要原 manager/installer。 |
| uninstall | 此类没有卸载方法，也没有删除 YAML 的代码。 | 宿主是否调用 stop、删除 DB/数据、保留 `category.yaml` 必须补源码；不能以插件无 hook 推断全宿主一定保留或一定删除。 |
| clone | 原文件路径不含实例身份，成员会由实例赋值遮蔽类默认；配置前缀是 metadata。 | 克隆命名、配置复制、enabled 重放防护、是否拷贝/改写源码、init 次数、共享 helper 语义均需实际原 clone 链；不能强行隔离全局 YAML。 |
| restart / crash | 文件写与配置回写不在同一事务；停用插件不表示策略已撤销。 | 崩溃恢复、helper 冷启动重读、存储原子性与失败通知待宿主补源。 |

普通事件/命令/调度并未被用户排除；但此插件没有自己声明这些入口。必须确认基类默认与宿主注册怎样处理 `None`，不需为 CategoryEditor 凭空增设定时业务。

## 5. 必须补齐的同运行版本原资料（独立 oracle 的最小闭包）

优先获取正在运行容器的**代码/公开内置资源**只读副本，逐文件哈希及完整来源信息。以下已知导入路径是精确要求；其下依赖文件按真实 import 展开，不猜上游路径。

| 优先级 | 资料 / 精确入口 | 要闭合的问题 |
|---|---|---|
| P0 | `app/modules/themoviedb/__init__.py` 中 `CategoryHelper` 来源及实际定义文件、全部本地依赖 | 构造、单例、init 次数；用户/默认 YAML 优先级；解析器 loader；空/无效/错误类型的 YAML；错误吞没/通知/回退；缓存替换/保留；分类字段、顺序、匹配规则。不能只截取 init 数行。 |
| P0 | `app/plugins/__init__.py` 的完整 `_PluginBase` 及其父类/实例装配 | `__init__` 与 `systemmessage` 来源、配置命名空间、update_config 的返回/异常、get_service 等继承默认、类名与分身身份。 |
| P0 | `_PluginBase.update_config` 真实调用到的 config operator、模型、存储会话/缓存实现 | 新对象替换还是 merge；未知键处理；写入 False/None/异常；缓存已改但 DB 失败；重启读值；事务边界。可用新建隔离 SQLite，不要带生产 DB/密钥。 |
| P0 | `systemmessage` 的实际类与 `.put` 全调用链 | 本地系统消息还是外部发送、字段/排序/去重/持久性、返回与失败；oracle 仅连接隔离消息存储/队列，绝不发送生产通知。 |
| P0 | `app/core/config.py` 的路径定义与必要公开配置资源；`settings.INNER_CONFIG_PATH/category.yaml` 的原始字节 | 内置 form 默认值与分类默认值；路径何时固定；初始化是否建立文件；编码/换行。不得取目标默认 YAML 代替。不要导出环境 secrets。 |
| P0 | 实际 `PluginManager` 与 startup/config-save/reload/stop/register 调用它的代码 | 初始化次数、实例生命周期、配置是否先保存、异常捕获/HTTP 响应、get_state 值处理、注册空声明、并发序列化。路径需在同源环境通过符号查明。 |
| P1 | 实际插件 install/update/uninstall/clone 的 manager、路由、installer/文件改写代码 | 更新/克隆触发 init 次数与开关复制、共享 YAML、uninstall 保留/删除规则；失败后的代码/配置/运行对象。 |
| P1 | 调用 CategoryHelper 的下载选目录与整理分类消费者、相关媒体信息模型/枚举 | “立即生效”必须观察下一次真实宿主分类结果，不能只验证 `.init()` 被调用。纯本地合成元数据即可，不需要下载种子或搬移媒体。 |
| P1 | 原 requirements/锁文件、Python 版本、YAML 包版本、启动版本标识/镜像 digest | 固定运行时异常、YAML 标量/顺序/标签、Path IO/换行行为；原源 SHA 与依赖可再现。 |
| P1 | 配置表单读取/保存路由及对应前端的默认合并逻辑 | 未保存默认、已保存关闭配置、空配置、预览与更新后显示；不将默认模型误作实际用户配置。 |

**资料缺口独立登记：** 当前用户实际 `content`、当前 `enabled`、用户 `category.yaml` 是否与配置一致、内存 helper 当前规则、源宿主版本与源 Git SHA 均未知。业务算法可以用原默认资源与人工合成数据建立隔离 oracle，不需要生产凭据；但“保留用户现存实际配置/策略”的交付不能在这些状态未知时声称完成。若之后读取运行态，应只读、最小化、脱敏，并单独绑定来源；不要用插件默认值补猜。

## 6. 建议差分场景与观察项（执行状态见第 10 节）

以下编号仅作审查建议，不是竞争权威账本或完成状态。发现阶段均未执行；随后完成的受控原源子集详见第 10 节，宿主端到端与目标差分不在本审查中宣称完成。每次应同时记录原/新返回值及类型、异常类型、成员、文件字节、helper 规则/缓存、系统消息、存储配置及事件顺序。测试必须绑定本源 SHA 与同原宿主闭包；不从迁移版输出生成期望。

### A. 参数与状态

1. 新实例依次 `get_state`、`init(None)`、`init({})`；再对已有状态实例重复假值初始化，区分保留/新建。
2. 非空配置缺 enabled、enabled null、False、0、空串/列表/对象；检查 get_state 原值，不仅真值；确认无写入而 `_content` 被更新。
3. enabled True、1、非空字符串（包括 `"false"`）、非空容器；判断宿主入口允许的值与 Python truthiness。非法类型被宿主拒绝的场景应在宿主层记录，不偷换为插件校验。
4. content 缺失/null/False/0/空容器归一空串；空字符串 enabled=True 会尝试写空文件，不自动拒绝；具体 helper 结果待原运行确认。
5. content 中文、emoji、CRLF/CR/LF、尾换行、BOM、大文本；比较文件字节、表单读回文本与 YAML 消费效果。
6. content 真值数字/列表/对象、含无效 Unicode 代理字符；观察原准确异常和文件是否已截断。
7. 真值非映射 config、未知键；观察宿主校验/插件 AttributeError 边界，以及成功回写后的其它键保留/丢失规则。

### B. 正常应用与实际分类

8. 合法自定义规则 apply：精确 A→B→C→D→E；get_state=False；本地系统消息正文/标题正确；配置 content 保留而 enabled=False。
9. 对相同内容再次显式 enabled=True：不得因等值而省掉文件写、helper、消息调用；存储可另有等值处理，须原源观测。
10. UI/get_form：未保存与已保存各状态，确保插件默认模型始终来自原内置 YAML；宿主叠加已保存值另行比较。重复 form 不允许因目标设计而触发 apply。
11. 内置 YAML 不存在、权限不可读、非法 UTF-8；分别观察 form 的异常传播和宿主错误响应。
12. helper 用多条有顺序/重叠条件的规则，电影/电视剧/缺字段/未匹配/全部条件；apply 后立即通过原下载目录选择及整理分类消费者查询纯合成元数据结果，不产生实际下载/整理。
13. 空 YAML、null/scalar/list 根、无效语法、重复键、未知字段与 loader 支持的边界；先记录原 helper 是否错误/回退/保留缓存，再要求目标相符。未读 helper 前不可预定答案。

### C. 分阶段错误与恢复

14. 写文件的父目录缺失/权限不可写/目标为目录；故障前后成员、配置、文件及 helper 状态；不要只断言有 error。
15. 隔离文件写中途故障（例如受控 writer/文件系统注入），观察部分写入与无回滚；注入用例需注明与真实 IO 用例的区别。
16. helper 构造失败与 `.init()` 失败分开；确认文件保留、没有消息和关开关回写；原 helper 内部吞错再返回的场景单独覆盖。
17. systemmessage 在入队前失败与（若原实现可能）存储后失败；enabled 在调用返回前仍未重置，下一次初始化是否重复消息需真实宿主确认。
18. update_config 返回 False/None、抛异常、DB 写失败/缓存分歧；内存 False 与持久值分开断言；正常返回值不得由目标自行升级为失败。
19. 在 A 后/B 后/C 后/D 后/E 后模拟隔离进程终止并重启；观察文件、缓存、配置与再次通知次数；不能靠目标日志自证恢复。

### D. 生命周期、共享对象与宿主链

20. 同实例成功 init→空配置 init→get_state，对比完整 reload 后空配置；成功后 disabled 重启不应被迁移自动“补跑”。
21. init 失败后的普通 call/get_state、再保存修复配置、reload；区分原宿主保留对象、丢弃对象、失败响应和注册阶段，不推定。
22. stop 前后成员、全局 YAML 与 helper 比较；卸载/重装在纯隔离实例确认文件与配置留存；不访问现用 NAS 插件目录。
23. update 1.3→相同固定 1.3 重载/刷新路径（若宿主存在），失败更新和原源码指定升级行为；不臆造旧版本迁移。
24. 原实例与合成 clone 的配置身份、类名/prefix、init 次数；enabled 复制后行为；A/B 交替应用确认全局 YAML 与 helper 可见性，分身是否自带改写由原 clone 链决定。
25. 同实例两个 apply 与两个分身 apply 的受控交错；确认宿主是否串行及配置写回中 `_content` 被另一调用替换的可能，不能仅测试并发文件最终值。
26. 启动排序中 plugin_order=5；get_command/get_api/get_page 返回 None 时宿主无多余事件/命令/调度；基类默认方法另外核对。

关键负向/变异建议：交换 C/D、跳过 helper reload、form 改读 user YAML、将 None/False 统一 bool、把失败后文件回滚、自动 dedup 相同内容、每 clone 独立 YAML，均应使至少一项源绑定断言失败。测试只用 mock 记录调用可覆盖局部顺序，但不能替代原 helper/存储/宿主的真实端到端比较。

## 7. 当前目标接入约束（非原行为证据）

只读当前恢复目录的入口/SDK用于接口定位；未审查 CategoryEditor 目标业务实现正确性，也未复用现有 target 测试或 oracle 作为原答案。

- `light-media/cmd/server/main.go:21–85` 提供指定 `-data-dir` 和 `-safe` 管理启动；本审查未运行。日后测试应使用新的独占隔离数据目录，不触原配置。
- `internal/plugins/protocol.go:75–112,165–220,223–255` 定义 Go executable manifest、methods/capabilities、实例身份和协议握手；`stop` 是必需 method。这不说明原 CategoryEditor 存在额外业务调用。
- `internal/plugins/worker.go:13–30,55–70,97–124` 定义 Invocation、Handler/HostCaller 和持久会话；同一源实例内存需要随会话保留，不可每次 getter 新建插件业务对象。
- `internal/plugins/base.go:9–16,32–35,93–108` 提供实例作用域 `plugin.base.get_config` / `update_config`；后者可返回 nil/bool。如何映射源 `update_config` 须以补齐的原源为准。
- `internal/app/plugin_original_lifecycle.go:54–71,74–120,165–195,234–245` 为目标 direct init/start/stop/reload 接入点；`plugin_original_direct_dispatch.go:41–50` 为普通调用接入点。目标注释自述“original”不构成已验证的源合同。
- 元数据和接入约束可以复用已有框架，不需要恢复或扩大被用户停止的“插件平台重构”任务。现有接口是否足够应由此插件实际需求驱动，不扩大到已排除的页面/API、workflow/AI 或官方认证。

目标参考文件内容绑定（仅记录本次所读快照，不表示验收）：

| 文件（相对 light-media） | SHA-256 |
|---|---|
| `internal/plugins/protocol.go` | `4dd45fba68965bed4255ecd4382036914b40f45945da6313fbe9ea46c8119454` |
| `internal/plugins/worker.go` | `b91a555c564512af5cf4b5fee799244a21dd3c26e08bf018df1aea1665ecdb57` |
| `internal/plugins/base.go` | `88d704b1249fd6af462f8dc8157586f1e30d579e4cc4719abf3289821f311aed` |
| `internal/app/plugin_original_lifecycle.go` | `8a510251edc033f3aefdb89ecd5209aff219225f11270047d79c359fbca56d03` |
| `internal/app/plugin_original_direct_dispatch.go` | `8785a4121a5dbb177798856b8c9ec1bb4da18996baccd796e8769d8d412587d1` |
| `cmd/server/main.go` | `903e44dd9932bd697fbe0296fee981e63723213b6b5ed49fd30e16d923390d60` |

## 8. 审查结论：已发现 / 待验证 / 缺口

1. **已发现：** 固定插件全文件与所有本地定义入口已逐行归属；业务重点不只是保存 YAML，还包括重载 helper、本地系统消息、一次性关闭/持久配置回写及失败时各阶段不同的部分状态。表单默认读内置文件、原始 Python 真值/类型、空配置保留内存和全局文件共享是高风险漏项。
2. **待验证：** 受控原源回放已覆盖第 10 节子集；本报告没有运行目标差分或对目标作通过判断。并发、崩溃恢复、更新卸载分身及真实下载/整理消费者链仍需要原宿主端到端，单测桩不能关闭这些项。
3. **资料缺口：** 第 5 节 P0 中部分已有归档原源，仍需确认本次 NAS 同源并补齐实际存储/单例链；用户当前配置/策略未包含在源包。目标 Git 基线身份已只读复核闭合。不能以缺料为理由排除行为或声称完整迁移。

## 9. 追加：已交付 main 中的归档原宿主证据

### 9.1 归档 CategoryHelper（不是当前 NAS 认证）

- 完整源 `light-media/testdata/plugins/category-editor-1.3/installed-category-helper.py`，8294 字节、225 行，SHA-256 `1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2`。
- `installed-reference.json`（SHA `eef07642049d763fd39016ac8660c3ec0b92a54e365a382a822aa195ff09ff62`）记录 `helper_and_kv.at=2026-10-20T11:44:16.559139+00:00`、原路径 `/app/app/modules/themoviedb/category.py`，与该文件 SHA/字节一致。归档插件本体与此次 NAS 本体 SHA 完全相同，**这不能推出 helper 八天后仍相同**。
- 同归档中 2026-10-20 用户 config enabled=False、content/file 哈希一致、plugindata 数量为 0；这里只能作为历史状态，不能替代 2026-10-28 当前用户配置迁移证据。
- `reload-yaml-provenance.json`（SHA `9647c36d4400f72f686e24d5fd1059b1e61df102ae391d0a60c9b4961250cf80`）记录 ruamel.yaml `0.19.1`、33 个 Python 源哈希和 wheel SHA `27592957fedf6e0b62f281e96effd28043345e0e66001f97683aa9a40c667c93`；本次下载相同 wheel 并逐个匹配全部哈希后用于隔离测试，不是产品 Python 依赖。

归档 helper 全文独立发现：

1. 构造器 L33–38 初始化三个 dict 后自动 `init()`；因此 `CategoryHelper().init()` 在无现存对象时可能有构造初载和显式重载两次。实际 `WeakSingleton` 保留/回收要另补源。
2. `init` L44–55：缺 user 文件时 `shutil.copy` 内置文件；以 UTF-8、`errors='replace'` 打开；用 `ruamel.yaml.YAML()` 默认 loader 读取。解析异常只 warn 且 `_categorys={}`；外层 IO/copy 错误只 warn，不清空此前 `_categorys`。
3. L57–59 **只有 `_categorys` 为真值才更新 movie/tv 子缓存**。热态空/注释/null/false/0/空字典/空列表/解析失败会保留旧 movie/tv；冷态保持空缓存。真值 scalar/list 会在 try 外的 `.get` 抛错，旧子缓存仍未替换。只有 movie/tv 显式空 mapping 等真值根可实际重置子缓存。
4. 列表/匹配 L96–225：按 YAML 顺序逐类，空规则立即成为 catchall；空媒体无论规则如何均返回空字符串。多字段 AND，但缺字段只把 match_flag=False 后继续检查剩余字段，不保证提前短路避免其它错误。条件假值直接跳过。
5. `release_year` 用 release_date 或 first_air_date 的前四字符；production_countries 需要每项 `.get('iso_3166_1')`；其它媒体 list/scalar 转字符串并 upper。条件值直接 `.split(',')`，没有先转 str、没有 trim；数值真条件会延迟到匹配时报错，数值 0/False 因假值跳过。
6. 条件值支持逗号集合、`!` 排除、`-` 范围。数字区间 inclusive 逐项展开，未见长度上限；非数字范围只扩两端。匹配正集交集和负集排除。类型/范围/复杂度边界不能由迁移自动“改好”后声称严格等价。
7. `load/save` L62–94 也是 helper 自有 API，但 CategoryEditor 从未调用它们；不得把它们的模型校验或 header 输出偷偷应用到 CategoryEditor 的直接写文件路径。

### 9.2 归档 _PluginBase

- 完整源 `testdata/plugins/auto-backup-2.1.4/input/_auto_backup_port/reference/app-plugin-base.py`，SHA `e6ac4a8621e078bf729d34c01188a5ff664ee48ad5e09cb4cf66fc44336a647f`；与 lark 插件依赖中的 `reference/dependencies/plugins/__init__.py` 字节相同。
- `reference/source-input-manifest.json`（SHA `5da90ba15d995bd73d6872cbecef23d5ea3ed60e77444e28e76f82e98ef7a4b2`）固定原路径 `/app/app/plugins/__init__.py`、采集时间 `2026-10-18T02:35:11.993186+00:00`。
- L40–50 构造 plugindata/chain/systemconfig/MessageHelper/EventManager，不改 CategoryEditor 的 enabled/content。
- L89–94 `get_render_mode()` 返回 `("vuetify", None)`；L128–139 `get_service`、L141–161 `get_dashboard(key, **kwargs)`、L163–175 `get_dashboard_meta`、L342–343 `close` 均 `pass→None`。完整基类未定义 `get_sidebar_nav`，不能臆造已存在的默认 sidebar 方法。
- L239–247 `update_config` 默认 namespace 取 `self.__class__.__name__`，调用 `systemconfig.set("plugin." + id, config)`。仍未拿到同源 SystemConfigOper 的 set/cache/DB 完整闭包，不能把受控回放 recorder 的存储动作冒充真实 DB。

### 9.3 归档 manager / 路由方法

`testdata/plugins/plugin-module-original-port/combined-oracle/extracted-methods.json`，SHA `31980e7adf18f4323a7990305fea1ebd3b62800dfcd4e854903e0bfeeb7a424b`，包含原方法全文（不是目标 Go）。其 manifest SHA `ee5249b3d6458d800a72b838fd8653bf239aa8897e842a276adf8facaf27a4dd`，创建时间 `2026-10-23T16:47:05.674058+00:00`；记录完整原 plugin.py SHA `b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7`、api-plugin.py SHA `16cb070c0d3c64c376c490099da60bc45d973bd4b74e8521aba1fd8fedd1f5cd`。本审查只读归档 JSON 内的方法文本，没有访问其中历史 work 路径。已对所读关键方法重新计算 text SHA，与每条记录一致。

- manager `start` 原 L72–119：构造新对象→读配置 init→放进 running map→get_state 控制事件注册；单插件异常被 catch，不撤销先前外部副作用。保存运行对象之前 init 失败则该新对象不进入 running map。
- manager `init_plugin` L121–139：已运行同对象直接 init(conf)，再 get_state 设置事件开关；没有构造新对象。API `set_plugin_config` L976–989 先 save config，再 direct init，再注册服务；因此初始化失败前新配置可能已保存。
- manager `stop` L156–192 与 `__stop_plugin` L619–632：disable event→`close()`→`stop_service()`，再清类/实例/模块缓存；close 和 stop 在同一 try 内。CategoryEditor 的两方法均 no-op。
- manager `reload_plugin` L641–651：stop→start→广播 PluginReload；API reload L563–573 再注册调度、命令、API。故空配置 direct init 与完整 reload 的差别确有归档宿主来源。
- manager get_plugin_config L776–787：未注册类返回 `{}`；已存真值 config 去空 key，否则 `{}`。API plugin_form L649–677：get_form 默认与 stored config 做 `{**model, **(stored_config or {})}` 合并；catch get_form 错误后返回 `{}`。插件本体默认读内置与 UI 展示当前值不冲突。
- manager clone L1905–2002：复制目录、改类/metadata、复制配置并强制 `enable=False, enabled=False`；reload 一次；若运行且配置真值，再 direct init；API clone L936–962 成功后再 reload/register。Python 改写 L2061–2137 改类名/prefix/metadata 并加 `is_clone=True`，**不修改全局 category.yaml 路径**。配置 namespace 独立、策略文件共享可同时成立。
- API uninstall L993–1030：普通插件从安装/注册/文件夹移除；仅 clone 分支显式删其配置/data/代码目录，再 remove_plugin。该方法未删除全局 category.yaml；remove_plugin 后续仍缺完整原定义，不能宣称完整卸载链已实测。
- API install L577–623 先安装/计数、更新 installed 列表，再 reload；此归档可指导固定版本更新路径，但真实 installer/故障恢复并未被本次回放覆盖。

### 9.4 归档系统消息

`testdata/notice-message-core/input/_notice_message_port/reference/source/helper_message.py`，SHA `c2b3626b9efb4de5e698a67ebd4624d8f684f8618a29a925d8c8d5001f052e89`，其 source-manifest SHA `5a5f2e2851acc6dc14e93feafa99e7ea00ce508f79bbdba2b1a526391ff7b9f0` 绑定同一文件。该 manifest 没有足以认证本次 NAS 采集时间的来源字段，故其时效证据比上两份更弱。

MessageHelper L789–859 使用 `queue.Queue` 与 TTLCache，默认 role=`plugin`，供 SSE 本地消息；put 按 role/title/str(message)/note/分钟生成键，并用 60 秒 TTL 去重，随后队列 JSON 含 type/title/text/date/note。**重复 apply 会再次调用 put，但宿主最终队列可能只收到一条**。本次受控 recorder 记录的是调用，不是实际 SSE 入队/去重验收；没有把它当外部通知发送。

## 10. 追加：已执行的受控原源回放与明确限制

### 10.1 原插件本体

- 脚本：`evidence/categoryeditor-source-replay.py`，全量 compile/exec 此次固定原模块，不做方法摘录，不导入目标业务代码。
- 输入：`categoryeditor-source-inputs.json`；输出：`categoryeditor-source-oracle.json`；完整命令日志：`categoryeditor-source-replay.log`。
- 最终运行 **78 场景、220 步**，命令退出 0；每步保存 before/after 成员原始类型与值、是否实例成员、真实文件字节 base64/SHA/模式、精确消息参数、完整 update_config 参数、阶段顺序、异常类型和文本、受控 adapter 状态。只对本任务随机 fixture 路径前缀归一成 `$CASE`。
- OS 真实 IO 覆盖 missing parent、目标目录、写权限拒绝、默认文件缺失/目录/读权限拒绝/非法 UTF-8；类型错误、代理字符编码错误不是手写模拟异常。实测非字符串 write_text 在打开前拒绝并保留旧文件；孤立 `\ud800` 先截断真实文件为 0 字节再抛 UnicodeEncodeError。原 NAS Python 具体版本仍未认证。
- `settings`、基类资源、helper 构造/init、systemmessage、update_config 是明确可控适配器；消息/helper/save 的 raise/False/None 为故障注入。**adapter_store 不是真实 SystemConfig 数据库**。本体回放不证明宿主生命周期、通知去重、SQLite、并发或原 NAS 实机。
- 最终 oracle SHA-256：`a92f0462e1708e293baa21203029e78071512a4afc78a3a038b3774733b02aca`。

### 10.2 归档 helper + 当前原插件

- 脚本：`categoryeditor-source-helper-replay.py`；输入/原始输出/日志分别为 `categoryeditor-source-helper-inputs.json`、`categoryeditor-source-helper-oracle.json`、`categoryeditor-source-helper-replay.log`。
- 真实加载归档 helper 全模块；真实 ruamel.yaml 0.19.1 与真实独占本地文件；35 输入分别 warm/cold 观察。适配点仅 settings/logger、未用到的 CategoryConfig 声明、以普通 type 代替 WeakSingleton；因此单例生命周期仍不算实测。为保留失败后的对象字段，用 `__new__`+原 `__init__` 明确构造。
- 对 file 类输入再加载此次原插件全模块，将 factory 绑定到保留的真实 helper 对象，真实写入并执行原 helper.init，记录 message/update 调用。该链证明业务代码执行阶段，不冒充真正 WeakSingleton 或完整 MP 宿主。
- cycle 输入只在有 **8 秒 timeout** 的隔离子进程内运行；实际退出 0，无 hang。未创建、组织或匹配任何实际媒体文件；`../escape` 只作为字符串解析/返回，没有拿它建立路径。
- 最终 helper oracle SHA-256：`9fa95d762b18da0ee90973f4b494bf508a4ce68546834d11d214795df66d78a6`。

具体可达原行为观察：

| 输入 | 原 helper / 原插件真实观察 | 迁移影响（不是目标审查结论） |
|---|---|---|
| `movie:\n  Numeric:\n    genre_ids: 16\n` | init 正常；列表 `['Numeric']`；空 media 返回 `''`；原插件完成写入/put/update，enabled=False；含 genre_ids 的 media 匹配才报 `AttributeError: 'int' object has no attribute 'split'`。 | 若目标在 apply 时预校验拒绝，将改变错误阶段、文件/消息/关闭状态、无 media 列表接口，不能称为原样等价。 |
| 65 KiB 纯注释 + 合法 mapping（总 66592 字节） | warm/cold init 成功，原插件应用成功，分类返回“合法分类”。 | 64 KiB 上限不是此原源规则；若目标保留限制需要明确差异/授权，不应把“太大”伪称原错误。 |
| 分类名 `'../escape'` | init/apply 成功，列表及匹配返回原字符串。 | 安全路径消费仍必须防穿越；不能为复刻而操作实际路径。可在写策略与消费阶段分离安全策略，差异需明确。 |
| `movie: &m\n  Recursive: *m\ntv: {}\n` | ruamel 将 Recursive 项解析为 None；bounded child 正常结束；原 helper 返回“Recursive”，原插件应用关闭成功。 | 不可假定所有 alias/cycle 都必须 init 抛错；也不能据此推断所有循环/别名输入都有界。 |
| blank/comment/null/false/0/空对象/空数组/语法错/重复键/多文档 | warm 保留“缓存电影”，cold 返回空分类；解析异常 warn 后继续，原插件可正常关闭。 | “空文件立即清空热缓存”或“所有 YAML 错误抛回 init”均需区别于原行为。 |
| root 真值 scalar/list | helper 在 try 之外 `.get` 抛错，warm 原子缓存保留；原插件文件已写，通知/关闭/配置回写不执行。 | 需保留不同于语法解析异常的失败路径。 |

### 10.3 自检、复现与剩余缺口

`categoryeditor-source-checks.py` 对以上原始记录按直接源码合同做 105 项 harness sanity 检查，退出 0，原始结果为 `categoryeditor-source-check-results.json`、日志 `categoryeditor-source-checks.log`。它没有运行迁移版，不是 differential/real_e2e，也不是目标通过结论。

复现命令（在任务目录，测试依赖已安装到当前云端兄弟 toolchain；从可信 wheel 重装时先核归档 SHA）：

```sh
python3 evidence/categoryeditor-source-replay.py --generate-inputs
python3 evidence/categoryeditor-source-helper-replay.py --dependency-root ../toolchain/python-oracle/ruamel-0.19.1
python3 evidence/categoryeditor-source-checks.py
```

输入、脚本、日志、原始输出及报告的 SHA 清单见 `categoryeditor-source-artifact-manifest.json`；独占 fixture 保留在 oracle 记录的 `evidence/categoryeditor-source-*-fixtures-*`，未清理源数据或测试证据。随机目录路径变化会改变输出环境字段和整体文件 SHA，但所有 case 观察路径已有限归一；重跑须生成新证据绑定，不覆盖成“同一次测试”。

**尚未闭合：** 当前 NAS helper/base/manager/消息链与归档字节的相等性、当前内置 YAML 字节、真实 WeakSingleton、SystemConfigOper/DB/缓存、实际配置数据迁移、真实系统消息去重/呈现、宿主 clone/update/uninstall/reload 全链、并发/崩溃恢复、真正下载/整理消费者链、以及迁移版实际差分和目标证据绑定。本次仅建立独立、可复现的原源观察基准；这些缺口没有被降级为“已等价”。

