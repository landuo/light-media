# M0 受保护种子材料获取探针

此程序验证五站能否取得一份可解析的 `.torrent` 材料，只在内存处理。它不提交下载器、不宣布 tracker、不建立媒体文件、不辅种，不保存真实种子材料或原始响应。没有自动重试、翻页或后台轮询。

## 输入、请求与输出边界

`cmd/metainfocheck` 从 stdin 接收已有 `integrations.Input` JSON，仅使用 `sites`。字段沿用 `id/kind/base_url/cookie/api_key/query`；下载器字段被忽略，不会连接下载器。最多五站、输入 64 KiB、每个查询 256 字节；HTML 站点最多两次、MTeam 最多四次 HTTP 请求（含一次受控材料跳转），单次十秒，总上下文两分钟。必须显式给查询词；空查询返回 `unverified`。凭据应由已授权的内存 broker 提供，不放命令行、环境变量、普通输入文件、Git 或报告。

```sh
CGO_ENABLED=0 go build -o work/metainfocheck ./cmd/metainfocheck
# 由内存凭据 broker 向该进程 stdin 传入配置。
```

请求仅一页搜索、首个候选、最多一份材料；搜索/JSON 响应上限 1 MiB，材料上限 4 MiB。HTTP client 自动重定向全部拒绝，只有下述 MTeam 材料阶段的一次手动校验跳转例外。不关闭 TLS 校验、不登录站点或生成 API Key。HTTP 成功、Content-Type 和文件后缀均不足以证明业务成功。

报告只含调用方的非敏感标签、kind、证据类型、阶段、稳定状态/原因码、请求数、受保护材料是否可用，以及文件数/总字节数和 v1/v2 结构摘要。**不输出标题、文件名、URL、候选 ID、tracker、Cookie、API Key、token、原始 info hash、原始响应或原始种子。** 标签必须由调用方提供安全的稳定名称，不能以秘密作为 id。

`protected_material_available: true` 只在材料通过有界 bencode 和 info 结构检查后成立，表示本次材料确实可取得。它不证明媒体内容、版权/质量、做种可行、客户端是否接受、文件已完整或实际迁移成功。`payload_verified` 始终 false。

退出码 0 表示所有要求的材料检查通过；1 表示部分失败/未验证/空搜索；2 表示输入 JSON 或输入长度错误。`empty_result` 仍不能验证材料可获取，因此总报告为 incomplete。失败不输出服务端错误正文。

## 分站契约

**MTeam**：只向显式配置、列入允许名单的 API origin 发 `x-api-key`，搜索 `POST api/torrent/search` 的第一页十项，取第一个数值候选 ID。根据固定 MoviePilot 源码 `mtorrent.py` 的 `genDlToken` 约定，`POST api/torrent/genDlToken` 使用 form 字段 `id`；要求业务 code 为零、data 为 URL 字符串。

API origin 仅允许 HTTPS 的 `api.m-team.cc`、`api.m-team.io` 与官方 API 文档所列测试站 `test2.m-team.cc`。实际 token 下载 URL仅允许 HTTPS、默认 443、无 userinfo/fragment，主机必须是 `m-team.cc` / `m-team.io` 或其严格点分子域，或下述精确材料 CDN；类似 `m-team.cc.evil.invalid` 不匹配。对其他未知 CDN、域与 HTTP 明确返回未验证/失败，绝不扩展授权。

授权只读联调的 `work/mteam-redirect-shape.json` 记录可信 MTeam API 本次提供的材料跳转为 `api.m-team.cc → HTTPS fr1.halomt.com:443`，查询仅记录字段名 `app_id/playload/sign/t/v`，没有值。仅据此追加 **精确 `fr1.halomt.com` 材料下载主机**，不允许 `fr2.halomt.com`、`halomt.com` 通配子域或恶意后缀，且不能作为 API origin。此依据是当前已授权 API 的材料数据流，不声称公开 Wiki 已证明其所有权或长期归属；新主机仍需重新核实。

实站首轮表明 MTeam 材料地址会返回重定向，因此仅在 token URL 的材料 GET 阶段允许 **一次** 301/302/303/307/308：先解析 Location（相对链接按当前材料 URL 解析），重新检查上述 HTTPS 材料主机允许名单、默认 443、无 userinfo/fragment，合格后再发一个全新 GET。下一次重定向、越域/降级或缺失 Location 都停止。API 搜索、token 请求及四个 HTML 站点仍拒绝重定向。报告只计 `controlled_redirects`，不输出 Location/URL。

**下载 token URL 及唯一受控跳转的请求都不带 API Key、Cookie、Authorization 或 Referer；HTTP client 没有 cookie jar。** 因而接口响应的 Set-Cookie 不会传播到下载请求。跨域边界测试使用合成响应验证，不访问真实站点。

