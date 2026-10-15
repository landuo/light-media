<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { api, action, ui, refreshSettings, toast, errorText, date, bytes } from '../api';
import DownloaderManager from './DownloaderManager.vue';
import DirectoryManager from './DirectoryManager.vue';
const emit = defineEmits<{ organize: [paths: string[]] }>();
function routeTab() {
  const value = new URLSearchParams(location.hash.split('?')[1] || '').get('tab');
  return ['system', 'directory', 'maintenance'].includes(value || '') ? value! : 'system';
}
const form = ref<any>(null),
  tab = ref(routeTab()),
  visitedTabs = ref(new Set([routeTab()])),
  testResult = ref<any>(null),
  saving = ref(false),
  logging = ref<any>(null),
  debugScope = ref('module'),
  debugID = ref('download');
function syncTab() {
  tab.value = routeTab();
  visitedTabs.value.add(tab.value);
}
function selectTab(value: string) {
  tab.value = value;
  visitedTabs.value.add(value);
  location.hash = 'settings?tab=' + value;
}
async function load() {
  form.value = JSON.parse(JSON.stringify(await refreshSettings()));
  form.value.tmdb ||= { api_key: '', access_token: '' };
  form.value.fnos ||= { host: '', username: '', password: '', enabled: false };
  logging.value = await api('/settings/logging');
}
async function save(section: 'tmdb' | 'fnos') {
  saving.value = true;
  try {
    // Downloaders and directories have their own save actions. Read current
    // values so saving this section cannot overwrite changes in those sections.
    const current = await refreshSettings();
    const c = JSON.parse(JSON.stringify(current));
    delete c.secret_state;
    if (section === 'tmdb') c.tmdb = form.value.tmdb;
    if (section === 'fnos')
      c.fnos = {
        ...c.fnos,
        host: form.value.fnos.host,
        username: form.value.fnos.username,
        password: form.value.fnos.password,
        enabled: form.value.fnos.enabled,
      };
    const result = await action(() => api('/settings', c, 'PUT'), '设置已保存');
    if (result) {
      ui.settings = result;
      form.value.secret_state = result.secret_state;
      if (section === 'tmdb') form.value.tmdb = result.tmdb;
      if (section === 'fnos') form.value.fnos = result.fnos;
      return true;
    }
    return false;
  } catch (e) {
    toast(errorText(e), true);
    return false;
  } finally {
    saving.value = false;
  }
}
async function test(target: 'tmdb' | 'fnos') {
  if (await save(target)) testResult.value = await action(() => api('/settings/test', { target }));
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
  if (result) logging.value = result;
}
async function debug(enable: boolean, scope = debugScope.value, id = debugID.value) {
  if (!id.trim()) {
    toast('请选择需要调试的模块或站点', true);
    return;
  }
  await saveLogging({ scope, id: id.trim(), minutes: 30, enabled: enable });
}
onMounted(() => {
  window.addEventListener('hashchange', syncTab);
  action(load);
});
onBeforeUnmount(() => window.removeEventListener('hashchange', syncTab));
</script>
<template>
  <div class="tabs">
    <button
      v-for="item in [
        ['system', '系统'],
        ['directory', '目录'],
        ['maintenance', '维护'],
      ]"
      :key="item[0]"
      :class="{ selected: tab === item[0] }"
      @click="selectTab(item[0]!)"
    >
      {{ item[1] }}
    </button>
  </div>
  <template v-if="form">
    <div v-if="visitedTabs.has('system')" v-show="tab === 'system'">
      <section class="panel">
        <header class="section-heading">
          <h2>TMDB 元数据</h2>
          <span class="badge">{{ form.secret_state?.tmdb ? '凭据已保存' : '尚未配置' }}</span>
        </header>
        <div class="form-grid">
          <label
            >API Key<input
              v-model="form.tmdb.api_key"
              type="password"
              autocomplete="off"
              placeholder="留空保留已保存值"
          /></label>
          <label
            >Access Token<input
              v-model="form.tmdb.access_token"
              type="password"
              autocomplete="off"
              placeholder="与 API Key 二选一"
          /></label>
        </div>
        <div class="button-row">
          <button class="primary" :disabled="saving" @click="save('tmdb')">保存</button
          ><button @click="test('tmdb')">测试</button>
        </div>
      </section>
      <section class="panel"><DownloaderManager /></section>
      <section class="panel">
        <header class="section-heading"><h2>媒体服务器</h2></header>
        <div class="form-grid">
          <label>类型<input value="飞牛影视" readonly /></label>
          <label class="check"><input v-model="form.fnos.enabled" type="checkbox" />启用</label>
          <label class="span-two"
            >地址<input
              v-model="form.fnos.host"
              type="url"
              placeholder="https://nas.example.com:8080/v"
          /></label>
          <label>用户名<input v-model="form.fnos.username" autocomplete="off" /></label>
          <label
            >密码<input
              v-model="form.fnos.password"
              type="password"
              autocomplete="new-password"
              :placeholder="form.secret_state?.fnos ? '已保存，留空保留' : '登录密码'"
          /></label>
        </div>
        <div class="button-row">
          <button class="primary" :disabled="saving" @click="save('fnos')">保存</button
          ><button @click="test('fnos')">测试</button>
        </div>
      </section>
    </div>
    <div v-if="visitedTabs.has('directory')" v-show="tab === 'directory'">
      <DirectoryManager @organize="emit('organize', $event)" />
    </div>
    <div v-if="visitedTabs.has('maintenance')" v-show="tab === 'maintenance'">
      <section v-if="logging" class="panel">
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
    </div>
    <section v-if="testResult" class="panel">
      <header class="section-heading">
        <h2>测试结果</h2>
        <button class="small" @click="testResult = null">收起</button>
      </header>
      <pre class="code-box">{{ JSON.stringify(testResult, null, 2) }}</pre>
    </section>
  </template>
</template>
<style scoped>
.span-two {
  grid-column: 1 / -1;
}
.panel > .button-row {
  margin-top: 20px;
}
</style>
