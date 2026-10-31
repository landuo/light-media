# S10 原八方法与必要保留对象链：有限后台片

发布基线为 `11f922896377cb0d02bf481ef0df10f11a1cb4c4`。目标为原六个 Manager 方法及两个 Scheduler 方法，不把旧三查询重复计入：init_plugin、remove_plugin、delete_plugin_config、delete_plugin_data、get_plugin_state、get_local_plugins、remove_plugin_job、update_plugin_job。包含为保持原绑定对象行为所需的调用/提交/job/已注册 API 引用与终态机制；没有新增 UI 页面。

本次重新实现的源码为39个Go文件及2个原源JSON，不是失踪旧S10候选或旧日志“找回”。源码逐路径绑定见 publication-manifest；历史完成信用与既有S01/S15交付保持，不把该片扩大成全部61类完成。

## 实际证据及其层级

- 固定原源8a83：选定不变AST方法体、真实SQLite/原ORM及Oper、Plugin/Pydantic、未启动APScheduler pending jobs；warm/cold为两个进程。构造/事件/业务回调采用明确owned输入。不是完整原宿主、真实插件、真实网络或timer firing。
- SDK actual-drain normal4/race4；host-only物理终止normal7/race3。后者normal包括旧2个self-test子进程根，不能全说成同一种独立worker。intentional物理join不等于child exit0，非claimant pending不是完成收据。
- 当前App正常门：normal01真实22根PASS+1根cwd错误FAIL，原失败不变；normal02仅纠正工作目录、复用已绑定程序跑该1根PASS。组合为23根，不伪写成normal01全部PASS。当前race23实际通过。
- 正常服务CLI：同一隔离DataDir、一次安装、两个normal宿主24→46均自然Wait0；24次业务query，失败/恢复/冷启以及授权/边界按实际raw记录。无环境proxy清洗，不接真实下载器/NAS或真实插件数据。

独立 App+CLI 总审已接受本片原八方法约定范围，未留实质阻断，见 `reviews/integrated/INTEGRATED-APP-CLI-FINAL-REVIEW.md` 与配套 JSON。独审与源码/原始证据分别绑定。当前验收通过不代表远端已推送；以本提交的交付回执和后续远端读回为准。

## 失败、可复核性及不包含内容

保留原C编译/断言失败及其窄修正、normal01 cwd失败与normal02补跑；不删除失败来“绿化”。本片不声称完整源构造/所有任意第三方SDK/浏览器或全部61类验收；没有CI运行则没有CI绿色声明。

每个必要当前门保留原命令、原日志/结果、一个完整实际输入map、生成测试源码及程序SHA。重复go-list大正文、重复相同阶段map保留原SHA/字节说明；差异阶段不能伪称相同。原driver/map中的绝对路径是当时执行位置，不是便携脚本保证。重放前按manifest将项目路径映射到本提交、复核Go工具链/模块校验与generated-source，并满足独立环境/权限；不要全局字符串改写原始证据或自动执行历史外连/登录操作。

不包含 binary、数据库/private-data、secret.key、账号/登录Cookie或Header、凭据、依赖/venv/cache、完整环境值、整个audit目录或旧WIP包。原始敏感内容如需脱敏，须将衍生副本明确命名并绑定原件和副本SHA，不能把改写副本说成原始日志。

目标仍为plugins-refactor；父提交11f922，不修改main、旧dirty树或NAS。本地commit/bundle不等于已经推送；最终交付状态以实际远端读回为准。

## Linux 复现入口

Go 1.26.0、CGO 和 go.mod/go.sum 对应依赖就绪后，在独立本地测试环境执行 `bash testdata/plugins/shared-manager-lifecycle-reconstruction-20261031/reproduce-app.sh`；加参数 `race` 运行同一 23 根和六个 race-built owned worker。脚本只构建本仓库自建夹具，保持 proxy/CA 环境，不访问真实站点/下载器/NAS，使用测试各自临时数据库及 owned loopback 服务；产生的程序目录会打印，不自动清除。它是便携复现入口，未冒充本文历史运行命令。

双正常宿主的实际 driver/argv/40 份非登录 raw body 在 drivers/normal-cli-driver.py 与 raw/normal-cli01。该 driver 依赖当时恢复目录布局、精确 39123 授权与当前构建 SHA；保留原字节作为证据，不在另一机器自动执行或替换代理。源采集脚本是开发证据工具，不是生产 Python 运行依赖。
