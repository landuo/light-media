# 普通 OCR helper：有限后台实现与验收

本片是在已交付 `42d6ef0ae3b4697200c13c22b7353de52320af6a` 上**重新实现并重新验收**的普通非 LLM OCR helper，含 19 个新路径、3 个窄修改路径。不是失踪旧 S15 文件或日志的恢复。是否已推送，以 ledger 的实际远端读回为准。

## 已实现范围

`plugins.SharedOCR.GetCaptchaText` / `ocr.get_captcha_text` 经现有身份、能力、调用期校验进入 Go 宿主，处理四个可选字符串 `image_url/image_b64/cookie/ua`。图片、data URL、base64 后备值、状态/网络失败、JSON 原值及有限源错误类别按固定源行为组合实现。调用撤销、取消、跨实例与冷启边界有实际门禁。保留已交 qB 路由和原 IYUU 实现，无新增 UI 依赖。

`OCR_HOST` 在启动时取快照；插件请求不能覆写端点、代理或 TLS。按用户明确选择，**仅 OCR 私有客户端不校验证书**；全局、普通请求、Git、浏览器设置不变。图片凭据仍受规范来源与跨源保护，OCR POST 不复用图片 Cookie/UA。关闭证书校验有服务器冒充风险。

## 实际证据

- **原 helper 纯组件**：固定源码 AST 与明示受控 response，31 helper + 10 status + 11 extended，共 52 条。它们不是 RequestUtils 网络运行。
- **源 POST 准备层**：固定 requests 纯方法 AST 实际运行两个 payload，核 JSON 字节、长度、header 合并。使用明确控制的默认 headers/stdlib JSON；未导入 requests/urllib3、未发送 HTTP，不冒充完整原环境。
- **历史源 HTTP 交集**：仅继承已提交且源 SHA/调用组成匹配的具体 GET、空 POST、响应解码等记录；不继承失踪 S15 日志。
- **新 Go normal**：SDK/core 小门与受影响修正；transport 5 个 OCR + 7 个既有邻接根；App 7 个 OCR 根及既有 qB 邻接 1 根。
- **新真实宿主/SDK worker**：正常 CLI 两宿主 `24 → 52`、已安装 worker `40 → 66`；5 次 OCR 调用为成功→typed JSON 错误→网络空值→恢复→冷启成功；只安装一次，两宿主自然 Wait 0。401/405 不触发 OCR。
- **新 race**：transport 3 根、App 与 race-built worker 3 根。详见 `reviews/final-seal.json` 与 `reviews/FINAL-REVIEW.md`，不以独立 PASS 字样代替输入绑定。

每组保留实际命令、必要原始日志、程序 hash 与一份完整原始 input-map。相同阶段映射的原 SHA 在 publication manifest 中列明，未将摘要冒充原始文件。历史 driver 中绝对路径是当时执行位置；复制路径或重新执行前需复核实际环境和权限。

## 保留的失败与未声称覆盖

- `s15-transport01` 因 runner 的 `go list` 缺 `-buildvcs=false` 在编译前失败；原 driver/stderr/退出码保留。修正 runner 不改产品来迎合测试。
- 源准备 `execution01` 在 Protocol 构造处因受限 builtin 缺 `__import__` 失败，capture 为空；原件和窄修正保留。`execution02` 的成功仅证明选定纯准备层。
- 新完整 Python RequestUtils HTTP、精确深 JSON 递归阈值、未实证的 HTTPS 代理两跳等仍是边界，不因此成为“已批准差异”。它们不被上述普通 helper 的有限验收扩写为全 S15 等价。
- 一次此前版本查询的 import 内部 IPv6 副作用结果未知；不宣称发生、成功或被拒。之后不再导入或探测，原事件记录保留。
- 本片不是 OCR 引擎/识别率、AutoSignIn 业务、S14 浏览器、S15 全依赖或全部 61 类完成。S15 父项不因本子片而整体 verified。无 CI 运行就没有 CI 绿色声明。
- 官方 `check_ledger.py` 资源本轮不可读取；本地结构、路径、hash 检查与官方 `--final` 门明确区分。

发布只包含精确源码/测试/样本、必要文本证据与唯一 ledger 增量；不含 private-data、数据库、凭据、binary、依赖、缓存或其他失踪候选。父提交保持 `42d6`，目标分支仍为 `plugins-refactor`；不改 `main`、旧 dirty 树或 NAS。本地归档不等于异地备份。
