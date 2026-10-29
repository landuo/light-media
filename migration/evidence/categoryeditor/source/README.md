# 固定原源证据（非目标验收）

这里保留本次独立源审查的原始字节：完整安装插件受控回放 78 场景 / 220 步、历史 helper 的 35 组热/冷回放、输入、驱动、日志和报告。`categoryeditor-source-artifact-manifest.json` 中的 `evidence/...` 是原采集工作目录相对路径；本目录按原 basename 保存，未改写哈希或把路径伪装为新的采集。

- 插件本体固定 SHA：`24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27`。
- helper 是 2026-10-20 归档，不是当前 NAS 同源证明。
- 回放适配器/故障注入、Python 版本、真实文件操作、未覆盖的真实宿主 DB/单例/网络均在原始报告中明确记录。
- 目标标准 worker 的自动差分测试读取本目录的固定输入和 oracle；源文件、驱动、输入、结果哈希必须一致，不能重新生成预期迎合目标。

## 重放原驱动

驱动保留原有隔离目录防护，所以不直接改写本目录结果。先在**新建独占临时目录**恢复如下最小测试布局；这不是恢复旧开发候选：

```text
owned-replay/
  evidence/                       # 复制本目录 *.py、输入 JSON；所有新输出留在这里
  source-snapshot/installed-plugin-sources/categoryeditor/__init__.py
  light-media/testdata/plugins/category-editor-1.3/
    installed-category-helper.py
    reload-yaml-provenance.json
```

插件本体可取仓库 `testdata/plugins/category-editor-1.3/installed-categoryeditor.py`，**先核上述 SHA** 后仅复制为该布局的 `__init__.py`。另外两项从同目录复制。运行 `categoryeditor-source-replay.py` 使用标准 Python；helper 驱动还需显式 `--dependency-root` 指向已安装并按 `reload-yaml-provenance.json` 核对全部 33 个 Python 文件 SHA 的 ruamel.yaml 0.19.1。该依赖只用于测试，不属于 Go 产品运行时。

不得使用生产 MP 目录作为输出、不得导入当前私密配置、不得调用旧服务的 `onlyonce`。新回放的环境/路径/驱动指纹或结果变化应另存并复核，不能覆盖此固定 oracle。
