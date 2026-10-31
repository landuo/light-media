# S10 普通 Manager/Scheduler 八方法 — App + normal CLI 最终有限独审

**接受本次已定义的普通 SDK→宿主八方法切片及直接 retained job/API 生命周期接线；未发现本次可观察合同内的未解决阻断。** 依据真实源组件、当前产品静审、当前 App 组合 normal23、race23 和两次独立 normal CLI host。不是整个平台/业务插件、任意 Python 对象或完整 Python 宿主运行等价，也不是 Git 推送收据。

## 1. App 实际证据

- `s10-integrated-normal01`：真实 exit1，23 根中 **22 PASS + AsyncReplacement 1 FAIL**、无 skip。失败发生在测试开始读取 `../../testdata/standard-services/async-oracle.json`，driver 错将 package test cwd 设为 repository。文件并未遗失：current 与 HEAD 均 SHA `8c209e25…`。原 FAIL/raw/driver 保留；其 after-test 映射本来未生成，不补造。
- `normal02`：只修运行 cwd 为 internal/app，原 App程序 + 6 worker 原地复用，不改产品/测试/oracle、不重编，只该 Async 根 **PASS/exit0**。6192 before=after-test=current，包含原6181编译输入的同字节继承及明确 runtime oracle/旧收据/程序。与原22根组成恰好23根，不称原01整门变绿。
- `s10-integrated-race01`：**23/23 PASS/exit0，无skip/fail，捕获输出无 DATA RACE**。6191 before=after-compile=after-test=current 逐SHA无漂移；App与6个worker全部fresh race build，go-list/test-c/build均-race；7个当前程序及1份generated-source副本核同。App SHA `bef999b05b62ade02d70f231fca6379f7403f2cb7e7be0ea9af9a96fab9d3f87`。
- 新 empty-all/self-remove、prefix/global jobID/invalid clock继续、getter失败恢复、exact API保留/注销、实际 legacy最后owner transport-only、cancel returned≠drained、accepted unpublished调用等门均在该统一当前程序上执行，不借旧失踪代码/日志算PASS。旧 C 编译/断言失败及本次 cwd 失败仍保留。

## 2. normal CLI 实际读回

`run01` driver exit0；协调者真实 tool launch010491/session95364、terminaldaca14 exit0 的保存收据已核，明确区分“协调者保留tool观测”与新的系统进程审计。它绑定 server `972e0df2…`、worker `ba1cd0ac…`、driver `efbd53c0…`。server当前3953编译输入 before=after=current相符，worker沿1081输入编译收据与实际程序连续；不是测试binary代替普通server。

- 两个独立正常host：PID24→46，两个listener inode，均自然 Wait0；所绑定直接SDK子进程40→60，独立starttime、同已安装 executable SHA。driver完成已知worker消失检查、无forced cleanup。不能把事后PID查询倒充退出时刻见证。
- **42个精确控制器HTTP调用、40份非login原body逐SHA核回、24个业务query与其raw逐条相等**。只安装一次，owned peer `/index`、`/worker`各一次，冷启未重新取材料。无跳转/forward/CONNECT或新增真实站点；这由限定driver、实际有限路由收据与空资源配置支持，不冒全系统syscall网络审计。
- 八wrapper均实际通过已安装SDK：explicit init→一次state getter且DB仍原配置；raw state/local；config热删除；empty data namespace删除；update注册；remove后Manager state=false/local=[]，但same PID API继续；bound job从close后的counter100执行到101；Wrong pid+exact global jobID移除服务，不移API。service普通HTTP返回accepted后另核真正counter/trace，不拿200当完成。
- init失败保存了副作用且没有post-failure getter，仅返回既有code-only `plugin: host_operation_failed`；同PID随后恢复。第二host的新workerconf={}、counter0、state=false，仍installed，本次transient jobs/close/job没有冷启重播。
- 环境按每次启动继承，driver末态环境相等；没有清proxy/CA/guard。不宣称不同exec间全环境相同。login凭据未读取/复制，private-data/DB/二进制未纳本独审文本附件。

## 3. 源合同与必要边界

真实源8方法 collector01 的 SQLite/Pydantic/stopped APS + owned callbacks，及empty-all新增原JSON，仍按各自组件层级使用。normalCLI是当前目标运行，非执行原Python fullhost/timer；非空delete_data与热失败/冷恢复由App/源SQLite证据承担，CLI只补空namespace普通wire。source异常原文不越过既有SDK脱敏边界。

empty-all running-order发现已由冻结实现和测试闭合；exact operation pin跨init→state，job/API registry owner不是Host权限；注销释放在锁外；实际legacy logical remove只物理TerminateTransport、不额外business stop，保存 intentional terminal而不伪称worker clean exit0。SDK Drained/TerminateTransport内部的独立终审作为明确引用，current对应源SHA核同，本报告不冒充其作者或重跑其门。

本片不声称serial worker支持其自身同步重入（实际self路径要求协商concurrent）、任意递归Python callback、官方鉴权provider、所有业务插件/43个调用方、浏览器UI、真实下载器/NAS或整平台完成。无需为这些排除范围扩新矩阵才能接受当前已约定普通切片。

资源均是阶段/调用边界采样：race9个样本最小scratch57,428,267,008B/tmp5,225,664,512B；CLI45个样本最小scratch57,420,365,824B/tmp5,225,664,512B。没有连续监控，不能声称全程保留线。当前源码与完整实际输入仍冻结可复查；ledger/发布由协调者另封，独审不改它们。
