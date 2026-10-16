# 接入顺序（未实施）

1. 迁入纯包时保留27方法来源校验、所有85/52/32/14原执行参考、form/commands、来源/许可。仅改import/fixture位置；将JSON case流式化再进正式NAS256MiB参考测试，不能删case冒充完整。
2. App宿主冻结原配置与原下载器名称/实例ID对应、revision/generation、时区和触发上下文，严禁自动重放迁移前onlyonce；持久date(+3s)/cron注册与onlyonce=false需同配置生命周期处理。
3. 实现Host.Services原helper名字集合过滤与active/type筛选，不依赖下载管理页面的app标签范围；该插件明确针对用户配置下载器中的全部种子。重复名称/引用丢失必须显式报错。
4. 实现已授权NativeCall，明确语义方法到qB v2/Transmission协议转换、认证/会话、限额和错误类别；保留原生字段/lazy tracker，不能用download.Task投影代替；正向回放测试后才能连真实下载器，只读先行。
5. 文件Host单独绑定授权源根、run snapshot与file identity，保留Python Path语义（含物理symlink/rglob判别），效果时重验TOCTOU与受管归属。候选虚拟Host不是生产文件后端，不能放开任意传入path。
6. 通知通过outbox、LogHost通过实际插件身份/代次/revision写日志；源计数与下载器wrapper false/None不能充当真实成功收据。删除/标签操作需要记录实际/未知结果，不能盲目重试。
7. 注册Manifest、原form动态下载器选项、Commands五入口、无自创page/API；SDK配置/绑定事件/手动run的权限必须来自当前宿主调用上下文，不能因为worker请求clean_seed.execute就认为有权限。
8. 源全部verbose/debug逐字文本尚未逐条移植（核心结果/进度/错误已有LogHost），需按实际App日志约定补核；TR set标签wire顺序只有集合语义等效，默认native对象repr包含本次运行地址，不能把跨语言地址文字当稳定业务字段。
9. Host模拟完整链、事务/取消/重启/效果未知收据、SDK与App目标通过后，再由根代理安排独立真实测试实例；不清理既有用户种子或文件，不修改旧服务。
