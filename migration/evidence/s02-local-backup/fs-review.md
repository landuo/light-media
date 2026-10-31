# S02 B2 实际 FS 组件：有限接受

**接受已执行的 B2 文件 IO 组件范围。** 独立读回 normal2 / race1 原始 test2json、命令退出、输入表、生成文本副本和两程序 SHA，无失败、skip 或 DATA RACE。没有新增测试。

- `evidence/s02-fs01`：normal 两根 PASS，6,200 个输入三阶段一致，20:20:49 UTC 终态。程序 `bf41bc7a…`。
- `evidence/s02-fs-race01`：仅同 hash 复用/并发根 PASS，6,209 个输入三阶段一致，20:21:39 UTC 终态。程序 `620f4390…`。go-list/test-c 均带 `-race`。
- 两门原绑定输入并集 6,220 路径逐当前 SHA/bytes 核回零漂移；原 candidate 产品/测试同已静审冻结值。后来新增 C 文件不在历史输入表，不计 B2 篡改，也不因此被 B2 通过。
- 运行期原 oracle JSON 与仅作静态出处的 generator 已明确额外绑定，不误称 go-list 自含；生成 testmain 副本逐字节核同。

实际证明：真实 App/SQLite、自有 AllowedRoots/library 目录，三项旧源材料交集、TR 原样读取、备份不改写、确定 hash 临时文件截断复用及两个不同材料同 hash 并发各读回自己的 bytes；完成后仅保留一个材料文件且 path-lock registry 释放。原 oracle 为历史材料投影，不扩大为真实下载器或完整原业务执行。

限制保持：scope.validate 为组件提供的受控端口，未通过真实 worker/CLI 验证 capability admission；first authorityErr、private/no-follow 等拒绝仅静核，未补权限/inode/symlink-escape 探针；16 MiB 是明确目标资源边界。临时材料不随 phase close 删除，年龄 cleanup/Close 尚不在本片，不能关闭完整 S02。

runner 继承环境，仅设置 GOMAXPROCS/CGO_ENABLED；其 environment_proxy_unchanged 是代码声明，不冒称另有全环境动态比对。每门三次命令前资源样本均过底线，不宣称连续/末态资源监控。原工具终态来自协调者消息，独审实际核原 result/raw/exit；不访问 sessions、不重运行。

详见 `b2-runtime-independent.json`。本轮仅读/hash与本审计写入，无 Go、网络、源 import、FS 探针或产品/ledger 修改。
