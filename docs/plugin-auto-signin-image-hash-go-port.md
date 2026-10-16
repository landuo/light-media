# AutoSignIn 的 Tjupt 图片哈希依赖

状态：纯 Go 包与离线原执行参考已验证；不是完整 AutoSignIn 插件、正式迁移或在线签到验收。正式服务未部署本批修改，原 MoviePilot 与用户站点、文件、种子均未修改。

## 来源与算法

以原安装 AutoSignIn 2.9.14 的 `sites/tjupt.py` 为依据，文件 SHA-256 为 `ed717891d30abcac798407c8889776ec79191e447b4ef7f97aee1bddb0f98bb0`。只提取 `_tohash`、`_comparehash` 的实际 AST，在原机 Pillow 12.2.0 上执行自建图片；不导入应用、不加载账户、不读取真实图片、不请求签到页面。方法 AST、原机 Image.py 与参考文件校验值见 [来源清单](../testdata/plugins/auto-signin-image-hash/source-provenance.json)。原机 Image.py 与 Pillow 官方 12.2.0 文件校验值一致。

原方法先 `resize(shape)`，再转 `L`，按行计算平均灰度，严格大于平均值记为 1。默认 shape 为 10×10；匹配调用严格使用 `score > 0.9`。比较方法仅先检查两个字符串长度是否相同，分母仍取 shape 的像素数：不额外要求输入为 100 位或仅包含二进制字符。Go 保留按 Unicode 字符比较、不同长度返回 -1，以及这些源行为。

`internal/plugins/autosignin/imagehash` 实现了 Pillow 的相关路径：bicubic 的取样中心、下采样滤波范围、归一化、22 位整数系数、每一方向分别取整和截断；P/1 模式使用 nearest；RGBA/LA 在缩放前后进行源版本的预乘和反预乘；尺寸不变直接复制；特别狭长图片沿用原版先垂直缩放的分支。RGB 到灰度、CMYK 转换和透明调色板的处理均逐像素核对，未使用一般图片库的近似缩放。

以上部分参照 Pillow 官方 [Resample.c](https://github.com/python-pillow/Pillow/blob/12.2.0/src/libImaging/Resample.c)、[Convert.c](https://github.com/python-pillow/Pillow/blob/12.2.0/src/libImaging/Convert.c)、[Image.py](https://github.com/python-pillow/Pillow/blob/12.2.0/src/PIL/Image.py) 和 [GifImagePlugin.py](https://github.com/python-pillow/Pillow/blob/12.2.0/src/PIL/GifImagePlugin.py)，保留 [Pillow 版权与许可](../internal/plugins/autosignin/imagehash/PILLOW-LICENSE.txt)。Python 脚本仅用于离线参考生成，不是新程序的运行依赖。

## 已验证接口和范围

- `Raw(Raster, shape)`：紧密排列的 1、P、L、LA、RGB、RGBA、RGBX、CMYK 原始模式字节；P 使用 256 项 RGB 调色板，1 使用未打包的 0/255 字节。
- `Gray`：暴露缩放后灰度，用来核对中间结果，避免只比较最终哈希而漏掉像素误差。
- `Hash(encoded)` / `Decode`：启用 PNG 的 1/2/4/8 位相关模式、GIF 默认加载策略的第一帧，以及经后续验证的 8 位 Huffman JPEG。保留 PNG 透明色原 RGB、GIF 透明调色板原颜色、灰度调色板导致的 L 模式、局部调色板、偏移画布及透明索引填充。GIF 的 L 模式须 bicubic，不能一律按 P 模式 nearest。JPEG 的范围、资源检查与来源见 [独立专文](plugin-auto-signin-jpeg-go-port.md)。
- `Compare` / `CompareShape`：保留原分母、字符和严格阈值语义；默认接口的分母为 100。

编码输入最多 8 MiB，原尺寸每边最多 8192、像素数最多 16 Mi，输出 shape 每边最多 1024。先核对编码尺寸再解码分配。未验证编码/模式返回明确错误；非法或超界输入不伪造哈希。范围不等于 Pillow 的全部格式、任意扩展加载策略或全部异常文字已复制。

## 参考与检查

两组压缩参考共包括：

- 588 组原始模式、缩放、透明与狭长边界；灰度每个像素和哈希均与实际 AST 执行结果比较。
- 160 组 PNG/GIF 编码图片，包含灰度 2/4 位、P 的 1/2/4 位、透明色、局部调色板、偏移首帧；比较模式、尺寸、完整灰度和哈希。
- 8 组原字符串比较，包含空串、99/101 位、Unicode、长度不同、负分母、0.90 和 0.91。
- 非法/截断编码、尺寸上限、拒绝未验证模式、并发输入不可变检查。

独立候选 normal、race、vet 均通过。3 秒时间制 fuzz 在 253,074 次后以 `context deadline exceeded` 非成功退出，不能算通过；加入有效 PNG/GIF 种子后，使用固定 10,000 次 fuzz 完整通过。正式包与 NAS 证据单独记录，不把候选运行混为正式包或生产服务验证。

正式包 normal 1.361 秒、race 5.288 秒、vet 与 CGO 禁用的 Linux/amd64 编译通过。该正式测试二进制 SHA-256 为 `97f311f67e78bd1c2c3e19a4c7a26f70e1a2d48cad68c01d7e4cb7e1db8ca401`，与 NAS 上传后校验一致。NAS 隔离容器 8 个顶层测试全部通过、0 跳过、0 失败、退出码 0、未 OOM；容器与自建临时目录已删除，原版 MoviePilot、新应用及 qB 服务的启动时间/状态/重启次数保持一致。详见 [NAS 报告](plugin-auto-signin-image-hash-nas-verification.json)。这不是生产部署或真实签到验证。

正式包同样通过带有效 PNG/GIF 种子的固定 10,000 次 fuzz（1.413 秒）；这不抹去上述时间制 fuzz 的失败记录，也不代表任意格式已验证。

## JPEG 首轮差异与后续接入

另有 42 组自建 JPEG 的真实 Pillow 解码结果用于诊断。直接改用 Go 1.26.2 标准 `image/jpeg` 时，27 组的缩放灰度不同，其中 6 组连最终哈希也不同。首轮因此明确拒绝 JPEG，未用 PNG 通过的结果扩大为 JPEG 支持。

后续纯 Go JPEG 已接入正式 Hash，1,449 组原机参考及内存预算检查见 [JPEG 专文](plugin-auto-signin-jpeg-go-port.md)。没有改原哈希阈值、没有标准库 fallback；该增量不覆盖任意 JPEG 或其他 Pillow 格式，也不表示完整 Tjupt 已可用或已迁移。

签到页面处理、答案缓存、豆瓣查询、原版提交与保存分支由 AutoSignIn 移植另行负责。本包不包含凭据、网络请求、在线图片获取、答案选择或提交操作。
