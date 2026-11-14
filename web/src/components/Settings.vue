<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { VSelect } from 'vuetify/components/VSelect';
import { defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue';
import { VSwitch } from 'vuetify/components/VSwitch';
import { api, action, ui, refreshSettings, toast, errorText, date, bytes } from '../api';
import DownloaderManager from './DownloaderManager.vue';
import DirectoryManager from './DirectoryManager.vue';
import ApiTokenSettings from './ApiTokenSettings.vue';
import ProxySettings from './ProxySettings.vue';
import PluginSystemSettings from './PluginSystemSettings.vue';
const RuleManager = defineAsyncComponent(() => import('./RuleManager.vue'));
const ResourceMetadataSettings = defineAsyncComponent(
  () => import('./ResourceMetadataSettings.vue'),
);
const emit = defineEmits<{ organize: [paths: string[]] }>();
function routeTab() {
  const value = new URLSearchParams(location.hash.split('?')[1] || '').get('tab');
  return ['system', 'directory', 'rule', 'maintenance'].includes(value || '') ? value! : 'system';
}
const form = ref<any>(null),
  tab = ref(routeTab()),
  visitedTabs = ref(new Set([routeTab()])),
  testResult = ref<any>(null),
  testing = ref(false),
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
  form.value.app_domain ??= '';
  form.value.site_data_refresh_hours ??= 6;
  form.value.read_site_messages = displayedReadMessages(form.value);
  logging.value = await api('/settings/logging');
}
function displayedReadMessages(settings: any) {
  if (settings.read_site_messages_error) return settings.read_site_messages;
  return settings.effective_read_site_messages ?? settings.read_site_messages ?? true;
}
async function save(section: 'app' | 'tmdb' | 'fnos' | 'site') {
  if (saving.value) return false;
  if (section === 'site' && form.value.read_site_messages_error) {
    toast(form.value.read_site_messages_error, true);
    return false;
  }
  saving.value = true;
  try {
    // Downloaders and directories have their own save actions. Read current
    // values so saving this section cannot overwrite changes in those sections.
    const current = await refreshSettings();
    const c = JSON.parse(JSON.stringify(current));
    delete c.secret_state;
    if (section === 'app') c.app_domain = form.value.app_domain;
    if (section === 'tmdb') {
      c.tmdb = form.value.tmdb;
      c.recognize_plugin_first = form.value.recognize_plugin_first === true;
    }
    if (section === 'site') {
      c.site_data_refresh_hours = form.value.site_data_refresh_hours;
      c.read_site_messages = form.value.read_site_messages;
    }
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
      if (section === 'app') form.value.app_domain = result.app_domain ?? '';
      if (section === 'tmdb') form.value.tmdb = result.tmdb;
      if (section === 'fnos') form.value.fnos = result.fnos;
      if (section === 'site') {
        form.value.site_data_refresh_hours = result.site_data_refresh_hours ?? 6;
        form.value.read_site_messages_error = result.read_site_messages_error;
        form.value.read_site_messages = displayedReadMessages(result);
      }
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
  testResult.value = null;
  testing.value = true;
  try {
    if (!(await save(target))) return;
    const result = await action(() => api('/settings/test', { target }));
    if (result) {
      testResult.value = result;
      const passed = result.status === 'ok' || result.status === 'passed';
      toast(result.message || (passed ? '连接测试成功' : '连接测试失败，请查看测试结果'), !passed);
    }
  } finally {
    testing.value = false;
  }
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
  <div class="tabs settings-tabs">
    <button
      v-for="item in [
        ['system', '系统'],
        ['directory', '目录'],
        ['rule', '规则'],
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
    <div v-if="visitedTabs.has('system')" v-show="tab === 'system'" class="settings-page">
      <ApiTokenSettings
        :configured="!!form.secret_state?.api_token"
        @saved="form.secret_state = $event.secret_state"
      />
      <section class="panel">
        <header class="section-heading"><h2>应用地址</h2></header>
        <div class="form-grid">
          <VTextField
            class="span-two"
            v-model="form.app_domain"
            label="访问地址"
            type="text"
            placeholder="https://media.example.invalid"
          />
        </div>
        <p class="hint">用于插件通知的默认应用链接；留空不生成默认链接。</p>
        <div class="button-row">
          <button class="primary" :disabled="saving" @click="save('app')">保存</button>
        </div>
      </section>
      <section class="panel">
        <header class="section-heading">
          <h2>TMDB 元数据</h2>
          <span class="badge">{{ form.secret_state?.tmdb ? '凭据已保存' : '尚未配置' }}</span>
        </header>
        <div class="form-grid">
          <VTextField
            v-model="form.tmdb.api_key"
            type="text"
            autocomplete="off"
            placeholder="留空保留已保存值"
            label="API Key"
          />
          <VTextField
            v-model="form.tmdb.access_token"
            type="text"
            autocomplete="off"
            placeholder="与 API Key 二选一"
            label="Access Token"
          />
          <VSwitch
            v-model="form.recognize_plugin_first"
            label="优先使用辅助识别"
            :disabled="saving"
            hint="优先使用插件辅助识别影片和剧集"
            persistent-hint
          />
        </div>
        <div class="button-row">
          <button class="primary" :disabled="saving" @click="save('tmdb')">保存</button
          ><button :disabled="testing || saving" @click="test('tmdb')">测试</button>
        </div>
      </section>
      <section class="panel"><DownloaderManager /></section>
      <section class="panel"><ProxySettings /></section>
      <PluginSystemSettings />
      <section class="panel">
        <header class="section-heading"><h2>站点选项</h2></header>
        <div class="form-grid">
          <VSelect
            v-model="form.site_data_refresh_hours"
            label="站点数据刷新间隔"
            :items="[
              { title: '每小时', value: 1 },
              { title: '每 6 小时', value: 6 },
              { title: '每 12 小时', value: 12 },
              { title: '每天', value: 24 },
              { title: '每周', value: 168 },
              { title: '从不', value: 0 },
            ]"
          />
          <VSwitch
            v-model="form.read_site_messages"
            :disabled="!!form.read_site_messages_error"
            label="阅读站点消息"
            color="primary"
            hide-details
          />
        </div>
        <p v-if="form.read_site_messages_error" class="error" role="alert">
          {{ form.read_site_messages_error }}
        </p>
        <div class="button-row">
          <button
            class="primary"
            :disabled="saving || !!form.read_site_messages_error"
            @click="save('site')"
          >
            保存
          </button>
        </div>
      </section>
      <section class="panel">
        <header class="section-heading"><h2>媒体服务器</h2></header>
        <div class="form-grid">
          <VTextField model-value="飞牛影视" readonly label="类型" />
          <label class="check"><input v-model="form.fnos.enabled" type="checkbox" />启用</label>
          <VTextField
            class="span-two"
            v-model="form.fnos.host"
            type="url"
            placeholder="http://192.168.1.10:5666"
            label="地址"
          />
          <VTextField v-model="form.fnos.username" autocomplete="off" label="用户名" />
          <VTextField
            v-model="form.fnos.password"
            type="text"
            autocomplete="new-password"
            :placeholder="form.secret_state?.fnos ? '已保存，留空保留' : '登录密码'"
            label="密码"
          />
        </div>
        <div class="button-row">
          <button class="primary" :disabled="saving" @click="save('fnos')">保存</button
          ><button :disabled="testing || saving" @click="test('fnos')">测试</button>
        </div>
      </section>
    </div>
    <div v-if="visitedTabs.has('directory')" v-show="tab === 'directory'">
      <DirectoryManager @organize="emit('organize', $event)" />
    </div>
    <div v-if="visitedTabs.has('rule')" v-show="tab === 'rule'">
      <RuleManager />
      <ResourceMetadataSettings />
    </div>
    <div v-if="visitedTabs.has('maintenance')" v-show="tab === 'maintenance'" class="settings-page">
      <section v-if="logging" class="panel">
        <h2>日志与保留策略</h2>
        <div class="form-grid">
          <VSelect
            v-model="logging.level"
            label="基础级别"
            :items="[
              { title: 'INFO', value: 'INFO' },
              { title: 'WARN', value: 'WARN' },
              { title: 'ERROR', value: 'ERROR' },
            ]"
          /><VTextField
            v-model.number="logging.rotate_mib"
            type="number"
            min="1"
            max="20"
            label="单文件上限（MiB）"
          /><VTextField
            v-model.number="logging.retain_days"
            type="number"
            min="1"
            max="90"
            label="保留天数"
          />
          <VTextField
            v-model.number="logging.total_mib"
            type="number"
            min="1"
            max="1024"
            label="日志总空间上限（MiB）"
          />
        </div>
        <button class="primary" @click="saveLogging()">保存日志策略</button>
        <h3>临时 DEBUG</h3>
        <div class="form-grid">
          <VSelect
            v-model="debugScope"
            label="范围"
            :items="[
              { title: '指定模块', value: 'module' },
              { title: '指定站点', value: 'site' },
            ]"
          />
          <VTextField
            v-if="debugScope === 'module'"
            v-model="debugID"
            placeholder="如 download、import、fnos"
            label="模块"
          />
          <VSelect
            v-else
            v-model="debugID"
            label="站点"
            :items="[
              { title: '请选择站点', value: '' },
              ...form.sites.map((site: any) => ({
                title: String(site.name || site.id).trim(),
                value: site.id,
              })),
            ]"
          />
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
      <p v-if="testResult.message" role="status">{{ testResult.message }}</p>
      <details>
        <summary>诊断详情</summary>
        <pre class="code-box">{{ JSON.stringify(testResult, null, 2) }}</pre>
      </details>
    </section>
  </template>
