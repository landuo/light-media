# 第十九阶段：已有普通目录成员级合并与 copy/link actor 收据

重新 fetch 后继续原工作树，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版只读，无提交、推送、部署或生产媒体/下载器操作。

## 原版分支依据与本轮开放边界

[只读审计](<../work/history-mp-alignment-20261115/directory-existing-target-next.md>)确认 MP TransHandler 目录递归直调存储命令，不进入普通文件 `never/always/size/latest`、覆盖事件或 latest 邻居清理。已有 root 的 get_folder 不清空无关文件；copy/link 的物理命令可以替换普通同名目标，softlink fallback 对占用报失败。

因此本轮从**真实、尚未被整根资产管理的普通既有目录**开始，只开放 native copy/hardlink 合并。已有 managed 根的跨资产成员转移、shared-root move/softlink、外部高层 transfer 合并仍保留原审查/未实现边界，不以普通 V3 策略或已有新根路径冒充完成。

## 物理发布与所有权

[成员发布](<../internal/app/import_directory_merge.go>)沿原目录 publisher/顺序/FileItem：冻结既有树观察值，保留无关条目；每个实际碰撞叶项单独冻结父 inode、旧 entry 和确定备份名，精确 quarantine 后复用原 publishImport 发布新 entry。没有搬走整个旧目录，也没有穿透旧硬链接/软链接写 referent。旧备份只在真正提交后清理，成功前缀保留，已完成成员不重发。

[共享目录证明](<../internal/app/import_shared_directory.go>)把观察与拥有分开：根/实际使用父目录只表示 inode 身份，Members 只含本次真实发布叶项，CreatedDirectories 只含本次确实新建的父容器。聚合大小/digest 只来自 owned 子集；无关 NFO、其他版本、既有子目录不会被扫描认领。发布完成后无关文件的合法变化不使本资产失效，但已拥有叶项变化仍失败。

member-scoped root 不取得整根删除权限。集合级 metadata、history/asset delete、reorganize、库存/presence 均须携带该证明；删除仅逐已拥有 leaf 走既有链，原 root/既有父目录保留，新建父容器仅真实为空才清理。

[实际合并及生命周期联测](<../work/history-mp-alignment-20261115/shared-lifecycle-sdk-r4.log>) **18.149s** 通过，10个真实业务样本＋1个子集快照单测：

- copy、hardlink、copy首个真实成员发布后取消/App冷恢复：原 root inode不换，300字节旧叶可被12字节源按目录命令替换，即使规则为never；独立SDK观察普通覆盖事件为0、每次执行仅一次root intercept。
- 原 Source快照/字节、external.nfo/无关其他版本及空目录保持；24字节仅是本次owned成员，STREAM=12，SDK file_list_new只有3个本次叶项。
- history目标/两端、asset hardlink、已删除结果冷恢复仅删已拥有成员，不给delete_file provider传shared根；每个实际成员恰好一次调用，冷恢复新worker不重调已完成删除。
- 实际导入Scraping=true自然生成完整metadata事件及scrape job，真实NFO写入后SQL失败；重开App后journal完成proof原子重基准，owner/receipt一致；原外来NFO inode保持，外来NFO不入owned，删除只清新NFO及已发布成员。
- 历史重整保留原root与无关文件；quality/ordinary completion库存用已拥有子集，无关NFO后续变化不使资产失效，已拥有leaf变更仍失败。

首次测试遇到的是样本配置/驱动口径：movie root与all目录规则冲突、复用已初始化管理员时重复setup、以及直接enqueue旧scrape缺实际metadata事件。已分别用movie规则、真实登录Cookie安装观测器、真实导入自然生成scrape任务修正，未把HTTP200或伪造成功记录计为业务通过。[扩大整合](<../work/history-mp-alignment-20261115/round19-integration.log>) **303.482s** 通过，包含本轮与此前整理历史/原生与存储actor/外部/目录/软链/删除/元数据/库存/V3业务回归。全仓仅编译、Linux/amd64测试交叉编译和diff检查通过；未执行Linux二进制，交叉产物已清理。[最后两项真实FS机制单测](<../work/history-mp-alignment-20261115/round19-empty-directory-final.log>)另外复核通过 **1.227s**，区分新建空目录rmdir、非空保留及既有root/空dir保留，不把机制样本扩称新publisher验收。[结构化证据](<../testdata/history-mp-alignment-20261115/directory-merge-storage-publication-evidence.json>)。

## source storage copy/link 调用封存

[actor 发布](<../internal/app/import_storage_copy_link.go>)补齐此前同步调用缺少的 Started/Returned 事实，沿同一 child receipt 加 StoragePublication，而非新公共平台。

- 调用前保存 Source instance/key、方式、父 inode、封存完整 FileItem；copy 冻结真实源摘要。
- 真实返回封存后绑定当时实际 stage 的 entry；后续 staged/publication 确认与实际返回分开。
- copy 既核源与目标内容，也检查不是同 inode 的硬链接；hardlink 精确检查源 inode/快照。两种方式的保真证据独立，不以相同大小冒充。
- 缺已封存结果不重调、不 native fallback；已封存结果即使 staged 快照尚未确认，普通与目录 guard 都能认出该精确 stage，冷恢复复用原 inode/权限/mtime。
- 原逻辑 Path/NewName/私有PublicationPath/完整FileItem和 copy 属性行为保持；move/softlink 路径不变。

23 个实际 SDK 样本通过 **19.527s**（新5＋旧copy/link7、move4、普通softlink4、目录softlink3），见[actor回归](<../work/history-mp-alignment-20261115/storage-publication-regression.log>)。包括普通copy及目录hardlink在Result已封存、staged快照前暂停，App/SDK PID更换后复用stage、每成员仅一次调用；两方式互冒以及结果SQL持久失败不认领/不重调。

本轮证据仍限独占合成材料和实际本地 Go/SQLite/FS/SDK，不扩称 NAS、任意云盘或完整新下载协议验证。无前端变更或新浏览器验收，软链接选择项尚未开放；整体目标继续。
