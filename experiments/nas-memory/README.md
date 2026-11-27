# 本机 NAS 内存实验

从候选版本的仓库根目录执行。需要根模块指定的 Go 版本、Node.js 22 和 macOS 或 Linux 的 `/bin/ps`。

## 对比两个版本的 /healthz

将 `BASELINE_TREE` 设为已有基线工作树的绝对路径。复现 2026-11-23 的对照时使用干净的 `37db6931`。在同一台机器上编译两个版本，二进制和结果均写到候选工作树下新建的同一个实验目录。

```sh
set -eu
: "${BASELINE_TREE:?请先设置基线工作树的绝对路径}"
candidate=$(pwd -P)
mkdir -p "$candidate/work"
d=$(mktemp -d "$candidate/work/nas-memory.XXXXXX")
printf '实验目录：%s\n' "$d"

(cd "$BASELINE_TREE" && go build -tags modernconly -o "$d/before" ./cmd/server)
go build -tags modernconly -o "$d/after" ./cmd/server
go build -tags modernconly -o "$d/seed" ./experiments/nas-memory/seed

TMPDIR="$d" node experiments/nas-memory/run.mjs "$d/before" "$d/seed" "$d/before.json"
TMPDIR="$d" node experiments/nas-memory/run.mjs "$d/after" "$d/seed" "$d/after.json"
```

每次运行均创建独占的临时数据目录，由 `app.Bootstrap`、`app.New` 和 `Store.Put` 写入 4 个启用目录，每个目录有 210 条按确定顺序生成的合成观察记录。5 种状态各有 42 条。测试实例使用随机密码、空插件市场和临时回环端口，启用 `--safe`，关闭自动更新和启动对账，固定 `GOMAXPROCS=4`。进程退出后删除本次临时数据目录。

脚本等待子进程开始监听，随后逐次核对 `/healthz` 的 `status=ok`、`plugins_loaded=0`、4 组状态计数，以及各组最近 5 个 pending 文件的名称、状态、顺序和原因。任一断言失败时返回非零退出码。结果文件名必须是新名字。

默认先空闲 3 秒，再串行执行 4 轮请求，每轮 100 次，每次请求后等待 20 毫秒，最后空闲 20 秒。每阶段末另外执行一次校验，读取 `memory.num_gc`。两次边界读数的差包含前一次边界请求读取计数之后的开销。空闲段只采集 RSS。

每 100 毫秒左右采样一次服务子进程 RSS，每个阶段起止另采一次。JSON 包含 server 和 seed 的 SHA-256、以 KiB 记录的逐点 RSS、每阶段采样峰值、中位数、时长、GC 起止计数和成功请求数。20 秒空闲数据用于衡量测试进程的短期内存保留。

可在同一 shell 运行长程对照。每版串行执行 8 轮各 500 次请求，间隔 2 毫秒，最后空闲 125 秒。

```sh
for version in before after; do
  TMPDIR="$d" NAS_MEMORY_PROFILE=long node experiments/nas-memory/run.mjs \
    "$d/$version" "$d/seed" "$d/$version-long.json"
done
```

## 离线比较堆采样

将 `HEAP_BEFORE` 和 `HEAP_AFTER` 设为同一服务进程先后保存的两份 `/api/debug/heap` 响应 JSON 的文件路径。输出给出累计分配字节数、存活字节数增幅最大的采样调用栈。前面生成的 RSS 报告使用另一种格式。

```sh
node experiments/nas-memory/heap-delta.mjs "$HEAP_BEFORE" "$HEAP_AFTER"
```

## 测量 SQLite 页缓存

可独立从候选仓库根目录执行。工具在临时目录中创建合成 SQLite 库，校验四个读连接的扫描结果，并输出活动连接和空闲连接的页缓存字节数，退出时删除临时库。以下两组参数只改变单连接缓存上限。

```sh
go run ./experiments/nas-memory/sqlitebudget -cache-kib 2048 -idle 4
go run ./experiments/nas-memory/sqlitebudget -cache-kib 512 -idle 4
```
