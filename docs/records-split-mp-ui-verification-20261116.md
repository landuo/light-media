# records 拆分：浏览器实机验证记录

日期：2026-11-16
实例：本工作树构建的 `cmd/server`，`-safe` 模式，`127.0.0.1:3132`，数据目录 `/tmp/lm-ui2`
数据：本地迁移演练库（24,200 行 v1 → v2），另加 2 条整理历史与 1 个站点
截图：`work/ui-verification-20261116/`（git 忽略）

## 1. 验证路径与结果

| 步骤 | 操作 | 结果 |
| --- | --- | --- |
| 首次启动 | 空库打开 | 进入创建管理员页；`user` 表投影修复后可创建 |
| 登录后导航 | 全部 14 个路由逐页打开 | 无 `not iterable`、无 500、无控制台异常 |
| 订阅列表 | 电影 / 电视剧页签 | 300 条订阅渲染；标题、年份、进度、状态来自 `subscribe` 表 |
| 订阅编辑器 | 打开 `Title 0` | 关键词 `kw0`、质量 `1080p`、下载器 `qb`、状态 `paused` 与 payload 一致 |
| 整理历史 | 空库 + 2 条真实行 | 空态显示“没有数据”；有行时标题/路径/方式/时间/成功失败均渲染 |
| 通知中心 | 徽标 + 面板 + API | 未读 2666 与 `message WHERE read=0` 一致；列表返回 100 条 |
| 通知已读 | `POST /api/notifications/nt_2/read` | 200；`message.read` 0→1；未读 2666→2665 |
| 状态计数 | `GET /api/status` | `subscription 300`、`intent 3000`、`asset 3000`、`import_plan 600`，与 `record_kind` 过滤结果一致 |
| 站点新增 | 添加 `https://example.org` | 卡片出现；`site` 表新增 `example.org | https://example.org | 1` |

## 2. 浏览器测试发现并修复的问题

### 2.1 管理员的 MoviePilot 列无法靠 AFTER 触发器补齐

`NOT NULL constraint failed: user.name`。`AFTER INSERT` 触发器在约束检查之后执行，改不了本行。
修复：`recordInsertSQL` 在 INSERT 语句里直接投影所有列（`CASE WHEN json_valid(?2) THEN json_extract(...) END` 或 `COALESCE`），触发器只保留给“仅更新 payload”的路径。

同时修复：`ON CONFLICT` 目标不能带表名限定，改为 `ON CONFLICT("uid")`。

### 2.2 直接写 MoviePilot 表时解析不到表

`recordTableForKind("user")` 只查了自有表清单，返回空表名，生成 `INSERT INTO ""`。
修复：回退时也查 `mpTables` 与 `mpCols`。

### 2.3 空整理历史返回 `items: null`

`transferHistoryMPFiltered` 在 `total == 0` 时返回 nil slice，JSON 变成 `null`，前端报 `e.items is not iterable`。
修复：返回空切片；页面恢复为“没有数据”+分页。

## 2.4 TMDB 凭证来源与真实订阅验证

部署在 NAS 的 light-media 配置（从原 MoviePilot 迁移而来）含 32 位 TMDB API Key。经 `/tmp` 副本解密只用于本地实例，测试实例通过「设置 → 媒体识别」保存该 Key（界面显示「凭据已保存」）。

| 操作 | 结果 |
| --- | --- |
| 新增电影订阅（TMDB 438631） | 200 附异步统计依赖提示；`subscribe` 行 `沙丘 / 2021 / movie / 438631 / active` |
| 页面渲染 | 卡片显示 2021、沙丘、已下载 0 / 1、订阅中、真实 TMDB 海报 |
| 菜单暂停 | payload 与列同步为 `paused`，卡片显示已暂停 |
| 菜单恢复 | 重新拉取 TMDB 详情后 `active`，卡片恢复订阅中 |
| 新增电视剧订阅（TMDB 1399 第 1 季） | 行 `权力的游戏 / tv / season 1 / total_episode 10 / active`，卡片显示「第 1 季 已下载 0 / 10」 |

新增失败时会返回 `SUBSCRIPTION_ASYNC_STATS_DEPENDENCY`（safe 模式缺少原异步统计依赖），但订阅与通知/事件已提交，属于该模式下的已知提示。

## 3. 未能覆盖的部分

- **订阅保存/暂停/恢复**：已在配置真实 TMDB 凭据后全部通过（见 2.4）。
- **新建订阅的异步统计依赖**：`-safe` 模式缺少原插件链路，接口返回提示但数据已落库。
- **下载管理**：页面要求先配置下载器；`downloadhistory` 的读取由 `/api/status` 计数与单元测试覆盖。
- **文件管理**：浏览的是容器文件系统，与 `library_asset` 无关；资产索引由 storetest 与迁移演练覆盖。
- **插件页面**：`-safe` 模式跳过插件加载。

## 4. 复现命令

```bash
go build -o /tmp/lm-bin/server ./cmd/server
/tmp/lm-bin/server -listen 127.0.0.1:3132 -data-dir /tmp/lm-ui2 -safe
# 浏览器打开 http://127.0.0.1:3132/ ，首次创建管理员
```

数据库侧核对：

```bash
sqlite3 /tmp/lm-ui2/light-media.db \
 "SELECT COUNT(*) FROM subscribe; SELECT COUNT(*) FROM downloadhistory WHERE record_kind='intent'; \
  SELECT uid,name,url FROM site; SELECT COUNT(*) FROM message WHERE read=0;"
```
