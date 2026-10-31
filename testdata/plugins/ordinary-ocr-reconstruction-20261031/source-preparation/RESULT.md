# S15 JSON POST：真实源码准备层结果

**execution02 实际 exit 0，stderr 为空，两例准备层断言通过。仅限 source preparation，不是 HTTP、OCR 服务成功或完整源环境验收。** 协调者执行 `venv/bin/python -I -S -B`；owner 仅读回，没有重跑或新 import。

## 两例实际输出

| 合成输入 | body bytes | Content-Length（观察值） | body SHA-256 |
|---|---:|---:|---|
| `base64_img="QQ=="` | 22 | 22 | `991e7fc9eaa761693f822db39b751e6e5d81159b57bf78d5be3a4e026e9c672f` |
| 汉字/emoji/引号/反斜线/换行/tab 字符串 | 50 | 50 | `0428dba62077fa4f31d2c9ec423c12574dbac019f9794bad538faaff57b82aca` |

实际 ASCII body（第二行是 JSON 转义后的 bytes，不是额外输入）：

```json
{"base64_img": "QQ=="}
{"base64_img": "\u6c49\u5b57\ud83d\ude42\"\\\n\t"}
```

两例均先执行原 `sessions.merge_setting`：RequestUtils 的 `User-Agent=None`、`Accept=None`、`referer=None` 被删除；随后原三个 prepare 方法给出 `Content-Type: application/json`，**Accept 缺失**。其余观察到的 headers 是受控输入保留的 `Accept-Encoding: gzip, deflate`、`Connection: keep-alive`，以及实际准备出的 Content-Length。这里不声明最终 wire headers。

## 来源与证据层级

- MoviePilot 固定 `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 OCR JSON dict / RequestUtils header dict；requests 2.34.2 准备方法、merge 和必要 helper 的 **25 个原样 AST 节点**；12 份输入源文件 SHA 与两份固定 MoviePilot blob 均有绑定。
- `complexjson` 使用明确受控的标准库 json，实际解释器 Python 3.12.14；不是重新发现完整 requests.compat backend。默认 Session headers 是已声明的受控输入，**不是重测默认值或重现旧 br 环境**。
- 实际输出 `capture02.json` SHA **`3637c76409663ff4e3f97e8641165a0ca4a9549a040e3e50efeeddb04d3b0dd2`**；driver SHA `9eeb228d9545e709c3bbdabd01895d8bc23292a7565791d41a90e8472074a999`；bindings SHA `3bad0d1ec2657acc830e0ebfa0830437f4df4269f28062bcf9877c9ac8e07657`。stdout 中记录的 driver/bindings/25 节点 hash 与当前文件逐项一致，body hex/ASCII/bytes/hash 互相核回。
- 没有执行 prepare_url、Session 构造/send、RequestUtils.request、完整 OcrHelper 入口或 normalization；没有网络、Go、新 requests/urllib3 package import 或 probe 跳过 hook。脚本保留前后 `requests` / `urllib3` / `socket` 不在 sys.modules 的断言；原始 `builtins.__import__` 只为正常 stdlib 泛型支持提供，不伪称 namespace 没有 import 能力。
- Unicode case 仅为 JSON POST 准备边界，不证明图片/base64 合法、完整 OCR 输入链或远端识别结果。真实 HTTP 旧 26 例的交集继承由独审另判，不能用这两例替代。

## 失败沿革保留

execution01 实际 exit 1、capture01.json 为 0 bytes；在原 Protocol 泛型参数化阶段缺少 builtin `__import__`，没有产生两例结果。原 driver/bindings/seal/README/片段已留 `before-fix01`，所有 execution01 原件逐 SHA 未变。唯一代码修复是加入原始 builtin，不改源 AST、不替换 Protocol、不造模块或 import hook。此前 default_headers import 的内部 IPv6 bind 是否发生/结果仍未观测，本次不重试或推断它。

`terminal-seal.json` 是本准备层实际结果与恢复文本绑定，**不是整体 S15 / HTTP 验收 seal**；独立审查另行记录。
