# AutoSignIn 图片依赖：纯 Go JPEG

当前正式 `imagehash.Hash` 已接入经参考验证的 8 位 Huffman JPEG 解码，覆盖 sequential baseline/extended 与 progressive，返回原生 L/RGB/CMYK 像素，再走既有 Pillow 等价缩放/灰度/哈希。没有 CGO、Python、共享 libjpeg、外部解码进程或 Go 标准 JPEG fallback。完整 AutoSignIn 的宿主、迁移和上线仍未完成，本文不表示 Tjupt 或 23 个插件已可用。

## 固定的实际来源

只读核对原机 Pillow 12.2.0、libjpeg-turbo 3.1.4.1、x86-64/SSE2/AVX2，无 JSIMD_FORCE 覆盖；`jpg=6.2` 只是 ABI。实际扩展与动态库 SHA、Image.py/JpegImagePlugin.py、依赖版本和 CPU 证据均保存在 [运行环境记录](../testdata/plugins/auto-signin-image-hash/jpeg/reference/installed-binary-provenance.json)，未读取配置或真实图片。

以 [Go 1.26.2 JPEG](https://github.com/golang/go/tree/go1.26.2/src/image/jpeg) 的 Huffman/marker/progressive 解析为基础，结合固定 [libjpeg-turbo 3.1.4.1](https://github.com/libjpeg-turbo/libjpeg-turbo/tree/9217719d3a58633923b096af4c1d50d304768a64) 和 [Pillow 12.2.0](https://github.com/python-pillow/Pillow/tree/3c41c095064200a02672d89cc5ff629eaf4b0d4f) 对照实际行为；完整校验见 [来源清单](../testdata/plugins/auto-signin-image-hash/jpeg/reference/source-checksums.json)。

纯 Go 在所有架构上模拟安装版 NAS 的整数 SIMD 语义，保留低 16 位 dequant、16 位加减回绕、32 位乘加、两次饱和打包和最终字节饱和。标量 C 的 range-table 在极端输入上与实际 x86 SIMD 不同；仅照标量公式会使 126 组极端系数中 90 组像素、86 组哈希不同，因此没有采用它冒充安装版行为。

保留 [Go BSD 许可](../internal/plugins/autosignin/imagehash/jpegport/LICENSE)、IJG/turbo 和 SIMD zlib 来源/版权/许可，翻译文件标明改动，相关完整文本在同一目录 `LIBJPEG-*` 文件。**This software is based in part on the work of the Independent JPEG Group.** 离线 Python 生成器只制造合成输入和读取参考像素，不参与 Go 运行时。

## 实现范围

- 原生 component planes，不受 `image.YCbCr` 固定采样布局限制；整数采样 1..4、水平/垂直/不同分量最大值、非交织 scan、progressive DC/AC refinement、restart blocks/rows。
- 量化表存在性和每分量第一次 SOS 的冻结、16 位 DQT，保留 late-DQT；sequential 缺表时按原 Annex K 缺省 Huffman 表及既有表选择，不能顺手补 progressive。
- 原版 RGB/YCbCr/CMYK/YCCK、JFIF/Adobe/分量标识优先级、CMYK inversion、精确整数颜色转换和 h2v1/h1v2/h2v2 fancy 边缘/交替舍入。
- 解码后完整复用已有 `Gray` / `Raw`，没有复制第二套缩放引擎或放宽原 `score > 0.9`。

`jpegport.DecodeRaster` 不注册或替换 Go 全局解码器。每次调用拥有自己的 entropy、系数、表和返回 slice；对外 `DecodeJPEG` / `Decode` / `Hash` 均使用同一受控实现。

## 资源边界

编码上限仍为 8 MiB、尺寸每边 8192、总像素 16 Mi。新增在 SOF 完整读取后、任何大 plane/系数缓冲区分配前的 64 MiB 主要缓冲估算：包括各采样分量的 padded planes、progressive 的全部潜在系数、原生输出和后续 Raw 的像素副本。超限返回明确资源错误，不先分配再失败；例如 4096×4096 RGB 即使处于一般像素上限之内，也会被拒绝。

此估算不含独立的编码输入、Go runtime 和宿主并发，因此不是进程 RSS 或整个应用 64 MiB 的保证。后续实际 AutoSignIn 宿主仍须控制同时进行的任务/图片数，不能把单次预算乘以任意工作线程后仍声称符合总内存约束。原始 Raster API 的边界和算法另见 [图片哈希移植](plugin-auto-signin-image-hash-go-port.md)。

## 原执行参考与验证

全部 expected 来自原机实际 Pillow，在内存制造图片/码流，不访问真实站点图片、签到或用户文件。新大参考使用 gzip NDJSON，每次读取一个 case；原父级 42 组 JPEG 复用同一文件，没有重复复制。

| 参考 | 组数 |
| --- | ---: |
| 原父级 JPEG | 42 |
| 质量/模式/baseline/progressive/subsampling/restart | 892 |
| 分量采样/marker/标识与异常 | 188 |
| 极端系数、DQT16 至 65535 | 126 |
| 完整 64 系数、ZRL 与饱和 | 180 |
| 表缺省/存在性/selector/late-DQT | 21 |
| 合计 | 1,449 |

其中 1,407 个成功输入，逐模式、尺寸、每个原生像素、完整缩放灰图与最终哈希均一致；42 个源异常双方均拒绝。额外覆盖 12 个并发独立 decoder、来源校验、拒绝 unsupported marker、不 fallback、在分配前拒绝超预算和超 8 MiB 输入。此前 588 raw、160 PNG/GIF、8 比较参考保持通过；Go 标准 JPEG 的 27 灰图/6 哈希差异诊断也继续保留。

迁入正式包并加入资源检查后：normal 0.858 秒、race 9.730 秒、vet、CGO 禁用 normal 0.876 秒通过；带真实合成 JPEG/PNG/GIF 种子的固定 10,000 次 fuzz 通过（0.738 秒）。没有把独立候选的结果替代正式包验证。

## NAS 隔离验证

正式父包当前构建已在 NAS 的 `linux/amd64`、`CGO_ENABLED=0`、无网络/只读容器中执行，18 个顶层测试通过、0 跳过、0 失败，退出码 0、未触发 OOM。二进制本地与 NAS SHA-256 均为 `ad8e2b0066a0dd2316863b5817a7dbc161718e2caebaab337efd6b2627ae13a9`。容器限制为 256 MiB、`GOMEMLIMIT=64MiB`；这是该样本集实际通过的运行证据，不是任意图片或整个应用的内存保证。

[完整报告](plugin-auto-signin-jpeg-nas-verification.json) 记录实际隔离参数、测试数量、清理与服务快照。自建容器和远端目录已删除，light-media / moviepilot / qb 的状态、启动时间和重启次数保持不变；没有注册插件、导入凭据或请求真实站点。

## 尚未覆盖

Arithmetic SOF9/SOF10、lossless SOF3/SOF11、differential/hierarchical 等模式明确返回 unsupported。任意损坏 entropy、错误 restart marker、libjpeg 带警告继续恢复、所有非法 progressive 排列等宽松恢复语义还未逐项移植。当前原 Pillow 插件本身只接受 8 位样本与 1/3/4 分量；这不表示所有 libjpeg 通用模式都能由原插件使用。

没有声称任意 Pillow 格式或其他版本/CPU/强制 scalar/draft/缩放选项均等价，也未通过这份纯包验证证明完整 Tjupt 在线业务可用。未验证格式明确失败，不转换成空成功结果。
