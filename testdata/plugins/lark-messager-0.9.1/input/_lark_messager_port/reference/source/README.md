# Lark 应用消息通知 - LarkMessager

基于 [Lark 开放平台](https://open.larksuite.com/)（国际版飞书）自建应用的消息通知与交互插件，为 MoviePilot 提供完整的 Lark 消息通道。

## 模块结构

```
larkmessager/
├── __init__.py     # 插件主类：生命周期、配置表单、详情页、API 端点、事件处理
├── client.py       # LarkClient：Token 管理、消息发送、卡片构建、媒体上传/下载、用户查询
├── crypto.py       # LarkCrypto：AES-256-CBC 解密 + SHA-256 签名校验
├── schemas.py      # Pydantic 数据模型：Webhook 事件、用户消息、卡片按钮回调
└── requirements.txt # 插件依赖库
```

---

## 配置指南

### 前置条件

- 已有 [Lark 开放平台](https://open.larksuite.com/) 账号（企业管理员权限）
- MoviePilot 服务已部署并可从外网访问（或通过内网穿透/反向代理暴露）

### 第一步：创建 Lark 自建应用

1. 打开 [Lark 开放平台](https://open.larksuite.com/app)，登录后进入**开发者后台**
2. 点击 **「创建应用」** → 选择 **「自建应用」**
3. 填写应用名称（如 `MoviePilot 通知`）、描述、图标
4. 创建完成后进入应用详情页，在 **「凭证与基础信息」** 页面复制：
   - **App ID**（格式如 `cli_aaa16e5421f99e17`）
   - **App Secret**（点击查看/复制）

> 这两个值需要填入 MoviePilot 插件配置的 `App ID` 和 `App Secret` 字段。

### 第二步：添加应用能力 - 机器人

1. 在应用详情页左侧菜单找到 **「添加应用能力」**
2. 选择 **「机器人」**，点击添加
3. 添加后应用就具备了收发消息的能力

> ⚠️ **必须先添加机器人能力，否则后续的权限和事件订阅都无法正常工作。**

### 第三步：配置应用权限

在应用详情页左侧菜单进入 **「权限管理」**，开通以下权限。

| 权限标识 | 权限名称 | 用途 |
|----------|----------|------|
| `im:message` | 获取与发送单聊、群组消息 | 发送通知消息 + 接收用户消息 |
| `im:chat` | 获取与更新群组信息 | 查询群聊列表、群聊详情 |
| `im:resource` | 获取与上传图片或文件资源 | 上传图片到 Lark（通知卡片图片、用户头像等） |
| `contact:user.base:readonly` | 获取用户基本信息 | 显示用户名称等资料 |
| `contact:user.id:readonly` | 通过手机号或邮箱获取用户 ID | 支持在插件配置中使用邮箱/手机号指定收件人（替代 Open ID） |
| `cardkit:card:write` | 读取与更新消息卡片内容（CardKit） | 启用 AI 智能助手流式卡片（逐字/流式输出回复、实时更新与关闭卡片） |

**权限开通步骤：**

1. 进入 **「权限管理」** 页面
2. 在搜索框输入权限标识（如 `im:message`）
3. 找到对应权限，点击 **「开通权限」**
4. **全部权限配置完成后**，必须进入 **「版本管理与发布」** 创建并发布新版本，权限才会生效

> ⚠️ **常见坑**：权限开通后，一定要发布新版本！否则 API 调用会报 403 或 "Access denied"（code=99991672）。

### 第四步：配置事件订阅（Webhook）

1. 在应用详情页左侧菜单找到 **「事件与回调」→「事件订阅」**
2. **请求地址**填写：

```
http(s)://<你的MoviePilot地址>/api/v1/plugin/LarkMessager/webhook
```

3. 点击「保存」——此时会发送 URL 验证请求，如果 MoviePilot 正常运行且插件已启用，应能验证通过。

**加密策略配置（推荐开启）：**

在 **「加密策略」** 区域：

| 配置项 | 说明 | 是否必填 |
|--------|------|----------|
| **Encrypt Key** | 用于消息加解密和签名校验的密钥 | 可选但强烈建议开启 |
| **Verification Token** | 验证请求来源的 Token | 必填 |

- 如果开启了 Encrypt Key，复制其值填入插件配置的 **Encrypt Key** 字段
- 复制 Verification Token 值填入插件的 **Verification Token** 字段
- **重要**：Lark 后台的值必须与插件配置完全一致，否则消息无法正常收发

### 第五步：添加事件

在 **「事件与回调」→「事件订阅」→「添加事件」** 中，搜索并添加以下事件：

| 事件 | 说明 |
|------|------|
| `im.message.receive_v1` | 接收用户发往应用的消息（私信 + 群聊@机器人） |

> 如果只需要单向推送通知（MoviePilot → Lark），可以不添加任何事件。
> 卡片按钮回调（`card.action.trigger`）无需单独添加事件，卡片交互由系统自动触发。

#### ⚠️ 关键步骤：开通事件相关权限

添加 `im.message.receive_v1` 事件后，所需权限列表中会显示以下权限项，但这些权限**默认未开通**！必须逐个点击每个权限项右侧的 **「开通权限」** 按钮，确认状态变为「已开通」后才能正常接收消息：

- 获取群组中用户@机器人消息
- 获取群聊中所有的用户聊天消息
- 读取用户发给机器人的单聊消息
- 获取群组中其他机器人和用户@当前机器人的消息
- 获取群组中所有消息（敏感权限）

> ⚠️ **如果跳过此步骤，Lark 将不会向你的服务器推送任何消息事件！**

### 第六步：发布应用版本

1. 左侧菜单进入 **「版本管理与发布」**
2. 点击 **「创建版本」**，填写版本号和更新说明
3. 提交后等待管理员审批（如果是自己创建的应用通常秒过）
4. 审批通过后点击 **「发布」**

> 只有已发布版本的事件订阅和权限才会生效。

### 第七步：在 MoviePilot 中配置插件

回到 MoviePilot → 设置 → 插件 → LarkMessager：

| 字段 | 填写内容 |
|------|----------|
| **App ID** | 第一步复制的 App ID |
| **App Secret** | 第一步复制的 App Secret |
| **默认通知用户** | （可选）填邮箱、手机号或 Open ID（`ou_xxx`），留空则不发送私信。**使用邮箱/手机号需开通 `contact:user.id:readonly` 权限**（见常见问题） |
| **默认通知群聊** | （可选）填群聊 Chat ID（`oc_xxx`），留空则不发送群通知 |
| **Verification Token** | 第四步的 Verification Token |
| **Encrypt Key** | 第四步的 Encrypt Key（强烈建议填写） |
| **管理员用户** | （可选）允许执行命令和调用管理员工具的用户。可**混填邮箱、手机号或 Lark Open ID（`ou_xxx`）**，任填其一即可，多个用 `,` 分隔。**插件会自动识别类型并把邮箱/手机号解析为 Open ID，同步给 AI 智能助手**，因此智能助手也能识别为渠道管理员（不再需要手动查 Open ID）。使用邮箱/手机号需开通 `contact:user.id:readonly` 权限 |
| **通知场景类型** | 选择哪些场景触发通知（留空=全部） |

保存设置后，状态应显示为「已启用」。

> **提示**：如果不想开通 `contact:user.id:readonly` 权限，可以直接填用户的 Open ID（`ou_xxx`）。获取 Open ID 的方法：
> 1. 在插件详情页点击 **「获取已加入的群聊」** 按钮
> 2. 或者在 Lark 管理后台查看用户详情页的 Open ID

### 第八步：测试连通性

1. 在插件页面点击 **「发送测试消息」** 按钮
2. 应在 Lark 中收到一条测试卡片消息
3. **在测试卡片中点击「点击确认」按钮**，系统将回复 **「测试确认成功」** 消息
4. 收到回复说明消息发送和事件回调均已配置正确，连通性验证完成！

> 点击「点击确认」按钮后如果没收到回复，说明事件订阅（Webhook）配置有问题，请检查第四步和第五步。

---

## 常见问题

### Q: URL 验证失败 / Challenge code 没有返回

- 检查 MoviePilot 是否正常运行、插件是否已启用
- 确认地址可被 Lark 服务器访问（内网环境需穿透）
- 确认 Encrypt Key 和 Verification Token 与 Lark 后台完全一致

### Q: 收不到消息

- 检查应用是否已发布（未发布的版本不生效）
- 检查是否已添加「机器人」应用能力（第二步）
- 检查添加 `im.message.receive_v1` 事件后，是否逐个开通了相关权限（第五步关键步骤）
- 检查 Open ID / Chat ID 是否正确
- 查看 MoviePilot 日志中 `[larkmessager]` 相关输出

### Q: Webhook 返回 403

- 如果配置了 Encrypt Key 但 Lark 请求不带签名头：这是 Lark 国际版的已知行为，插件已做兼容处理
- 确保已重启 MoviePilot 让最新代码生效（文件热重载可能不够）

### Q: 报错"缺少权限 contact:user.id:readonly"（code=99991672）

使用了「默认通知用户」或「管理员用户」字段填邮箱/手机号时，需要 Lark 应用具备「通过手机号或邮箱获取用户 ID」权限：

1. Lark 开放平台 → 你的应用 → **「权限管理」**
2. 搜索并开通 `contact:user.id:readonly`（通过手机号或邮箱获取用户 ID）
3. 进入 **「版本管理与发布」**，创建并发布新版本（**权限变更必须重新发布才生效**）
4. 回到 MoviePilot 重新点「发送测试消息」

> 不使用邮箱/手机号收件人（只用群聊 Chat ID 或 Open ID）则无需此权限。

### Q: AI 智能助手流式回复收不到 / 提示缺少 cardkit:card:write 权限

AI 智能助手的「逐字流式输出」基于 Lark CardKit 流式卡片接口，需要应用具备「读写消息卡片」权限：

1. Lark 开放平台 → 你的应用 → **「权限管理」**
2. 搜索并开通 `cardkit:card:write`（读取与更新消息卡片内容）
3. 进入 **「版本管理与发布」**，创建并发布新版本（**权限变更必须重新发布才生效**）
4. 重启 MoviePilot，让最新代码与权限生效

> **未开通时的行为**：插件会自动降级为普通 interactive 卡片发送，AI 回复仍可正常送达（只是不再有逐字流式效果）。若开通并发布后仍失败，请检查应用已发布且 MoviePilot 已完全重启。

---

## API 端点

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `POST` | `/api/v1/plugin/LarkMessager/webhook` | 匿名（Lark 签名校验） | Lark 事件回调入口 |
| `GET` | `/api/v1/plugin/LarkMessager/test` | Bearer Token | 发送测试消息 |
| `GET` | `/api/v1/plugin/LarkMessager/status` | Bearer Token | 查询插件运行状态 |
| `GET` | `/api/v1/plugin/LarkMessager/fetch_chats` | Bearer Token | 列出本应用已加入的群聊 |

## 事件处理

| Lark 事件 | 处理逻辑 |
|-----------|----------|
| `url_verification` | 返回 `{"challenge": "..."}`，完成 URL 验证 |
| `im.message.receive_v1` | 提取消息文本，构造 `CommingMessage` 转发到 MoviePilot `UserMessage` 事件 |
| `card.action.trigger` | `action_id=test_ok` 时回复确认消息；其他 action 转发到 `MessageAction` 事件 |

MoviePilot 系统通知通过监听 `NoticeMessage` 事件，将标题和内容构建为交互式卡片推送到所有配置的接收目标。

## 架构说明

```
Lark 用户/群聊
    │
    ▼ (消息/卡片点击)
┌─────────────────┐
│  Lark 开放平台    │ ← 事件订阅 + 卡片回调
│  (加密转发)       │
└────────┬────────┘
         │ POST /webhook
         ▼
┌──────────────────────────────┐
│  MoviePilot                  │
│  ┌────────────────────────┐  │
│  │  LarkMessager 插件      │  │
│  │  ├─ 签名校验            │  │
│  │  ├─ 消息解密            │  │
│  │  ├─ Token 校验          │  │
│  │  ├─ Challenge 响应      │  │
│  │  └─ 事件分发/消息推送    │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

## 依赖

```
pycryptodome>=3.20.0
requests>=2.28.0
```

> `lark-oapi` 已在 requirements.txt 中声明但实际未使用，消息收发全部基于 `requests` 直接调 REST API。

## 技术要点

- **Webhook 安全**：`allow_anonymous: True` 绕过 MoviePilot 框架的 apikey 校验（Lark 回调不携带 apikey），安全性由 Lark Encrypt Key 加密 + 签名校验保证
- **加密请求**：配置了 Encrypt Key 的请求体为 `{"encrypt": "base64..."}`，解密成功即视为可信来源，跳过签名校验
- **多 worker 兼容**：测试结果和群聊查询结果通过 `save_data` / `get_data` 持久化到数据库，跨 gunicorn worker 进程共享
- **Vuetify 渲染**：配置页用 `get_form`，详情页用 `get_page`，按钮点击反馈通过后端 `save_data` + `get_page` 重渲染链路实现
