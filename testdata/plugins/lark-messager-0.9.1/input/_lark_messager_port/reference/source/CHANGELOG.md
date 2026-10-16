# LarkMessager 更新日志

## v0.9.1 - 2026-07-10

### 新增

- **管理员支持邮箱 / 手机号（AI 智能助手自动识别）**：管理员配置合并为**单个输入框**，可混填邮箱、手机号或 Lark Open ID（`ou_xxx`），任填其一即可，多个用 `,` 分隔。插件在保存配置时通过 Lark 通讯录接口（`contact:user.id:readonly`）把邮箱 / 手机号解析为 Lark Open ID（`ou_xxx`），并写回 `SYSTEMCONFIG.Notifications` 中名为 `Lark` 的通知配置（`FEISHU_ADMINS`）。AI 智能助手的渠道管理员判定只读该配置、只比对 Open ID、且不解析邮箱，因此此前填邮箱 / 手机号对智能助手无效；现在由插件在后台完成解析与同步，智能助手即可识别管理员。

### 说明

- 使用邮箱 / 手机号管理员需在 Lark 开放平台为应用开通 `contact:user.id:readonly` 权限作用域并重新发布版本。
- 解析在后台线程完成（不阻塞插件启动）；解析结果写入 `Notifications` 时带变更守卫，避免触发配置重载死循环。
- **App Secret 显示 / 隐藏按钮**：配置页 App Secret 字段新增眼睛图标按钮，默认隐藏（密文），点击切换明文显示；通过表单渲染器的 `{{ }}` 表达式绑定 `type` / `append-inner-icon`，并用 `onClick:append-inner` 切换模型字段 `app_secret_visible` 实现。

## v0.9.0 - 2026-07-09

### 修复

- **流式卡片更新/关闭 404 修复**：CardKit 内容更新端点实际要求 `PUT`（非 `POST`），关闭端点要求 `PATCH`（非 `POST`）；原代码用 `POST` 导致 Lark 网关判为未匹配路由、返回纯文本 `404 page not found`，流式卡片的逐字更新与关闭静默失败。现对齐官方 `lark_oapi` SDK（`HttpMethod.PUT` / `HttpMethod.PATCH`）。
- **智能助手消息识别修复**：接收事件 `im.message.receive_v1` 的消息类型字段是 `message_type`，原代码误用发送 API 的 `msg_type` 字段，导致 `type` / `text` 恒为空、消息链报「未识别到有效消息」。改为 `message.get("message_type") or message.get("msg_type")`。
- **CardKit 不可用降级**：`_send_streaming_card_message` 在 CardKit 流式创建失败（最常见是 Lark 应用缺少 `cardkit:card:write` 权限作用域）时，降级为普通 interactive 卡片发送（走标准 `/im/v1/messages`，无需 cardkit 权限），保证消息仍可达。
- **CardKit 响应防御性解析**：Lark 国际版 `card_element.content` / `card.settings` 变更端点在成功时可能返回 `null` 后尾随 NUL/控制字符，原 `resp.json()` 抛 `JSONDecodeError: Extra data` 被误判为失败。新增 `_safe_parse_cardkit` / `_cardkit_ok`：剥离控制字符后解析，无 `code` 信息时以 HTTP 2xx 兜底、有 `code` 时以其为准。

### 说明

- 流式卡片需在 Lark 开放平台为应用添加 `cardkit:card:write` 权限作用域，并**重新发布应用版本**后生效。

## v0.8.0 - 2026-07-09

### 修复

- **列表场景补全海报图**：`post_medias_message`（媒体选择列表）与 `post_torrents_message`（资源选择列表）原先只发纯文字，现改为图文卡片——每个条目带媒体海报（取自 `MediaInfo.get_message_image()` / `Context.media_info`），与标题并排展示，翻页时同样生效。
- **卡片渲染支持图文列表**：`build_card_v2` 新增 `image_items` 参数，每项渲染为 `[海报 | 标题]` 一行（`column_set`）；无海报的条目回退为纯文字行。
- **通知发送支持图文列表**：`send_notification` 新增 `image_items` 参数，自动上传每个条目海报并渲染到卡片；单图（`message.image`）路径保持原逻辑不受影响。

