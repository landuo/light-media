# Go 插件仓库索引

仓库设置可填写一个 HTTP(S) 索引 URL，或 HTTPS GitHub 仓库根地址，同时发布多个插件。作者本地仓库仍使用 `go-plugins.v1.json`，同一文件也可包含多个插件。旧的单插件索引不需要迁移。

## 多插件目录格式

外层 `schema_version` 当前为 `1`，`plugins` 是有序数组。每项就是已有单插件 Index 的完整结构，不是可跳转到任意路径的索引链接。

```json
{
  "schema_version": 1,
  "plugins": [
    {
      "schema_version": 1,
      "plugin_id": "ExampleOne",
      "current_version": "1.0",
      "history": { "1.0": "作者更新说明" },
      "releases": [
        {
          "version": "1.0",
          "manifest": { "...": "完整、真实可执行文件导出的 Manifest" },
          "artifacts": [{ "...": "目标平台、URL、真实摘要、大小、格式和宿主契约" }]
        }
      ]
    },
    {
      "schema_version": 1,
      "plugin_id": "ExampleTwo",
      "current_version": "2.0",
      "releases": [{ "...": "第二个插件的完整 Release" }]
    }
  ]
}
```

上述省略号仅用于说明结构，不能直接安装。实际每个 Manifest、Release、Artifact 都必须满足现有 Go 制品校验；摘要必须来自实际编译产物，不可用占位字符串。外层不能同时出现单插件字段或未知字段，插件 ID 不能重复（包括仅大小写不同），上限 1024 项、整个索引上限 2 MiB。空数组表示当前没有发布插件。

## 用户与作者流程

1. 作者编译各插件、计算实际包与执行文件摘要，将完整声明加入同一数组。远程制品沿用绝对 HTTP(S) URL；本地制品使用配置根内相对路径，可以分别放在 `one/worker`、`two/worker`。
2. 用户只保存一次索引 URL。市场一次读取整份索引，再按插件 ID 展示；安装、更新、指定 release 和历史读取都继续携带目标 ID，选择该项，不能落到数组第一项或兄弟插件。
3. 多来源同 ID 仍先选最高版本；同版本保留配置来源顺序，在线先于本地。未显式声明 `add_time` 的远程候选按数组前后得到递减顺序，含义是索引位置，不是发布时间。本地没有据此虚构时间。
4. 当前版本不兼容的插件显示不可安装原因，其他兼容成员仍可安装。已安装旧版仍可配置，但不兼容新版本不能触发更新；后端始终重新核验平台、契约和摘要，不信任卡片的可用标志。
5. 本地监听按“目标插件声明 + 目标制品文件状态”计算指纹；只编辑或移动另一个条目不会重载未变的实例。改变目标制品仍需重新编译/发布，不自动编译 `.go`，不执行 `.py`。

## 与 MoviePilot 格式的边界

原 MoviePilot 从一个 `package.json` / `package.v2.json` 中按插件 ID 读取多项，处理包分支、系统版本兼容和授权后，再跨来源去重。此 Go 格式实现同一仓库多项及跨来源优先逻辑，以 Go 宿主 API、平台和制品契约执行校验；没有实现 Python 包分支选择、pip 依赖或 Python 执行环境。

直接提供原 `package*.json` 的 JSON URL 时，可以读取其中的名称、版本、作者、标签和历史作为明确不可安装的元数据。安装与已有类兼容检查都不会接纳它；显示信息不授予执行权限。HTTPS GitHub 仓库根地址（owner/repo，可带.git和尾斜线）先读取 main/go-plugins.v1.json；仅该文件返回404时，读取 package.v2.json 与基础 package.json。基础项必须明确 v2=true，两组候选仍按最高版本/来源规则合并。所有这些原Python项始终不可安装。此 main 分支来自原 PluginHelper._base_url，不调用默认分支 API；自定义分支请填写直接索引 URL。tree/branch 网页、认证信息或任意查询参数不用于根地址解析；强制刷新的内部 _refresh 参数可透传。读取失败不宣告空列表成功。

不兼容条目保留的是作者当前版本，不自动改为旧版本成功。结构损坏、重复 ID、缺失身份或缺失当前 Manifest 的仓库会整体报错；平台/契约不兼容成员按项显示原因。已有单插件 `Discover` API 仍要求结果唯一；宿主多插件列表使用 `DiscoverRepository`，精确读取使用 `DiscoverPlugin`。

## 本轮验证

`TestOriginalMarketCatalogActualMultipleInstallUpdateAndIsolation` 使用三个真实 Go 编译产物，通过同一 HTTP 索引安装两个 ID，再只更新其中一个；检查实际 IPC 版本、实例身份、来源收据及不兼容更新状态。`TestOriginalMarketCatalogLocalWatcherOnlyReloadsChangedPlugin` 验证同一本地目录中更新并重排一个插件不会重启另一个。`artifacts/repository_test.go` 覆盖惰性读取、精确选择、重复/混合结构拒绝、本地指纹隔离，以及原 Python 元数据不能进入安装流程。

## 分身的精确身份更新

复制产生的 Go 分身最初运行源插件的独立副本；这不意味着可以用源插件的市场更新包覆盖它。市场/历史/更新仅查找完整 instance_id。若作者确实发布同名分身ID的制品，宿主在校验真实制品后，允许受认证的 clone 记录把业务ID绑定到这个精确ID，同时保留 host_instance、配置和运行数据；普通实例仍禁止跨插件抢占。卸载后按父生命周期实现删除分身代码/数据并留下不可用别名，同名重建可重新复制当前源插件。

对应真执行用例 TestOriginalMarketCatalogExactCloneUpdatePreservesSourceAndRecreatesAfterUninstall：源包force更新clone必须在预检拒绝；exact-ID包升级、保留配置数据/源进程与字节、卸载、同名重建均分别核对。该能力不会自动同步源插件更新给所有分身。

## 分身在 Go 代码中读取自己的名称

作者在 Manifest 的 capabilities 中声明 `plugin.base.get_instance_metadata`，并使用宿主已识别的 `light-media.sdk-base/v1` 或 `light-media.sdk-resources/v1` 契约；`plugins.BaseMethods()` 已包含这项声明。当前业务调用内可以使用：

```go
base := plugins.Base{Host: host}
metadata, err := base.GetInstanceMetadata(ctx)
if err != nil {
    return nil, err
}
name, err := base.GetName(ctx) // 对应原 _PluginBase.get_name()
```

`metadata` 返回当前实例的 `InstanceID`、直接来源 `SourceInstanceID`、`Name`、`Description`、`Icon`、`DisplayVersion`；原实例的来源为空。分身不指定字段时沿用创建时确定的源实例值。`PluginID` 与 `Version` 则仍为当前 worker 认证的真实制品身份。例如分身显示版本可为 `9.7`，实际运行的二进制仍为 `1.0`；展示值不能替代摘要、握手或版本校验。

该接口只读、只处理当前实际调用对象，只接受空 JSON 对象，不允许指定其他 plugin_id 或提交名称、版本等字段。元数据来自宿主认证记录，不读取插件配置中的同名字段。停止、替换或已结束调用的旧回调不能借此访问新实例。旧二进制不会自动取得新能力，作者必须声明并重新发布；现有配置/数据方法的显式目标语义不变。

Go 实现不会改写二进制中的字符串常量；需要分身名称的页面、通知等业务代码应调用上述接口。`TestOriginalBaseSDKActualCloneInstanceMetadata` 使用真实编译的 worker、管理 HTTP 分身与重载，验证自定义字段、真实版本不变、源进程独立、只读边界和持久化。
