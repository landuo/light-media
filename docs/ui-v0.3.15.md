# v0.3.15 原版 MoviePilot UI 对齐

按用户要求，将现有 P0 功能对应页面的视觉基线改为 MoviePilot 原版模板与实际运行页面。旧项目只读，没有修改。

参考依据：

- `MoviePilot-Frontend/src/plugins/vuetify/theme.ts`、`src/styles/common.scss`、`src/@layouts/styles/_variables.scss`、`src/@layouts/components/VerticalNavLink.vue` 和 `src/layouts/components/DefaultLayout.vue`。
- 原版运行页只读测量：侧栏 260px、顶栏 64px、内容左右 8px，背景与侧栏均为 `#F4F5FA`，实际主色 `#8D51F9`，文字 `rgba(58,53,65,.87)`。卡片及字段使用 8px 圆角，普通卡片不添加阴影；导航使用原版紫色渐变胶囊选中项。
- 站点使用 `SiteCard.vue`、`SiteCardListView.vue`、`SiteAddEditDialog.vue`、`SiteResourceDialog.vue`；文件、历史、下载使用原版 `FileList`、`FileToolbar`、`TransferHistoryView`、`DownloadingCard`、`ReorganizeDialog`；订阅与发现使用 `SubscribeCard`、`SubscribeEditDialog`、`FullCalendarView`、`MediaCard`。下载器和目录设置参考原版对应设置、卡片和弹窗。

主要调整：

- 统一浅色页面、侧栏、胶囊搜索栏、导航分组与选中项；设置回到系统菜单。保留退出图标及用户要求移除重复页面标题的约定。
- 移除自行添加的推荐大横幅，海报采用原版比例、类型/评分角标、悬停详情和搜索/订阅图标；键盘聚焦可显示同一操作区。
- 站点恢复紧凑卡片、32px 头像、上传蓝色/下载黄色数据条、悬停及键盘操作、圆形悬浮按钮，编辑框 720px，Cookie/API 页签、Outlined 字段及左侧开关。保留 3 并发连接测试。
- 文件恢复原版白色容器、面包屑、过滤条和 64px 行；整理历史使用紧凑表格；手动整理弹窗 720px、固定头尾、小屏全屏，保留全部已确认的路径与整理选项。
- 下载和订阅采用原版图片背景、暗色遮罩、绿色进度与对应菜单；订阅列表移除自创统计大卡。日历恢复透明网格、海报事件卡与手机海报布局。
- 资源卡、彩色标签、促销条、列表以及 560px 下载确认框按原版调整；下载器/保存目录使用原版下划线字段。站内资源浏览桌面使用表格，小屏使用卡片。
- 下载器、目录和系统设置恢复原版卡片比例、弱强调条、下划线字段和页签；日志采用原版浅色表面与语义颜色。

验证：全部 97 项前端测试通过，类型检查、生产构建、格式检查、差异检查和 Linux amd64 纯 Go 编译通过。凭据扫描核对 18 个已知值，无匹配。

浏览器验证使用无生产凭据、无出站请求的本地合成实例：检查了站点卡片/编辑框、文件列表与多选手动整理入口、订阅卡片及菜单、日历、发现海报到详情、资源下载确认、下载管理、整理历史、系统/目录设置和日志。1440×1000 下侧栏/顶栏实测 260/64px、站点卡高 168px、编辑框宽 720px；390×844 下日历宽 374px、页面宽 390px，无横向溢出，站点表单按钮可见。核对期间修正了全局 details 样式遮挡订阅卡、旧日历最小宽度及下载进度宽度等样式冲突。未提交真实下载、整理、删除或站点配置。

发布资源：`index-D7wv682C.js`、`index-DPuopX4i.css`。Linux 二进制 SHA-256：`e1dc48aa19e3548ce3c58cf450bb04393419a6a77b16ddd430eda98fc7d5c187`。

发布核验：内外网鉴权状态接口均返回 v0.3.15，内外网入口使用同一新资源，外网 HTML/JS/CSS 与本地构建一致；容器 running、healthy、重启 0。发布前后 active writes/jobs 均为 0，资产 11、整理计划 19、下载意图 15、订阅 1、已有失败任务 3 的计数一致。保留上一版二进制与 compose，没有新建数据库备份。浏览器需整页刷新后加载新版资源。
