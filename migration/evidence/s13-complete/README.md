# S13 · 精确八方法整项行为与宿主链验收

**独立组合终审 ACCEPT；采用、账本、提交和远端/CI 由协调者另记实况。** 终审原文与 seal 在 `parent-review/`，原始时间/内容未改。这是当前61清单中的一个完整 S13 父项，不是新增 numeric 子片交付。

范围：qB `delete_torrents_tag`、`set_torrents_tag`、`get_torrent_id_by_tag`、`stop_torrents`、`set_torrent_category`；TR `set_torrent_tag`、`stop_torrents`、`change_torrent`。继承 `migration/evidence/s13-four-controls`、`s13-qb-tags`、`s13-tr-final` 的原真实各层与普通安装版 CLI 信用；精确继承核对见 `inheritance/`，没有重跑或清零七个未受影响方法，也不扩 reannounce。

本轮闭合普通 numeric 字符串差异：原 CPython 3.12.14 的14真实案；leaf4/SDK3 各 normal+race；小 worker 2 根；App 2 根 normal+race 含真实 owned HTTP 三成功/一个 ValueError。普通安装 worker CLI02 实际五次业务调用、四次 True/一次源 ValueError、八次 RPC/两个 artifact GET，非法零 HTTP、同 worker 恢复、同私有 DB 新 host/worker 冷启，两个 host 自然退出0。详见各层实际结果及独立终审；owned HTTP 不是真实 Transmission daemon。

产品仅两个文件的 numeric 字符串处理；非 string、转换顺序、原 truthiness、round、int64/finite、try内外、未知效果不重试、当前身份/guard、TR TLS/proxy和生命周期边界保留。当前合法下划线/Unicode Nd/数值空白不再受旧 ASCII-only 边界拒绝；旧历史 README 的该限制只描述当时版本。原任意 Python 对象/无限数值域没有被新增为验收条件。

初次 App 夹具 FAIL 保留在 `numeric/retained-failure/`：测试误禁原 native 的空 Basic；successor02 仅改精确夹具断言。未改产品迎合测试，未伪造未运行的 race01/CLI01。源 raw 仍为完整原字节；三份 Go embed 包内副本 SHA 同一，canonical 路径为 `internal/plugins/downloadercommands/testdata/source-tr-numeric.json`。

`numeric/bindings/restore-index.json` 将60个编译绑定/生成源引用按原字节 SHA 去重为30个 gzip 对象，含长度、原/压缩 SHA 与逻辑恢复路径；已逐个解压核同。无需写原绝对路径或执行恢复材料。无私有 DB、secret、登录材料、全 host/app 日志、运行二进制、依赖树或无关38/S01候选。编译清单列出的稳定传递路径只是证据元数据。

采用白名单为 `target-candidate-bindings.json` 的11路径（2产品、6测试/夹具、3原raw副本）；不采用20个 compiler clean-view 回退/隐藏映射，不改25个Meta脏文件。协调者须在其他持有808148输入的执行终态后采用、读回字节、更新唯一ledger整项计一次，并做最终白名单/提交/远端核验。本目录的候选终审不冒充那些发布步骤。
