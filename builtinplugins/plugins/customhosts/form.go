package customhosts

const form = `[
  [{"component":"VForm","content":[
    {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":6},"content":[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件"}}]}]},
    {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VTextarea","props":{"model":"hosts","label":"自定义hosts","rows":10,"placeholder":"每行一个配置，格式为：ip host1 host2 ..."}}]}]},
    {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VTextarea","props":{"model":"err_hosts","readonly":true,"label":"错误hosts","rows":2,"placeholder":"错误的hosts配置会展示在此处，请修改上方hosts重新提交（错误的hosts不会写入系统hosts文件）"}}]}]},
    {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAlert","props":{"type":"info","variant":"tonal","text":"host格式ip host，中间有空格！！！（注：容器运行则更新容器hosts！非宿主机！）"}}]}]}
  ]}],
  {"enabled":false,"hosts":"","err_hosts":""}
]`
