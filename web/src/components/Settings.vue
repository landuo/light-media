<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { api, action, ui, refreshSettings, toast, date, bytes, go } from '../api';
const form = ref<any>(null),
  tab = ref('connect'),
  testResult = ref<any>(null),
  libraries = ref<any[]>([]),
  backups = ref<string[]>([]),
  allowed = ref(''),
  siteKind = ref('mteam'),
  saving = ref(false),
  logging = ref<any>(null),
  debugScope = ref('module'),
  debugID = ref('download');
const siteTemplates: any = {
  mteam: ['馒头', 'https://api.m-team.cc'],
  hhanclub: ['憨憨', 'https://hhanclub.net'],
  ubits: ['UBits', 'https://ubits.club'],
  jiayuan: ['家园', 'https://hdhome.org'],
  '1ptba': ['1PTBA', 'https://1ptba.com'],
};
const ready = computed(() => [
  { name: 'TMDB 元数据', ok: !!form.value?.secret_state?.tmdb },
  {
    name: '资源站点',
    ok: form.value?.sites?.some((s: any) => s.enabled && form.value?.secret_state?.sites?.[s.id]),
  },
  { name: '下载器', ok: !!form.value?.downloaders?.length },
  { name: '入库目录', ok: !!form.value?.roots?.length },
  { name: '飞牛影视', ok: form.value?.fnos?.enabled && !!form.value?.secret_state?.fnos },
]);
async function load() {
  const s = await refreshSettings();
  form.value = JSON.parse(JSON.stringify(s));
  form.value.tmdb ||= { api_key: '', access_token: '' };
  form.value.fnos ||= {
    host: '',
    username: '',
    password: '',
    library_guid: '',
    mappings: [],
    enabled: false,
  };
  form.value.log_level ||= 'INFO';
  form.value.log_max_mib ||= 5;
  form.value.log_retain_files ||= 5;
  form.value.auto_import_mode ||= 'hardlink';
  form.value.material_max_mib ||= 256;
  form.value.sites ||= [];
  form.value.downloaders ||= [];
  form.value.roots ||= [];
  form.value.allowed_roots ||= [];
  form.value.fnos.mappings ||= [];
  for (const d of form.value.downloaders) d.mappings ||= [];
  allowed.value = form.value.allowed_roots.join('\n');
  backups.value = (await api('/backups')) || [];
  logging.value = await api('/settings/logging');
}
async function save() {
  saving.value = true;
  try {
    const c = JSON.parse(JSON.stringify(form.value));
    delete c.secret_state;
    c.allowed_roots = allowed.value
      .split('\n')
      .map((p) => p.trim())
      .filter(Boolean);
    const result = await action(() => api('/settings', c, 'PUT'), '设置已保存');
    if (result) {
      ui.settings = result;
      form.value = JSON.parse(JSON.stringify(result));
      form.value.fnos.mappings ||= [];
      for (const d of form.value.downloaders) d.mappings ||= [];
      allowed.value = (result.allowed_roots || []).join('\n');
      return true;
    }
    return false;
  } finally {
    saving.value = false;
  }
}
function addSite() {
  const k = siteKind.value,
    t = siteTemplates[k];
  form.value.sites.push({
    id: k,
    kind: k,
    name: t[0],
    base_url: t[1],
    cookie: '',
    api_key: '',
    enabled: true,
    daily_budget: 500,
  });
}
function addDownloader() {
  form.value.downloaders.push({
    id: 'downloader-' + Date.now(),
    name: '新下载器',
    kind: 'qbittorrent',
    base_url: 'http://127.0.0.1:8080',
    username: '',
    password: '',
    download_path: '/media/download',
    mappings: [],
  });
}
async function test(target: string, id = '') {
  if (await save()) testResult.value = await action(() => api('/settings/test', { target, id }));
}
async function listLibraries() {
  if (await save()) libraries.value = (await action(() => api('/fnos/libraries'))) || [];
}
async function saveLogging(debug?: {
  scope: string;
  id: string;
  minutes: number;
  enabled: boolean;
}) {
  const result = await action(
    () =>
      api(
        '/settings/logging',
        {
          level: logging.value.level,
          retain_days: logging.value.retain_days,
          total_mib: logging.value.total_mib,
          rotate_mib: logging.value.rotate_mib,
          ...(debug ? { debug } : {}),
        },
        'PATCH',
      ),
    debug ? '临时调试范围已更新' : '日志策略已保存',
  );
  if (!result) return;
  logging.value = result;
  // Keep a later full settings save from reverting this dedicated update.
  form.value.log_level = result.level;
  form.value.log_retain_days = result.retain_days;
  form.value.log_total_mib = result.total_mib;
  form.value.log_max_mib = result.rotate_mib;
}
async function debug(enable: boolean, scope = debugScope.value, id = debugID.value) {
  if (!id.trim()) {
    toast('请选择需要调试的模块或站点', true);
    return;
  }
  await saveLogging({ scope, id: id.trim(), minutes: 30, enabled: enable });
}
async function backup() {
  const r = await action(() => api('/backups', {}), '数据库备份已创建');
  if (r) backups.value = (await api('/backups')) || [];
}
async function restore(name: string) {
  if (!confirm('准备恢复 ' + name + '？服务端会检查是否满足停机恢复条件。')) return;
  const r = await action(() => api('/restore', { filename: name, confirm: true }));
  if (r) testResult.value = r;
}
onMounted(() => action(load));
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">SETTINGS</p>
      <h1>设定</h1>
      <p class="muted">配置元数据、服务、媒体目录及系统维护。</p>
    </div>
    <button class="primary" :disabled="saving || !form" @click="save">
      {{ saving ? '正在保存…' : '保存设置' }}
    </button>
  </div>
  <template v-if="form"
    ><div class="button-row" style="margin-bottom: 20px">
      <button @click="go('sites')">站点管理与数据</button
      ><button @click="go('downloaders')">下载器配置</button
      ><button @click="go('files')">文件管理</button
      ><button @click="go('history')">整理历史</button>
    </div>
    <div class="setup-checklist">
      <div v-for="r in ready" :key="r.name" :class="{ done: r.ok }">
        <span>{{ r.ok ? '✓' : '○' }}</span
        >{{ r.name }}
      </div>
    </div>
    <p class="hint">
      秘密字段不会从服务端回显。显示“已保存”时，留空会保留原值；更换地址或账号后请重新填写凭据。连接测试会先保存当前设置。
    </p>
    <datalist id="fnos-libs">
      <option v-for="l in libraries" :key="l.guid" :value="l.guid">{{ l.name }}</option>
    </datalist>
    <div class="tabs">
      <button
        v-for="t in [
          ['connect', '连接与凭据'],
          ['paths', '目录与映射'],
          ['system', '维护与备份'],
        ]"
        :key="t[0]"
        :class="{ selected: tab === t[0] }"
        @click="tab = t[0]!"
      >
        {{ t[1] }}
      </button>
    </div>
    <template v-if="tab === 'connect'"
      ><section class="panel">
        <div class="section-heading">
          <h2>TMDB 元数据</h2>
          <span class="badge">{{ form.secret_state?.tmdb ? '凭据已保存' : '尚未配置' }}</span>
        </div>
        <p class="hint">
          API Key 与 Access Token 只需填写一种，用于影视、榜单、演员资料及相关推荐。
        </p>
        <div class="form-grid">
          <label
            >API Key<input
              v-model="form.tmdb.api_key"
              type="password"
              autocomplete="off"
              placeholder="留空保留已保存值" /></label
          ><label
            >Access Token<input
              v-model="form.tmdb.access_token"
              type="password"
              autocomplete="off"
              placeholder="与 API Key 二选一"
          /></label>
        </div>
        <button class="small" @click="test('tmdb')">保存并测试 TMDB</button>
      </section>
      <section class="panel">
        <div class="section-heading">
          <h2>资源站点</h2>
          <div class="inline-form">
            <select v-model="siteKind">
              <option v-for="(s, k) in siteTemplates" :key="k" :value="k">
                {{ s[0] }}
              </option></select
            ><button class="small" :disabled="form.sites.length >= 5" @click="addSite">
              ＋ 添加站点
            </button>
          </div>
        </div>
        <article v-for="(s, i) in form.sites" :key="i" class="config-item">
          <div class="section-heading">
            <h3>{{ s.name || s.kind }}</h3>
            <div class="button-row">
              <label class="check"><input v-model="s.enabled" type="checkbox" />启用</label
              ><span class="badge">{{
                form.secret_state?.sites?.[s.id] ? '凭据已保存' : '待填写'
              }}</span
              ><button class="small danger" @click="form.sites.splice(i, 1)">移除</button>
            </div>
          </div>
          <div class="form-grid">
            <label>标识 ID<input v-model="s.id" placeholder="唯一英文标识" /></label
            ><label>名称<input v-model="s.name" /></label
            ><label>协议<input :value="s.kind" readonly /></label
            ><label>站点 / API 地址<input v-model="s.base_url" type="url" required /></label
            ><label v-if="s.kind === 'mteam'"
              >API Key<input
                v-model="s.api_key"
                type="password"
                autocomplete="off"
                placeholder="留空保留" /></label
            ><label v-else
              >Cookie<input
                v-model="s.cookie"
                type="password"
                autocomplete="off"
                placeholder="留空保留" /></label
            ><label
              >每日请求预算<input v-model.number="s.daily_budget" type="number" min="0" max="10000"
            /></label>
          </div>
          <button class="small" @click="test('site', s.id)">保存并测试站点</button>
        </article>
        <p v-if="!form.sites.length" class="hint">添加你使用的站点，再配置 Cookie 或 API Key。</p>
      </section>
      <section class="panel">
        <div class="section-heading">
          <h2>下载器</h2>
          <button class="small" @click="addDownloader">＋ 添加下载器</button>
        </div>
        <article v-for="(d, i) in form.downloaders" :key="i" class="config-item">
          <div class="section-heading">
            <h3>{{ d.name }}</h3>
            <button class="small danger" @click="form.downloaders.splice(i, 1)">移除</button>
          </div>
          <div class="form-grid">
            <label>标识 ID<input v-model="d.id" /></label
            ><label>名称<input v-model="d.name" /></label
            ><label
              >类型<select v-model="d.kind">
                <option value="qbittorrent">qBittorrent</option>
                <option value="transmission">Transmission</option>
              </select></label
            ><label>Web / RPC 地址<input v-model="d.base_url" type="url" /></label
            ><label>用户名<input v-model="d.username" autocomplete="off" /></label
            ><label
              >密码 {{ form.secret_state?.downloaders?.[d.id] ? '（已保存）' : ''
              }}<input
                v-model="d.password"
                type="password"
                autocomplete="off"
                placeholder="留空保留" /></label
            ><label
              >下载器保存路径<input v-model="d.download_path" placeholder="下载器视角的路径"
            /></label>
          </div>
          <h4>下载器路径 → 程序路径</h4>
          <div v-for="(m, j) in d.mappings" :key="j" class="mapping-row">
            <input v-model="m.from" placeholder="例如 /downloads" /><span>→</span
            ><input v-model="m.to" placeholder="例如 /media/download" /><button
              class="small danger"
              @click="d.mappings.splice(j, 1)"
            >
              移除
            </button>
          </div>
          <div class="button-row">
            <button class="small" @click="d.mappings.push({ from: '', to: '' })">＋ 添加映射</button
            ><button class="small" @click="test('downloader', d.id)">保存并测试下载器</button>
          </div>
        </article>
        <p v-if="!form.downloaders.length" class="hint">
          支持 qBittorrent 与 Transmission，按实例独立管理。
        </p>
      </section>
      <section class="panel">
        <div class="section-heading">
          <h2>飞牛影视</h2>
          <label class="check"
            ><input v-model="form.fnos.enabled" type="checkbox" />启用飞牛入库同步</label
          >
        </div>
        <div class="form-grid">
          <label
            >影视服务地址<input
              v-model="form.fnos.host"
              type="url"
              placeholder="https://nas.example.com:8080/v" /></label
          ><label>用户名<input v-model="form.fnos.username" autocomplete="off" /></label
          ><label
            >密码 {{ form.secret_state?.fnos ? '（已保存）' : ''
            }}<input
              v-model="form.fnos.password"
              type="password"
              autocomplete="off"
              placeholder="留空保留" /></label
          ><label>默认媒体库 GUID<input v-model="form.fnos.library_guid" /></label>
        </div>
        <h4>程序路径 → 飞牛宿主路径</h4>
        <div v-for="(m, j) in form.fnos.mappings" :key="j" class="mapping-row">
          <input v-model="m.from" placeholder="例如 /media" /><span>→</span
          ><input v-model="m.to" placeholder="例如 /vol2/1000/media" /><button
            class="small danger"
            @click="form.fnos.mappings.splice(j, 1)"
          >
            移除
          </button>
        </div>
        <div class="button-row">
          <button class="small" @click="form.fnos.mappings.push({ from: '', to: '' })">
            ＋ 添加映射</button
          ><button class="small" @click="test('fnos')">保存并测试连接</button
          ><button class="small" @click="listLibraries">保存并读取媒体库</button>
        </div>
        <div v-if="libraries.length" class="lookup-list">
          <button
            v-for="l in libraries"
            :key="l.guid || l.id"
            @click="form.fnos.library_guid = l.guid || l.id"
          >
            {{ l.name || l.title }} <span>{{ l.guid || l.id }}</span>
          </button>
        </div>
      </section></template
    >
    <template v-else-if="tab === 'paths'"
      ><section class="panel">
        <div class="section-heading">
          <h2>媒体库目录</h2>
          <button class="small" @click="listLibraries">读取飞牛媒体库 GUID</button
          ><button
            class="small"
            @click="
              form.roots.push({
                id: 'root-' + Date.now(),
                name: '新媒体库',
                path: '',
                kind: 'mixed',
                fnos_library_guid: '',
              })
            "
          >
            ＋ 添加目录
          </button>
        </div>
        <article v-for="(r, i) in form.roots" :key="i" class="config-item">
          <div class="form-grid">
            <label>标识 ID<input v-model="r.id" /></label
            ><label>名称<input v-model="r.name" /></label
            ><label>绝对路径<input v-model="r.path" placeholder="/media/电影" /></label
            ><label
              >飞牛媒体库 GUID（留空用全局）<input
                v-model="r.fnos_library_guid"
                list="fnos-libs"
                placeholder="对应此目录的飞牛库" /></label
            ><label
              >类型<select v-model="r.kind">
                <option value="mixed">混合</option>
                <option value="movie">电影</option>
                <option value="tv">剧集</option>
              </select></label
            >
          </div>
          <button class="small danger" @click="form.roots.splice(i, 1)">移除此目录配置</button>
        </article>
        <p class="hint">目录配置的移除不会删除已有文件。硬链接要求源与目标处于同一文件系统。</p>
      </section>
      <section class="panel">
        <h2>允许扫描的源目录</h2>
        <label
          >每行一个绝对路径<textarea
            v-model="allowed"
            rows="5"
            placeholder="/media/download"
          ></textarea>
        </label>
        <p class="hint">手动扫描和下载整理仅允许访问这些源目录，目标目录由上方媒体库配置限定。</p>
      </section>
      <section class="panel">
        <h2>默认位置</h2>
        <div class="form-grid">
          <label
            >下载后整理方式<select v-model="form.auto_import_mode">
              <option value="hardlink">硬链接（默认）</option>
              <option value="copy">复制</option>
              <option value="auto">自动（允许跨卷复制）</option>
            </select></label
          >
          <label
            >默认下载器<select v-model="form.default_downloader">
              <option value="">未指定</option>
              <option v-for="d in form.downloaders" :key="d.id" :value="d.id">
                {{ d.name || d.id }}
              </option>
            </select></label
          ><label
            >默认入库目录<select v-model="form.default_root">
              <option value="">未指定</option>
              <option v-for="r in form.roots" :key="r.id" :value="r.id">
                {{ r.name || r.path }}
              </option>
            </select></label
          >
        </div>
        <p class="hint">
          下载预览会冻结本次整理方式。硬链接必须同卷；选择复制或自动时，跨卷发布会额外占用目标空间，下载源仍保留。
        </p>
      </section>
      <section class="panel">
        <h2>加密种子归档</h2>
        <label
          >加密种子归档上限（MiB）<input
            v-model.number="form.material_max_mib"
            type="number"
            min="32"
            max="2048"
        /></label>
        <p class="hint">
          默认 256 MiB，可设 32–2048 MiB。已确认下载的材料不会自动过期；未确认预览的孤立材料保留 24
          小时。总量满后会拒绝新预览，不会删除在用材料。
        </p>
      </section></template
    >
    <template v-else
      ><section v-if="logging" class="panel">
        <h2>日志与保留策略</h2>
        <div class="form-grid">
          <label
            >基础级别<select v-model="logging.level">
              <option>INFO</option>
              <option>WARN</option>
              <option>ERROR</option>
            </select></label
          ><label
            >单文件上限（MiB）<input
              v-model.number="logging.rotate_mib"
              type="number"
              min="1"
              max="20" /></label
          ><label
            >保留天数<input v-model.number="logging.retain_days" type="number" min="1" max="90"
          /></label>
          <label
            >日志总空间上限（MiB）<input
              v-model.number="logging.total_mib"
              type="number"
              min="1"
              max="1024"
          /></label>
        </div>
        <button class="primary" @click="saveLogging()">保存日志策略</button>
        <h3>临时 DEBUG</h3>
        <div class="form-grid">
          <label
            >范围<select v-model="debugScope">
              <option value="module">指定模块</option>
              <option value="site">指定站点</option>
            </select></label
          >
          <label v-if="debugScope === 'module'"
            >模块<input v-model="debugID" placeholder="如 download、import、fnos"
          /></label>
          <label v-else
            >站点<select v-model="debugID">
              <option value="">请选择站点</option>
              <option v-for="site in form.sites" :key="site.id" :value="site.id">
                {{ site.name || site.id }}
              </option>
            </select></label
          >
        </div>
        <div class="button-row">
          <button @click="debug(true)">为此范围开启 DEBUG 30 分钟</button
          ><button @click="debug(false)">关闭此范围 DEBUG</button>
        </div>
        <div
          v-for="override in logging.debug_overrides || []"
          :key="override.scope + ':' + override.id"
          class="notice"
        >
          {{ override.scope === 'site' ? '站点' : '模块' }} {{ override.id }} · 截止
          {{ date(override.expires_at) }}
          <button class="small" @click="debug(false, override.scope, override.id)">关闭</button>
        </div>
        <p class="hint">临时调试到期自动关闭，重启后清除。只为指定模块或站点输出额外细节。</p>
        <p v-if="logging.health?.write_error" class="error-box">
          日志写入失败：{{ logging.health.write_error }}
        </p>
        <p v-if="logging.health?.stdout_error" class="error-box">
          标准输出异常：{{ logging.health.stdout_error }}
        </p>
        <p v-if="logging.health?.cleanup_error" class="error-box">
          日志清理异常：{{ logging.health.cleanup_error }}
        </p>
        <p v-if="logging.health?.retention_error" class="error-box">
          日志保留策略执行异常：{{ logging.health.retention_error }}
        </p>
        <p class="hint">
          队列 {{ bytes(logging.health?.queued_bytes || 0) }} · 实时缓存
          {{ bytes(logging.health?.ring_bytes || 0) }} · 丢弃 DEBUG/INFO/WARN/ERROR：{{
            logging.health?.dropped_debug || 0
          }}
          / {{ logging.health?.dropped_info || 0 }} / {{ logging.health?.dropped_warn || 0 }} /
          {{ logging.health?.dropped_error || 0 }}
        </p>
      </section>
      <section class="panel">
        <div class="section-heading">
          <h2>数据库备份</h2>
          <button class="primary small" @click="backup">立即创建备份</button>
        </div>
        <p class="hint">
          备份数据库与加密设置；迁移时需同时保留服务端主密钥。恢复准备由服务端检查，按返回指引停机完成。
        </p>
        <div class="table-wrap">
          <table>
            <tbody>
              <tr v-for="name in backups" :key="name">
                <td class="mono">{{ name }}</td>
                <td>
                  <a
                    class="button small"
                    :href="'/api/backups/' + encodeURIComponent(name)"
                    download
                    >下载</a
                  >
                </td>
                <td><button class="small" @click="restore(name)">准备恢复</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!backups.length" class="hint">还没有备份。</p>
      </section></template
    >
    <section v-if="testResult" class="panel">
      <div class="section-heading">
        <h2>服务端返回结果</h2>
        <button class="small" @click="testResult = null">收起</button>
      </div>
      <pre class="code-box">{{ JSON.stringify(testResult, null, 2) }}</pre>
    </section>
    <div class="sticky-save">
      <span>修改后保存设置，空白秘密字段保留原值。</span
      ><button class="primary" :disabled="saving" @click="save">保存设置</button>
    </div></template
  >
</template>