</template>
<style scoped>
.settings-tabs {
  margin-bottom: 24px;
}
.settings-page {
  display: grid;
  gap: 24px;
  width: 100%;
}
.settings-page .panel {
  margin: 0;
  padding: 24px;
  border: 1px solid var(--surface-border);
  border-radius: 8px;
  box-shadow: none;
}
.settings-page .section-heading {
  margin-bottom: 24px;
}
.settings-page .section-heading h2,
.settings-page .panel > h2 {
  color: var(--text);
  font-size: 20px;
  font-weight: 500;
  line-height: 1.4;
}
.settings-page .panel > h2 {
  margin: 0 0 24px;
}
.settings-page .panel > h3 {
  padding-top: 24px;
  margin-top: 26px;
  border-top: 1px solid var(--line);
  font-size: 18px;
  font-weight: 500;
}
.settings-page .form-grid {
  gap: 24px;
}
.settings-page .form-grid .check {
  align-self: center;
  min-height: 48px;
  font-size: 16px;
  gap: 12px;
}
.settings-page .notice {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.settings-page .notice .small {
  margin-left: auto;
}
.span-two {
  grid-column: 1 / -1;
}
.panel > .button-row {
  margin-top: 24px;
}
.settings-page .form-grid + .primary {
  margin-top: 24px;
}
@media (max-width: 760px) {
  .settings-page .panel {
    padding: 24px 16px;
  }
  .settings-page .form-grid {
    gap: 24px;
  }
  .settings-page .button-row > button {
    min-height: 42px;
  }
}
@media (max-width: 600px) {
  .settings-page .form-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }
}
</style>
