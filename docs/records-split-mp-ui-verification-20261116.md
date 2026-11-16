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

## 3. 未能覆盖的部分

- **订阅保存/暂停**：`saveSubscription` 会重新拉取 TMDB 详情，本实例没有配置 TMDB 凭据，返回 `INVALID_MEDIA`。这是环境限制；payload → 列投影的写入路径由站点保存与通知已读两条链路覆盖。
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
