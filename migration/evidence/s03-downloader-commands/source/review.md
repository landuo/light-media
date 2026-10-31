# S03 source01：实际原 wrapper 观测独审

**接受为有限源组件实证；不是 S03 产品验收。** 协调者按冻结命令单次运行，工具回执 117ef0 / exit 0；原始 stdout 48,216 B，SHA `8de99e30850e6a111b9b97de08de755b50452bbd6da463567111e11872952e26`，stderr 为空。独审没有重运行原源、collector、Go 或服务。

## 已核事实

- 6 个冻结输入当前重哈相同；实际 10 个完整原节点与已静审节点逐项一致，34 个实际 case 输入与冻结 JSON 顺序、内容完全相同。Python 3.12.14，原 -I -S -B；无源 constructor/整个模块导入。实际模块禁入检查和前后输入检查通过。
- 23 次 recording 调用、15 次给定返回、8 次 owned RuntimeError、9 条原 ERROR；11 个前置短路不调用端口。34 个 wrapper 均返回，包括业务 False/None 和被源捕获的错误；不能写成 34 次下载器操作成功。
- qB 缺 client、空 content、vendor False、parser 错误均可返回 (False, [])，整个 tuple 仍为真。列表 ID 去假值后得到 ["Owned-A", "42", "True"]；计数、映射字符串回退和分类/default/null 分支保原 trace。
- qB tag/path 假值变 None，is_paused 的缺省 False 与显式 None 分开；TR 的空 content、空目录、空 labels 和显式 None 原样进入 recording 端口。TR 给定普通 JSON 仅证明直通，不是 vendor Torrent。未调用/异常时 None 的 identity=true 不构成返回链证明，必须同时看 vendor_return_observed。
- start/recheck 对 None/[] 仍调用端口；delete 对空 ids 短路。六种非 add 操作中 vendor 返回 False 后源 wrapper 仍返回 True；异常才记 ERROR 并 False。delete 两层均固定 exact False。

## 后续可复用方式与限制

直接把完整原 raw 作为不可变 oracle；若生成目标 fixture，只附原 SHA/显式层级投影，保留每 case 的原 input、tuple/type/truth、return 与有序 trace，不裁成 Go 预期。Target 需显式区分源值和宿主授权/未知副作用，不能把 wrapper True 当 daemon 确认。TR 普通 JSON identity 不是新真实返回对象合同。

本次仅原八叶方法与两个 qB helpers，对 recording 端口执行；无 native HTTP/RPC、真实下载器、普通 SDK/host 验收。URL+cookie/Version 分支、真实 URL 获取与任意对象 getter 未覆盖；synthetic bytes 未证明有效 torrent。模块检查不是 syscall trace；环境不改由冻结脚本静读与执行收据支持，不声称另有全环境动态探针。