**憨憨**：用 `keyword/page=0`，要求已验证的 searchinput/name=keyword、div.torrents-table、首条 torrent-table-sub-info 内的 torrent-table-for-spider-info；从该条目解析详情与明确下载链接。授权实测形状 `work/hhan-download-shape.json` 确认搜索结果下载链接是同源 `/download.php`，查询键恰好 `id` 与 `passkey`（摘要没有原值）。仅此站允许这两个键各一次：id 必须为首候选的数值 ID，passkey 必须非空、最多 128 个 ASCII 字母/数字/下划线/连字符。它保持原始链接并只在内存发送至配置的同源 HTTPS 地址；不猜测或重新构造 passkey，不输出 URL/原值。重复键、其他参数、外域/降级和重定向全部拒绝。

**UBits、家园、1PTBA**：保留 NexusPHP candidate 的 table.torrents/id=torrents 结构要求。只从识别出的搜索 table 内取首个数值 details.php 候选及相同 ID 的显式下载链接。

UBits、家园、1PTBA 只访问配置 origin 的 `download.php?id=数字`，查询必须只含一个 id，保留禁止 passkey/额外参数的策略。四站都要求搜索结构内的明确链接：没有链接、外域、未支持的参数或磁力链接返回 `unverified`，不从详情 ID 猜下载地址，不请求第二个候选。搜索模板变化、登录页和挑战页不会被当成空结果。

HTML selector 是 M0 有界窄探针，未实现通用 DOM/全站资源搜索。它不验证促销、详情、分页、完整发布属性、站点内容身份与受保护材料长期缓存策略。

## bencode / info 校验

标准库实现的小扫描器保留原始字节切片，不做 decode→encode 的 hash 重建。限制 4 MiB、递归深度 64、节点/键/值项总数 50,000、文件 10,000、字典键最大 4 KiB；探针整数限于 int64，并拒绝大小求和溢出。更大的合法材料会显式失败，首版不放开无限解析。

拒绝截断/超长字符串、长度前导零、非法整数/负零/整数前导零、重复或不按原始字节排序的字典键、尾随垃圾、非字典顶层或缺失/非字典 info。HTML 登录或错误页不能伪装成种子。字典字符串按原始字节处理，不假定每个字段都是 UTF-8。

v1 检查 name、正 piece length、length/files 二选一、非负文件长度、路径列表、重复路径、pieces 长度为 20 的倍数且数量与总长度一致；SHA-1 对原始 info 字节计算，只报告完成事实。v2 要求 meta version=2、至少 16 KiB 的二次幂 piece length、合法 file tree、非空文件 32 字节 pieces root 和 piece layers 字典。大文件层的条数与大小须匹配；将层哈希按 BEP 52 的零节点规则补齐 Merkle 树，核对 pieces root。小文件 root 无媒体 payload 可供验证，因此不宣称文件内容正确。

Hybrid 检查两套内容路径与长度一致，v1 padding 条目参与 piece offsets 但从内容布局比较中排除；Hybrid 汇总按 v2 内容文件/字节计数。未来做种预览仍需按客户端实际文件布局与完整文件 piece verification 验证，不能仅依靠本报告批准辅种。

## 证据与验证

`testdata/protocol/synthetic-torrent-v1.torrent` 是 **synthetic**，tracker 使用 `.invalid`，没有真实 passkey、素材或有效媒体 payload。新增测试均使用 synthetic 编码、localhost httptest 或受控 HTTP round-trip，不是 captured 协议回放或真实站点认证。测试覆盖 v1/v2、piece-layer root、资源限制、错误/重复字典键、HTML 伪材料、跨域/私有参数/重定向拒绝、MTeam form/API Key 隔离和报告脱敏。

源码实现期间未接触实站凭据。后续 broker 的授权只读实测应另存脱敏摘要；只有 live_request 的实际通过结果才能记为实站一份材料可获取，不能将本合成测试记作五站实测通过。

协议依据：[BEP 3](https://www.bittorrent.org/beps/bep_0003.html) 规定 bencode 和 v1 info；[BEP 52](https://www.bittorrent.org/beps/bep_0052.html) 规定 v2 file tree、原始 info 哈希和 piece layers。[MTeam 官方 API 说明](https://wiki.m-team.cc/api) 规定 API Access Token、x-api-key、允许的接口边界和速率建议；官方 tracker 说明记载 cc/io 两类地址。下载 token form 约定来自固定 MoviePilot `1528176beacc0f9f8d8d9cbf1adf048b0da67b5c` 的 `app/modules/indexer/spider/mtorrent.py`，仍需以当前实站结果验证。
