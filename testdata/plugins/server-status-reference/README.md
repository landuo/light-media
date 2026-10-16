# MPServerStatus 1.4 原执行参考

installed-source.json是实际安装源码AST、公开元数据、RequestUtils三方法、Python/Unicode和HTTP依赖版本快照。SHA指原安装文件正文，快照并不是正文副本hash。没有原用户配置或应用导入。

- generate_oracle.py只读本地快照，执行原42方法，合成RequestUtils/socket/ssl/time，输出父目录server-status-original-oracle.json（174例/15完整时序）。
- generate_unicode_reference.py通过只读SSH在原安装Python解释器仅运行静态parser AST，输出server-status-unicode-original-reference.json（8例）。不读取配置、导入应用或访问用户服务。快照已保存Unicode15全680数字/29空白。
- generate_network_reference.py须自行创建专用venv安装network-reference-requirements.txt全部固定版本，真正执行RequestUtils三方法及原插件AST，输出server-status-http-original-reference.json（19例）。仅绑定/连接127.0.0.1临时自签名TLS服务器；key在临时目录销毁；NETRC指自建空文件；没有Cookie/Authorization/通知。原HTTP per-request verify=False只限该fixture，独立TLS和普通客户端仍校验。

这三个脚本是审计/复现工具，不参与Go运行时或普通Go测试。普通Go测试读取已有JSON，不需要Python、SSH、真实网络或原服务。自然OpenSSL错误文案在本机产生，未声称与NAS系统文案逐字一致。网络JSON含随机临时端口/耗时，重新执行会改变正文SHA；合同字段和边界由Go测试固定核对。

来源和行为边界详见docs/plugin-server-status-go-port.md。ui-reference-audit.json是前端只读快照，后续前端编辑会改变其文件hash，不视为运行时依赖。
