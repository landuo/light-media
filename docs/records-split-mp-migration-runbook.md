# records 拆分迁移 Runbook（NAS 停机执行）

对象：把 v1 的 `records(kind,id,payload,updated_at)` 迁移到 v2 功能表。
工具：`cmd/records-migrate-check`（`--data-dir` 干跑，`--apply` 执行）。
前置：**服务必须停止**；工具的实例锁会拒绝运行中的实例。

---

## 0. 迁移前确认

1. 目标数据目录包含 `light-media.db`、`secret.key`、`instance.lock`。
2. 没有进程持有实例锁（`/config/instance.lock`）。
3. 磁盘剩余空间 ≥ 数据库大小的 3 倍（迁移会复制数据并保留冻结表）。

## 1. 备份

```bash
cd /path/to/compose
docker compose stop light-media
cp -a config/light-media.db  config/light-media.db.pre-v2
cp -a config/secret.key      config/secret.key.pre-v2
docker inspect light-media --format '{{.Config.Image}}' > image.pre-v2.txt
```

- 停服务后再复制，避免 WAL 未 checkpoint 的中间态。
- 若目录存在 `light-media.db-wal`，确认其大小为 0 或停服务后已被合并。

## 2. 干跑预检（只读）

```bash
records-migrate-check --data-dir /config
```

输出 `source_rows`、每张目标表的行数、`unknown_kinds`（会进入 `plugin_record`）。
把输出保存到迁移记录里；`unknown_kinds` 必须逐项确认是插件自定义 kind，而不是本应用的功能 kind。

## 3. 执行迁移

```bash
time records-migrate-check --data-dir /config --apply
```

成功标志：`version=2`、`source_rows=0`、`tables={}`。

参考量级（本地 24,200 行合成库）：迁移 5.1 秒，数据库 6.6 MB → 21 MB（含冻结表）。

## 4. 迁移后校验

```bash
sqlite3 /config/light-media.db <<'SQL'
SELECT MAX(version) FROM migrations;                                  -- 2
SELECT COUNT(*) FROM sqlite_master WHERE name='records';              -- 0
SELECT COUNT(*) FROM records_frozen_v1;                               -- 等于预检 source_rows
SELECT COUNT(*) FROM subscribe;
SELECT COUNT(*) FROM downloadhistory;
SELECT COUNT(*) FROM import_plan;
SELECT COUNT(*) FROM library_asset;
SELECT COUNT(*) FROM managed_file;
SELECT COUNT(*) FROM message;
SELECT COUNT(*) FROM original_subscription_aliases;                  -- 等于订阅数
SQL
```

抽样核对（与预检报告比对）：

- `subscribe` 行数 = 预检 `subscription` 行数；
- `downloadhistory` 行数 = `intent` + `native_download` + `download_added`；
- `import_plan_file` = 计划文件总数；`import_receipt_member` = 回执目录成员总数；
- `plugin_record` 行数 = 预检 `archived_rows`。

## 5. 启动与页面验证

```bash
docker compose up -d light-media
```

按顺序验证：订阅列表与编辑、下载管理（开始/暂停/删除）、整理历史（预览/立即整理/四范围删除）、
站点管理（列表/统计/图标）、通知中心（未读、已读）、文件管理（浏览/整理）。

服务日志中不应出现 `no such table`、`no such column`、`malformed JSON`。

## 6. 回滚

```bash
docker compose stop light-media
cp -a config/light-media.db.pre-v2 config/light-media.db
cp -a config/secret.key.pre-v2     config/secret.key
docker compose up -d light-media   # 使用旧镜像 tag（见 image.pre-v2.txt）
```

注意：

- 回滚到旧版本会丢失迁移后新写入的数据（回滚窗口内）。
- `records_frozen_v1` 保留一个版本，用于抽样核对；下一版本发布时删除。
- 未经 `--apply` 的旧库仍可直接被旧二进制启动。

## 7. 迁移幂等性

- `--apply` 可重复执行：`version=2` 后直接跳过，目标表按主键 upsert。
- 子表（`import_plan_file`、`import_receipt_member`）在迁移与每次 `Put` 时重建，不会重复累积。
