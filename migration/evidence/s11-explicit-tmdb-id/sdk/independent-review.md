# S11 SDK02 普通／race 实际有限独审

**ACCEPT：wire-only 两根普通＋两根race。** 已独读实际raw，不重跑。normal02 1937输入、race02 1974输入各三阶段全同，当前逐SHA核同；程序、实际-race list/compile、生成testmain副本、精确两根与13子例各PASS，无skip/FAIL/DATA RACE。两门repo源子集相同，但不称普通与race整个闭包相同。

原normal01／race01都是真实**编译失败、零根执行**：fixture用*ReplyError（wire DTO不实现error）。candidate02只一行改成现有*Error；产品不变，原测试前像及原stderr/exit保留，不抹FAIL。

当前两根确证11字段缺省/null区别、RawMessage数值与源escape保真、object/null及克隆所有权、严格重复键/UTF8/输出形状/请求大小边界、HostCaller错误identity、取消及无自动重试。recording HostCaller specimen不称原source oracle、实际worker/host/native/fullMediaInfo；SDK保留raw输入也不表示host已经支持全部参数域。

准入静审由协调者完成，本独审另实读当前产品/断言和运行输入。运行result记录环境代理保持，仅按每次继承记，不推断跨exec环境完全相同。资源为三阶段采样，不宣连续reserve。新native/App源不在这SDK编译包的实际闭包内，不把增加其它包文件误作旧门失效。