### 说明

- 单条通知（下载完成 / 整理入库 / 订阅等）此前已通过 `message.image` 嵌入卡片，本次未改动其路径。
- 点击选择按钮后，原卡片会被 `edit_message` 更新为「已选择：XXX」反馈（文字），此为预期行为。

## v0.7.0 - 2026-06-30

### 新增

- **按钮回调支持**：注册 `EventType.MessageAction` 事件处理器，支持 `[PLUGIN]LarkMessager|action` 格式的按钮回调路由
- **广播通知回退**：`post_message` / `post_medias_message` / `post_torrents_message` / `send_direct_message` 无明确目标时自动回退默认用户/群聊
- **全链路诊断日志**：`get_module` / `post_message` / `_switchs` 过滤等关键节点添加 debug/info/warning 日志

### 优化

- **通知卡片样式**：通知类型 → header 模板色映射（下载→blue, 入库→turquoise, 订阅→green 等）
- **卡片视觉层次**：header 标题栏 + 分隔线 + 灰色信息栏
- **测试卡片美化**：schema 2.0，header 模板色 + 三列字段布局 + 双按钮

## v0.6.0 - 2026-06-29

### 新增

- **全面对齐内置飞书模块**：通过 `get_module` 注册 13 个消息链方法到系统 MessageChain
- **入站消息转发**：走 `_forward_to_message_chain` POST 到 `/api/v1/message`，与内置飞书模块完全一致
- **流式卡片**：支持 CardKit API（创建/更新/关闭），Agent 场景逐字输出
- **表情回应**：`mark_message_processing_started/finished` 标记处理状态
- **资源下载**：图片/文件/语音消息收发，富文本解析
- **消息编辑**：`edit_message` 支持流式卡片更新和普通卡片编辑

### 修复

- 修复 `reply_message` JSON 双重序列化 bug
- 卡片升级到 schema 2.0

## v0.5.0 - 2026-06-29

### 优化

- **通知卡片优化**：图片嵌入卡片内部而非单独发送，支持链接跳转
- **MIME 类型修复**：图片上传时使用 `mimetypes` 检测真实类型，替代通用的 `application/octet-stream`
- **参数传参修复**：修正 `upload_image` 参数传参方式（`params` → `data`）

## v0.4.0 - 2026-06-26

### 重构

- **收件人体系重构**：默认用户与默认群聊统一使用邮箱/工号/Open ID 实时转换，移除手动 Open ID 配置字段
- **管理员用户字段**：改为支持邮箱、工号或 Open ID，与默认通知用户保持一致

### 修复

- 修复卡片回调 `message_id` 为空导致回复失败
- 修复 `reply_message` 异常捕获
- 完善使用指南步骤并修复详情页重复标题

## v0.3.0 - 2026-06-25

### 修复

- 修复 Webhook 全链路（鉴权/签名/解密/事件结构/多 worker 惰性初始化）
- 修复签名校验导致 Lark 后台 URL 验证失败（`challenge` 必须在签名校验之前处理）
- 修复测试消息无反馈（改用 `save_data` 持久化 + `get_page` 重渲染方案）

### 新增

- 新增详情页使用指南卡片与一键获取 ID 按钮
- 移除插件文件头部 docstring（避免与插件名称重复）

## v0.2.0 - 2026-06-24

### 新增

- 配置项对齐主仓库飞书通知风格
- 动态注册 `MessageChannel.Lark` 通知渠道

### 修复

- 修复签名校验、图片格式判断及插件加载失败问题
- 修正插件元数据与名称

## v0.1.0 - 2026-06-23

### 新增

- 初始版本发布
- 支持 Lark 应用消息发送（文本/卡片/图片/文件）
- 支持事件订阅 Webhook（URL 验证、消息接收、卡片按钮回调）
- 支持 `NoticeMessage` 通知转发到 Lark
- 支持远程命令 `/lark_test`
- 完整的消息加解密（AES-256-CBC）与签名校验
