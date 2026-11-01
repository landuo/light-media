# 四父08/09/10/12继承与本轮共因闭合提案

本提案不是权威ledger，也不是自行宣布四父已验收。精确独立父项组合裁决由非作者作出，采用/ledger/commit/push/远端读回由协调者负责。

## 完整范围继续继承

- **08 市场列表**：原卡片加工、搜索/标签/作者/仓库筛选、20条追加及同数量刷新、空结果/失败重试、分身逻辑版本与对象隔离信用保留。
- **09 市场选择**：原完整package/repository竞争、普通版本/兼容性/原始字段与次序、失败及缓存选择信用保留。旧184观测中的2个超2MiB差分是本轮已知共因；原raw仍不可达，新parser6是新执行。
- **10 刷新**：原显式force、缓存/后台刷新、进度/取消相关已验信用保留；本轮只改变展示材料读取边界，不修改刷新调度器。
- **12 历史/Release**：原作者历史/资产列表/指定版本三层分离、latest更新、原UI无旧版本选择器、无仓库/缺类/init失败/分身等信用保留。Python纯展示不变成可下载执行资产。

`inheritance-readback.json`保存soleledger四父原合同/历史信用文字的只读快照，不把旧schema状态当新进度。

## 精确继承而非泛称所有字节没改

1. 已提交 `market-pipeline/candidate.json` 的111文件均对应真实122faf提交blob；当前5f829有108同字节。3差异原始patch保留：
   - market cache唯一原HTTP构造改为A808已交的专用RequestUtils材料client；原package缓存/选择仍同字节。
   - http.go新增已交统计/rating及versionedservice拦截、专用错误类别和TempFileDays保留。其路径/类型不吞市场/list/history/release；本轮App和普通CLI又走真实路由。
   - 当前index.html已加载后续统计修正构建，不把留在目录的旧bundle冒充当前加载。
2. 当前index实际是`index-DNRPL9b-.js`→`Plugins-CWrbcVZf.js`。134个已接受service-usage-runtime可达资产逐哈希同已执行frontend-current记录；整个web/src与web/dist对b4f325接受提交零diff。
3. `Plugins.vue`对122faf只含已交统计请求失败保留旧统计并console.error、删除额外状态文案；保留原patch。不是本轮新UI更改。原市场选择/筛选/分页/历史组件信用不清零，也不冒本轮浏览器执行。
4. 资源矩阵中已固定的市场13个leaf与源9个文件当前仍同字节；原始native acquire预算不变。候选仅改变Python展示reader调用及取消检查，新增专用reader，不碰全局MaxIndexBytes或Go可执行材料预算。

## 本轮实际证据分层

- 原源完整parser六个有限输入，含合法>2MiB dict/空白、非法JSON/非dict/404/small，独审source-final01；仅parser真实执行，getter/异步HTTP/res.text全链静态绑定，不伪原HTTP/缓存新执行。
- 四个artifacts根 normal/race，真reader+parser、关闭/取消/错误/redirect/原生限额；单包vet单独实际PASS。共同runner旧文字不代表本轮vet曾失败。
- App normal/race：新root真实隔离TCP4 TLS完整读取、列表/选择/历史/Release、坏JSON与503失败不抹旧缓存、恢复；历史类为显式synthetic imported-class边界。旧package-cache精确root10子例复用。
- 普通cmd/server真实build及两个normal host、同privateDB、15个固定TLS请求：市场/list/选择/force失败/缓存保留/恢复/冷启/未安装release。此路径不需要worker，不为“真实”加安装或重跑父11。实际CLI独审`cli-final01/seal.json`为准。

所有测试私有DB、日志、随机密码/API token/cookie、TLS私钥和可执行二进制不进入发布。旧184/2缺raw情况保留，不伪恢复。

## 最终条件

四父历史完整合同 + A808既交 + 本轮新原parser/目标reader/App/普通CLI实际与独审 + 当前依赖证明，可交独立整项组合审。最终还需协调精确采用5文件、合入必要证据与ledger，预提交独审、commit/push及真实远端SHA读回。本目录不自行通过该发布条件。
