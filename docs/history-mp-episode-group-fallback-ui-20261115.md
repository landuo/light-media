# 第七轮：手动整理剧集组与自动推荐界面

## 范围与依据

本子任务只改[共用手动整理弹窗](../web/src/components/ReorganizeDialog.vue)、[对应SFC交互测试](../web/tests/reorganize-dialog.test.mjs)，并按父任务扩展许可更新[规则设置帮助文案](../web/src/components/EpisodeFormatRuleSettings.vue)及[对应测试](../web/tests/episode-format-rules.test.mjs)。没有修改Go、共享导入核心或旧项目，也没有操作浏览器、NAS、下载器。

只读对照 MoviePilot-Frontend HEAD `2829e3b08214348f05ff2f5beae644882eccad58` 的 [ReorganizeDialog.vue](../../MoviePilot-Frontend/src/components/dialog/ReorganizeDialog.vue)：

- 164–264：剧集组选项、默认null、名称与季数/集数副标题。
- 390–419：TMDB编号改变清空旧选择、400ms防抖；非电视剧清空组。
- 1413–1449：原版VSelect、默认组、clearable、loading、桌面半行剧集组与各四分行季/集；移动端单列。
- 756–779、838–865：推荐按钮可用性不依赖规则表；读取的是表长度；空表点击提醒，非空全disabled允许请求。
- 868–910：成功只更新episode_format并记录返回提示；失败保留手工输入。

## 剧集组实际接线

- 仅`tv`且TMDB编号为有效正整数时启用剧集组选择。
- 输入编号400ms后请求 `GET /api/catalog/episode-groups?id=TMDBID`，使用公共API客户端解包后的数组 `{id,name,group_count,episode_count}[]`。
- 默认项为“不指定剧集组 / 使用 TMDB 默认季集排序”，`value=null`；实际选项保存ID，显示名称和“3 季 • 24 集”等副标题。
- 预览、立即整理、加入队列共用payload。TV请求携带`episode_group: null | string`，其他媒体类型不携带此字段；清空选择提交null，由后端解为空字符串。
- TMDB编号/媒体类型变化只清空剧集组选项与选择；取消旧请求、清理400ms定时器，以代次和当前ID双重核对结果，旧请求的success/error/finally均不改变新ID状态。
- 失败在该控件的hint/error显示真实错误，默认组仍可用于预览/整理。卸载时中止请求、清理定时器。
- 不从`initial.episode_group`自动继承历史组；已有`from_history`默认false及后端显式继承行为保持不变。未联动目的路径、目的存储、整理方式、刮削或分类开关。

## 自动推荐与原版前后端差异

[后端自动推导报告](history-mp-episode-auto-recommend-20261115.md)中的默认fallback允许直接向API提交空规则配置；原版前端却在规则表长度为0时点击toast返回。本轮保留此**已核实的原版不一致**，没有自行将空表UI改成自动请求：

| 状态 | 按钮与请求 |
|---|---|
| 选择无效或整理/预览/推荐处理中 | 禁用 |
| 规则表已读取且为空 | 按钮可点；toast提示先在设置规则页填写；不发POST |
| 规则表非空、全部disabled | 按钮可点；POST推荐，后端执行自动fallback |
| 规则表非空、有启用规则 | POST推荐；后端规则优先，未命中或不兼容再auto |
| 配置状态尚未知/读取失败 | 不伪装成空表；保持可请求，由后端返回实际结果 |

自动成功仍**只更新episode_format**。紧凑的原有输入hint展示返回的规则/智能分析名称、样本、confidence高低、majority_count/sample_count、体积放宽、原生验证/回退计数与原版message。手工修改定位后不再展示旧模板的成功说明；错误保留当前定位和其他表单字段。

已删除设置中的“自动推导尚未实现”旧帮助，改为准确说明规则优先、未命中智能分析、空表提醒与全停用规则仍可自动分析。原设置CRUD/保存/拖动逻辑未改。

先前父任务验证的tooltip局部`width: max-content`修复保持原样，不全局屏蔽ResizeObserver错误。

## 交互测试与验证

测试执行真实编译后的Vue SFC事件处理器、computed及watch；Vuetify组件和网络被替身隔离，**不是浏览器或实际TMDB网络验收**。

新增4组交互测试：

1. 400ms防抖后的真实GET路径/方法、默认与实际选项副标题；选择/清空只改变请求中的episode_group；历史默认不自动继承。
2. ID切换立即清空/中止旧读，旧响应不覆盖新ID或清除新loading；无效ID不请求，切电影隐藏且不发送组，切回TV保持默认。
3. 组读取错误保持目的路径与默认payload，卸载中止请求并隔离迟到结果。
4. 非空全disabled规则表进入fallback；显示原版message/样本/低置信度/多数派/原生计数；只改episode_format，选中剧集组、路径、方式和开关保持，自动冲突错误保留手工草稿。

原有空配置测试已纠正为“按钮可点击但toast且不发请求”；其余规则/推荐/独立表单/预览/后台任务测试保留。规则帮助文本也有断言。

最终验证：

- 相关SFC测试 **24/24** 通过：[日志](../work/episode-group-ui-tests.log)。
- `npm run check` 通过：[日志](../work/episode-group-ui-check.log)。
- 全量 `npm test` **635/635** 通过，0失败：[日志](../work/episode-group-ui-full-tests.log)。
- `npm run build` 通过，生产资源与新增MDI图标已构建：[日志](../work/episode-group-ui-build.log)。保留Vite既有JSON import/native configLoader与chunk-size提示。
- `git diff --check` 通过。本轮3个后台job全部收集，没有新建服务进程或临时二进制。

真实GUI由父任务后续独占验证，本报告不代替该验收。
