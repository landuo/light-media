# S15：仅 JSON POST 源码准备层捕获（待静审执行）

**原版 execution01 实际 exit 1、capture01.json 为空：在原 SupportsItems 的 Protocol 泛型参数化时缺少 builtin __import__。当前最小修正版仅静态准备，尚未第二次执行，没有 body/header 运行结果，不是 HTTP 或完整源环境 oracle。** 原 script/bindings/seal/README 留在 before-fix01；execution01 原件不动。仅写本 audit，不改产品，不 Go、不网络，不 import requests/urllib3。

## 实际源与范围

- MoviePilot 固定 `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 `app/helper/ocr.py`、`app/utils/http.py`：当前 bytes 与固定 Git blob 相同。OCR 使用 `RequestUtils(content_type="application/json").post_res(..., json={"base64_img": image_b64})`；其它 header 参数默认 None。
- 只静态读取现存 oracle venv 的 requests 2.34.2 / urllib3 2.7.0 源文件，不 import 它们。12 个文件 SHA 与 25 个原样 AST 节点/行号绑定在 `input-bindings.json`；原始片段在 `selected-source.txt` 便于独审。
- 执行路径限定：受控 request-header dict → **实际 sessions.merge_setting**（CaseInsensitiveDict）→ 实际 PreparedRequest.prepare_headers / prepare_body / prepare_content_length。源 header 里的 None 必须经 merge 删除，不能直接交 prepare_headers。User-Agent=None 也会被 merge 删除；只观察该结果，不声称最终 wire User-Agent。
- 原样抽取必要 CaseInsensitiveDict、SupportsItems、校验/转换 helper 和异常类型，保留函数体/装饰器；不执行任何源文件的顶层 module code。类型注解延迟求值；typing/collections/re/json 等只来自标准库。
- `super_len` 原函数不是通用无 IO 函数，故准确限定其**本轮纯 bytes 路径**：两个 payload 都是 exact dict[str,str]，data/files 固定 None，标准库 json.dumps 产生 str，再由实际 prepare_body.encode 得到 exact bytes；super_len 只走 __len__，无 fileno/tell/seek。执行 namespace 不提供 os/io/socket/open 名称；修正版仅额外提供原始 builtins.__import__ 供 CPython typing/Generic 内部正常依赖使用，不能再声称 namespace 完全无 import 能力。25 个固定源 AST 没有 Import/ImportFrom 或直接 __import__ 调用。未删除/替换文件分支、Protocol 或函数实现，没有自写 import hook / sys.modules 假模块。
- 不抽取/执行 prepare_url、完整 PreparedRequest.prepare、Session.__init__/prepare_request/send、RequestUtils.request、OcrHelper 入口或 normalize；不导入 compat/utils/models 的 module，不执行默认 Accept-Encoding 探测。没有 probe 跳过 hook、sys.modules 替换或全局 monkeypatch。

## 两个受控输入，不扩矩阵

1. `base64_img="QQ=="`。
2. `base64_img` 为汉字、emoji、引号、反斜线、换行、tab 的合成字符串。它只检查 JSON POST 准备的 Unicode/escape 字节，**不是合法 OCR 图片、完整入口 normalization 或 OCR 服务成功的证明**。

Session headers 输入固定为协调者先前已只读观察的受控值：

```json
{"User-Agent":"python-requests/2.34.2","Accept-Encoding":"gzip, deflate","Accept":"*/*","Connection":"keep-alive"}
```

这不是再次实测源默认 headers，也不是重建旧 `br` 环境。`complexjson` 明确绑定**标准库 json**，不推断/重测完整 requests.compat 当时选择的 JSON backend。只断言 OCR 必要 Content-Type、缺 Accept、两份预先固定 JSON 字节；Content-Length 等仅作为实际源码准备层输出观察。

## 待协调者授权的唯一执行方式

修正静审通过后，由协调者第二次执行（owner 不启动，不能覆盖 execution01/capture01）：

```sh
/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/audits/repository-source-oracle/venv/bin/python -I -S -B /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/audits/s15-source-preparation/capture_source_preparation.py > /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/audits/s15-source-preparation/capture02.json
```

`-I -S -B` 隔离 Python 启动环境、跳过 site 初始化/第三方 `.pth` 并禁止写 bytecode；这不是修改网络环境/导入 hook，也不赋予任何网络能力。脚本先核全部 source/script hashes，再编译/执行选中 AST；stdout 只输出 source-preparation JSON、body hex/ASCII/SHA 和受控 headers，不创建 HTTP 会话。输出明确 `http_observed=false`、`full_source_environment=false`。实际源 HTTP 旧 26 例的可继承交集由独审另判，本脚本不替代它们。

## 持续限制（原文）

> 按实际证据记录该只读import的潜在内部副作用，不把未知写成已发生或拒绝。保持暂停受限IPv6探针和新requests import；静态源核对/已授权owned Go测试可继续。若该限制阻断最终关键等价性证明再给具体缺口，不扩大网络权限，也别因与当前实现无关的探针让整体停工。

协调者此前 default_headers 只读 import 已执行 exit 0；其内部 IPv6 bind 是否实际发生、结果如何未被观测。本准备没有重试该 import 或探针，不把未知写成已发生/被拒绝，也不经修改模块、另一路 import 或跳过 probe 来复现它。纯 AST 准备层的结论与该未观测副作用分列。
