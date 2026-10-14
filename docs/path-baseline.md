# M0 NAS 路径、下载器映射与容器硬链接基线

2026-10-15 对现有配置做只读核对，并在两个已授权目录之间执行小范围路径探针。没有修改下载器或飞牛配置，没有扫描、读取或改写现有媒体；探针只创建自己的随机小文件，验证后清理。该结果支持列明路径的 M0 可行性，不等于完整整理流程已完成。

## 三方路径与建议应用映射

| 用途 | NAS 宿主路径 | 下载器所见 | 飞牛/建议新应用所见 | 证据与限制 |
| --- | --- | --- | --- | --- |
| qB 可用下载挂载 | `/vol2/1000/media/download` | 现有 `qb` 容器 `/media/download` | 建议应用 `/media/download` | Docker `Mounts` 确认 bind、RW；本次未把它认定为 qB 默认 save_path |
| Transmission 默认下载目录 | `/vol2/1000/media/download/电视剧` | 原生进程同一宿主路径 | 建议应用 `/media/download/电视剧` | 只读取运行进程的 config-dir 和 settings 中路径字段，确认默认 download-dir；目录当前可读写，容器内单独测试 |
| 国漫目标库 | `/vol2/1000/media/国漫` | 下载器无需把目标库作为源数据目录 | 飞牛库目录为宿主该路径；建议应用 `/media/国漫` | 飞牛只读库目录证据与 NAS 探针目标一致，未改变库设置 |
| qB 另一下载挂载 | `/vol2/1000/docker/qbittorrent/downloads` | 现有 `qb` 容器 `/downloads` | 本轮建议应用未挂载这个目录 | 明确独立于 `/media/download`，未做硬链接/整理测试，不能自动重写为已测试媒体路径 |

建议新应用仅在明确选定的映射下使用宿主 `/vol2/1000/media` → 应用 `/media`，而不是分别挂载来源与目标为不同 bind mount 后假定 `link` 一定成功。当前一次性测试容器已经实际验证整个父目录 bind 的布局；生产应用的挂载与下载提交路径仍须按该契约显式配置，不能把建议布局说成已部署服务。

Transmission 原生路径到建议应用路径应以完整目录边界映射：`/vol2/1000/media/download/...` → `/media/download/...`，拒绝不属于授权根的路径。qB 若任务实际使用 `/downloads` 或其他挂载，要另配置且验证对应的实例映射；不能仅按名字相似进行替换。

## 只读配置核对

- SSH 复用现有有效密钥。qB 只读取 `docker inspect` 的 `Mounts` 字段，未输出环境变量、完整配置或服务凭据；保存摘要只保留 `/media/download` 和 `/downloads` 相关挂载。
- Transmission 通过进程参数只取 config-dir，确认原生配置位于 `/vol2/@appdata/transmission`。用 `jq` 只选取 download-dir、incomplete-dir 和 incomplete-dir-enabled；未输出其他 settings 字段。incomplete-dir 未启用，本轮不测试它。
- `findmnt` 确认授权来源与目标都处于 `/vol2` 的 ext4 挂载。当前文件系统与目录结论不推广到别的卷、挂载或将来配置。
- 飞牛已验证的国漫库目录为 `/vol2/1000/media/国漫/`，完整只读库摘要在 `docs/m0-evidence.json`。该路径核对本身不证明新 NFO 已读取、演员字段兼容或扫描完成。

## 容器内实际探针

只使用 NAS 已存在的 `alpine:3.22`，image ID 为 `sha256:c83674e1999044d33d751661371b873539f47e5b5c5ca3320c7e0377acca6238`，`--pull never`。测试容器使用：

- `--rm --network none --read-only`；UID 1000 / GID 1001；`--cap-drop ALL --security-opt no-new-privileges`。
- 现有已上传静态 pathcheck 二进制只读挂载为 `/pathcheck`。
- 宿主 `/vol2/1000/media` 作为一个 bind mount 映射到 `/media`，写权限仅供已授权随机文件实验；命令不遍历媒体目录。

两组探针均只运行一次：

| 来源 → 目标 | 结果 |
| --- | --- |
| `/media/download` → `/media/国漫` | passed |
| `/media/download/电视剧` → `/media/国漫` | passed |

两组都确认实际 `link` 成功、`os.SameFile` 身份相同、源内容保持、目标改名与解除链接后源保持、自身测试文件清理成功。容器已自动删除；复用的 NAS staging 二进制目录由主任务统一清理，没有删除其他步骤的临时文件。

原始结构化结果为 `work/path-container-result.json` 和 `work/path-container-transmission-result.json`；只读路径/挂载/运行身份摘要为 `work/path-readonly-mapping.json`。这些只包含路径与有限测试结果，不保存 Cookie、密码、Token、完整 settings 或视频内容。

## 尚未由这次探针证明的边界

该探针验证了当前 UID/GID、bind 布局及指定目录。生产应用的 UID/GID、容器挂载、下载器某个任务实际 save_path 或其他目录不同，都需要重新核对；仅有 Docker Mounts 不证明默认下载路径或全部已有种子路径。没有改变 qB 的 `/downloads` 挂载，没有验证从那个独立目录整理到媒体库，也没有在现有种子上进行迁移/辅种/校验操作。

硬链接共享文件内容，媒体库一侧不得原地改写视频。本实验不验证复制中断恢复、挂载掉线、空间不足、并发符号链接替换、完整整理状态机、源数据引用管理或 NFO 入库；这些仍按设计阶段分别验收。
