# 插件贡献的普通推荐与探索数据源

固定前端 `2829e3b08214348f05ff2f5beae644882eccad58`，只读 `pages/recommend.vue`、`pages/discover.vue`、`pages/browse.vue`、`views/discover/ExtraSourceView.vue`、`MediaCardListView.vue`。`generate_oracle.mjs` 执行固定推荐/探索追加函数、默认启用映射、Vue 3.5.44 的实际筛选 watcher 和分页参数函数，结果为 `source-oracle.json`。没有从目标实现生成预期。

对应目标为 `plugin-catalog-sources.ts`、`ExtraSourceView.vue` 和现有推荐/探索组件。native 偏好继续使用已有 ID/API 路径、探索前缀储存；UI 按原版同标题共享选择，新增额外推荐默认关闭（同名内建可继承已启用选择）。返回页面重新询问普通 source 事件，推荐按路径追加，探索持久导航按前缀去重、视窗使用原始 extra 数组，保留原版重复前缀/旧导航行为。

原版 `browse.vue` 会将包括 title 在内的全部 query 交给分页组件；过滤参数的 page 可覆盖自动页码，重复键保留数组。原始 MediaInfo 保存在 original_media，源字段和十字段去重键保留；opaque source_id 不会变为 TMDB ID，动作先经已有身份解析。未知源 HTTP(S) 图片由浏览器正常加载，不转发应用凭据，不允许 URL 用户名密码。

原版 watcher 按对象键顺序逐项恢复 truthy 默认值、清依赖，数组与深拷贝 previous 按引用比较：本样本会在 101 次后触发 Vue 原有递归更新异常。原始首次生成的未捕获失败日志保留，随后捕获并记录了实际源异常；目标完整 SFC 与该结果一致，没有将它改成另一套筛选策略。

`web/tests/plugin-catalog-sources.test.mjs` 使用实际 SFC 与 Vue watcher、真实 Axios 配置拦截器验证；API 对端为合成 adapter，明确不是浏览器/独立服务证据。真实 Linux 和浏览器门后续单独绑定版本、SDK PID、原始结果与失败恢复。所有界面目前仍归 36/37/43 增量，未凭专项或编译宣布完整迁移。
