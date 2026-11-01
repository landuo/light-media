# Provider CN 原观察：有限 actual 终核

**接受固定原程序 CN enrichment 5例观察，不代表 target、真实 provider/cache/host 或整片等价。** 无重跑、无源码/native/Go/网络执行。

- raw stdout 78,354 bytes，SHA `285ad4a28c5b5bc03533baff22ff1bb43da490fa27be99222d3de1aa2f86dffa`；exit0、stderr0。tool311e58 无 session；tool-receipts 明示 coordinator 实际结果转录，非 raw tool replay。
- 当前独立重核68输入 hash/长度，drift=[]；实际8节点精确等于 preexec；实际5例输入/初始detail精确等于 frozen owned JSON。文件型 loaded modules 前后均绑定当前manifest字节，新增仅精确 initializer/extension 两模块。四 unit exec、一次 initializer exec、三个允许import事件；一次 bytecode_refused 是已审 fixed pyc 的主动拒读后 source fallback，不是工具拒绝或原业务错误。输出只留审计计数，不能冒称逐事件完整审计/原生 syscall 证明。

## 五个实际结果（不是新期望表）

1. Movie：真实转换依次 `電影標題→电影标题`、`專著→专著`、`电影专著→电影专著`。前两被相等性条件排除，第三个原 alias 选中；后续 alias 不再转换。显示 title=`电影专著`，返回同一 detail 对象。
2. TV：默认 MediaWiki 转换 `-{zh-hans:默认标题;}-→默认标题` 和 `電影→电影` 都改变输入，不合格；显示 name 回退 `新加坡中文`。native 默认 wikitext 确有运行，返回同一 detail。
3. Movie：`電影→电影` 被排除；空 SG 翻译先回退 original，再用于中文显示，title=`原始中文`、sg_title 同值，en_title=`Owned US`。同一 detail 返回。
4. 已有 `已有中文` 显示 title 不改；原 get_info 其他增强字段照常写入；零 native 调用，同一 detail 返回。
5. CN alias title=7：原 `StringUtils.is_chinese:116` 真实抛 `TypeError: expected string or bytes-like object, got 'int'`，frames 连到 get_info641→CN738→nested727→is_chinese116。不是 collector 人造错误；零 native 调用。显示 title 仍 Owned English，但 media_type、genre_ids、names（保留整数7）、content_rating、en/hk/tw/sg_title **均已写入**，错误前部分变更完整保留。

每例只有一个明示 detail recording 调用；共6次真实两参数 native 转换，所有返回 UTF-8 SHA 相符。四个成功返回的 dict 与记录 detail 完全相同；rating、names顺序、enum、genre IDs、extra titles 均与完整原节点读写次序一致。没有日志/target 结果代替源码结果，也没有删去第5异常以制造全成功。

范围仅为已冻结原 get_info/helpers/is_chinese/MediaType/convert 和 exact native；detail 是录制端口，language 固定 zh-CN，非真实 TMDB、cache/model/Chain/worker 或实际 HTTP。原4789/24证据不被替换；该raw可作为后续target修复差分依据，但本审查不批准任何Go变更。
