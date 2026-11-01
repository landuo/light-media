# S11 explicit-ID 原组件实际采集独审

**ACCEPT，限原组件 recording 层。** 协调者唯一执行 `cdd5bc exit0`，01:09:56.864789–01:09:57.605691；stderr空，stdout19473B/SHA `e517f848ee9e8aaff54170f30335be4a2eb5aaecf7cffff07806fecb81801ecb`。本审只读回，未重跑。command.json的RUNNING是启动历史，result.json/exit才是当前终态。

16输入、8准备项和准入文件当前SHA核同，实际节点记录与已独核的4完整原方法＋原MediaType enum一致。12case原输入及类型、完整有序trace、kwargs、返回/异常逐项核回：**10return＋2未捕获RuntimeError；13读取/11依赖返回/2依赖异常/唯一WARN，无INFO或ERROR。** 不是12次业务成功。

## 最小可复用合同

1. metaNone时，缺失/null或0 ID验证False；所采正整数True。
2. 已知MOVIE/TV仅一次get_info，直接返回原依赖值；TV None不转查movie。实际参数是MediaType枚举＋int，不是字符串替身。
3. 未指定类型是**None而非MediaType.UNKNOWN**：顺序TV→movie，即便TV已命中仍执行第二读。仅一边真就返回该原字典；{}＋None返回None。
4. 两边真且metaNone：原消歧函数先None，再唯一WARN `无法判断tmdb_id:900011 是电影还是电视剧`，最终None。不据此推断标题/年份消歧。
5. 第一读异常立即停止；第二读异常即便已有真TV仍直接传播。此helper层不捕获、不制造ERROR日志、不改None；外层module/ChainBase错误处理仍属另一层。
6. report payload在metaNone时早退None，即便mediainfo为真owned sentinel；不访问模型字段/身份helper/HTTP。

允许后续差分直接复用完整原raw和上述精确层级，不从目标反推oracle或裁掉真实trace。recording replies/errors不是实际provider/vendor/HTTP；类壳不是源生命周期。fresh/cache/fullChain/最终MediaInfo/metadata评分/SDKhost未在此门执行。旧10oracle与原3源只作当前同SHA继承，未重采或提升信用。无本有限源证据阻断；产品完整性须由后续真实实现及门证明。
