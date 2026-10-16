# Rust 动漫参考数据

`release-meta-rust-anitomy-oracle.json.gz` 的期望值来自固定的 `anitomy-pure` 0.1.0 源码实际执行，包含 10,103 个输入的全部有序最终元素或源错误，其中 779 个输入还保留各阶段有序元素及每个子 token 的 Found/Unknown。压缩只减少重复数据，测试无需 Rust、Python、网络或 NAS。

源码包的 Cargo 校验和是 `33f525032668d2aff5dff115958157db7aecf1dc2fd5f5df93cf1be1452dfd4a`；每个源文件的 SHA、Rust 编译器版本和 Unicode 版本保存在 fixture 中。输入包括原库公开测试字面量、已有安装版参考里的合成标题、185 个关键词的不同上下文、集数/资源/括号组合、显式 builder 选项和固定种子的合成语法。不存在用户真实文件、配置或凭据。

`generate.py --source /path/to/anitomy-pure-0.1.0` 是离线重建入口，必须使用 fixture 记录的编译器。脚本拒绝不同源码 SHA 或编译器，在 `work/_releasemeta-rust-reference/` 创建可丢弃的插桩副本。插桩只增加只读阶段快照和有序元素读取，成功输入再调用未改动的原 `Parser::parse` 并断言最终结果完全相同。提交的 fixture 不依赖这个工作目录。

`generate_unicode.py` 通过 `unicode.rs` 遍历同一 Rust stdlib 的所有 Unicode 标量，生成三个属性范围与大小写映射，输出到正式 `rustanime/unicode_data.go`。重建后须运行 gofmt，再由统一测试验证。当前表是 Unicode 17；安装 wheel 的编译器已通过二进制标记核实为 Rust 1.97.1（提交 `8bab26f4f68e0e26f0bb7960be334d5b520ea452`），该版本的精确官方原表经全部标量逐项比较后与正式 Go 表零差异。Go 运行时不加载 Rust。

`release-meta-rust-anime-wrapper-oracle.json` 是另一个参考：在实际安装的 MoviePilot 容器中只读 AST 隔离执行 `MetaInfo`、`rust_accel` 和元数据类，使用合成设置及已安装 `moviepilot-rust` 0.2.6，保存 139 个动漫输入，包括 12 个 nullable 制作组表达式的 Some("")/None 消费差异和 39 个实际安装 wheel 的新 Unicode token、名称与发布组参考。它同时断言名称、类型、季集与资源字段，以覆盖 prepared/original/name 重试间的依赖。源 SHA 和三方版本在该 JSON 中。它没有调用应用初始化、数据库、下载器或文件写入。

许可证：翻译的 anitomy-pure 代码保留 `internal/releasemeta/rustanime/LICENSE.md` 的 MPL-2.0；Unicode 数据许可保留在同目录 `UNICODE-LICENSE.txt`。

`release-meta-rust-unicode-provenance.json` 保存扩展 SHA、编译器提交、官方源 URL/SHA、全部 1,112,064 标量的验证范围和行流摘要。运行 `python3 testdata/plugins/releasemeta-rust-reference/verify_installed_unicode.py --fetch` 可将三个固定公共源码下载到工作目录（核验 SHA），离线编译只读查询器，并将实际 Go 范围/映射逐标量对照。已有下载时可省略 `--fetch` 或指定 `--source-dir`；输出及二进制只进入 `work/`，不修改 Go 表、原安装文件或服务。此工具不会导入应用或读取凭据。
