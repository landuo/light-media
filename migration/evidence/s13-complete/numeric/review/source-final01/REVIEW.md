# S13 TR 数值字符串：有限实际源终审 ACCEPT

非作者只读终审通过。未重新执行 collector、原源码、Go 或网络。

## 实际终态与绑定

- 协调者工具 `cc88c0`，2026-11-01 09:51:33.552013 → 09:51:33.917694 UTC，exit 0、stderr 0；result、exit、tool-receipt 与 raw hash一致。
- stdout 36,397 B，SHA256 `1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d`。
- 14 个固定短字符串场景：**3 个 True 返回、11 个未捕获 ValueError**。只有3次 recording change_torrent调用及3次录制返回；无日志。过程PASS不意味着14次下载器修改成功。
- 当前独读113个输入、9个准备封存文件及协调preexec文件全部字节/hash一致；原manifest SHA等于实际输出绑定。manifest无预期缺失cache项，不冒称所有pyc不存在。执行标志实为 `-I -S -B`。
- 46 个文件型模块 before/after 完全一致，全部路径/hash在输入闭包中。原collector每阶段检查禁止模块，实际报告无禁止import、input_drift=[]。

## 原实现与观察真实性

独立静态重算固定源整个Transmission class AST、两完整方法AST/片段hash，并重建selected-source.txt逐字一致。原 `change_torrent` 的bool/int/float/round转换、参数顺序和try位置未改；只有该完整方法被14次调用。保留的set_torrent_tag未调用。原constructor/module/vendor均不执行，receiver仅object.__new__并注入录制trc；此为明确选定方法层，不冒完整Transmission初始化。

旧collector与新collector的ordinary、RecordingLogger、RecordingClient、original_class及CollectorBoundary AST保持一致。原方法无decorator/import；原完整方法声明被保留，类仅装这两节点，future-annotations为既有隔离构造层，不声称原类注解反射/module初始化等价。原builtin转换没有被替换成预处理器、Go规则或录制返回。

14个实际input等于冻结cases，arguments_after_source全部等于原输入。前三个有效场景分别实际生成：

| 场景 | 关键真实观测 |
|---|---|
| 下划线四设置 | upload1000、download2048、ratio26.75、idle10；原布尔标志与Python数值类型保留 |
| Unicode Nd/空白 | upload123、download42、ratio1.25；非空Unicode零得到idle mode1、limit0 |
| 指数含下划线 | ratio125.0，Python类型float，而非int |

11个错误均发生在原try之前：原ValueError class/message保留，零录制调用、零日志；无False替代。完整每案输出和类型在review.json。这里只实际观察外部调用/异常；没有采集异常前局部change_kwargs的部分内容，不据静态顺序假称其已动态逐步录制。

## 精确版本勘误

实际stdout、result和准备stdlib-bindings一致为 **CPython 3.12.14**（Clang22.1.3），不是3.12.13。`audits/s13-tr-numeric-scope01/REVIEW.md` 第12行对本轮运行时的3.12.13陈述应以本勘误取代；原文件不改。其第22行关于旧serverstatus证据的历史版本未在本审重开，不据此清零历史信用。

## 有限边界

录制client/logger只是原方法最外依赖端口；不证明实际daemon、SDK/RPC接受、真实限速/ratio修改或选择器正确。该collector没有全系统audit hook；禁止模块检查与静态封闭调用链不能冒称完整native系统调用观测。没有新Unicode全域扫描、任意Python对象、巨大/非有限数值、网络或原vendor执行。

本seal仅接受这14个有限原基准，不接受目标代码或完整S13，不重复旧23源案/既验8操作，不批准新分片push。`check_actual.py`仅JSON/hash/AST读取，review.json保存逐输入当前绑定与全部实际case投影。
