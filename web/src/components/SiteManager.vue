<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { api, bytes, clone, date, errorText, friendly, refreshSettings, toast, ui } from '../api';
import { downloadSiteExport, type SiteImportResult } from '../features/site-transfer';
import SiteImportDialog from './SiteImportDialog.vue';
import SiteResourceDialog from './SiteResourceDialog.vue';

const data = ref<any>({ sites: [], supported: [] });
const filter = ref('all'),
  query = ref(''),
  editing = ref<any>(null),
  original = ref<any>(null);
const loading = ref(false),
  saving = ref(false),
  mutations = ref(0),
  refreshing = ref<string[]>([]);
const importing = ref(false),
  exporting = ref(false),
  importVisible = ref(false),
  error = ref('');
const menu = ref(''),
  deleting = ref<any>(null),
  history = ref<any>(null),
  statistics = ref(false),
  resources = ref<any>(null);
const credentialTab = ref('cookie'),
  limitEnabled = ref(false),
  sortMode = ref(false),
  dragged = ref('');
const transferBusy = computed(() => importing.value || exporting.value);
const working = computed(
  () =>
    transferBusy.value ||
    loading.value ||
    saving.value ||
    mutations.value > 0 ||
    refreshing.value.length > 0,
);
const transferBlocked = computed(
  () => working.value || !!editing.value || !!deleting.value || importVisible.value,
);
const state = (row: any) =>
  row.connection.status === 'failed'
    ? 'failed'
    : row.connection.status === 'ok'
      ? row.connection.duration_ms >= 5000
        ? 'slow'
        : 'connected'
      : 'unknown';
const statuses: Record<string, string> = {
  all: '全部站点',
  enabled: '已启用',
  disabled: '已停用',
  connected: '连接正常',
  slow: '连接较慢',
  failed: '连接失败',
  unknown: '未检测',
};
const filtered = computed<any[]>(() =>
  data.value.sites.filter((row: any) => {
    if (filter.value === 'enabled' && !row.config.enabled) return false;
    if (filter.value === 'disabled' && row.config.enabled) return false;
    if (
      ['connected', 'slow', 'failed', 'unknown'].includes(filter.value) &&
      state(row) !== filter.value
    )
      return false;
    return (row.config.name + row.config.base_url)
      .toLowerCase()
      .includes(query.value.toLowerCase());
  }),
);
const totals = computed(() =>
  ['uploaded', 'downloaded', 'seeding_bytes', 'seeding'].map((key) => {
    const observed = data.value.sites
      .map((row: any) => row.stats)
      .filter((stats: any) => stats?.[key] != null);
    return {
      key,
      observed: observed.length,
      value: observed.reduce((sum: number, stats: any) => sum + stats[key], 0),
    };
  }),
);
const names: Record<string, string> = {
  uploaded: '累计上传',
  downloaded: '累计下载',
  seeding_bytes: '做种体积',
  seeding: '做种数量',
  ratio: '分享率',
  bonus: '积分 / 魔力',
  leeching: '下载中',
  level: '用户等级',
  joined_at: '加入时间',
};
function value(stats: any, key: string) {
  if (key === 'ratio' && stats?.ratio_infinite) return '∞';
  const n = stats?.[key];
  if (n == null || n === '') return '未提供';
  if (['uploaded', 'downloaded', 'seeding_bytes'].includes(key)) return bytes(n);
  return typeof n === 'number'
    ? n.toLocaleString('zh-CN', { maximumFractionDigits: key === 'ratio' ? 3 : 2 })
    : n;
}
async function load() {
  if (transferBusy.value || loading.value || importVisible.value) return;
  loading.value = true;
  error.value = '';
  try {
    data.value = await api('/sites/manage');
    if (!ui.settings) await refreshSettings();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
function create() {
  if (working.value || importVisible.value) return;
  original.value = null;
  error.value = '';
  credentialTab.value = 'cookie';
  limitEnabled.value = false;
  editing.value = {
    id: '',
    name: '',
    base_url: '',
    cookie: '',
    api_key: '',
    token: '',
    ua: '',
    rss: '',
    enabled: true,
    pri: data.value.sites.length + 1,
    timeout: 25,
    downloader: '',
    daily_budget: 600,
    proxy: false,
    render: false,
    limit_interval: 0,
    limit_count: 0,
    limit_seconds: 0,
  };
}
function edit(row: any) {
  if (working.value || importVisible.value) return;
  original.value = row;
  error.value = '';
  editing.value = clone(row.config);
  editing.value.pri ??=
    data.value.sites.findIndex((site: any) => site.config.id === row.config.id) + 1;
  limitEnabled.value = !!(
    row.config.limit_interval ||
    row.config.limit_count ||
    row.config.limit_seconds
  );
  menu.value = '';
  credentialTab.value =
    row.config.kind === 'mteam' || row.credential_state?.api_key ? 'api' : 'cookie';
}
async function save() {
  if (transferBusy.value || saving.value) return;
  saving.value = true;
  error.value = '';
  try {
    data.value = await api('/sites/manage', {
      ...editing.value,
      ...(!limitEnabled.value ? { limit_interval: 0, limit_count: 0, limit_seconds: 0 } : {}),
    });
    editing.value = null;
    await refreshSettings();
    toast('站点配置已保存');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    saving.value = false;
  }
}
async function toggle(row: any) {
  if (working.value || importVisible.value) return;
  mutations.value++;
  menu.value = '';
  error.value = '';
  try {
    data.value = await api('/sites/manage', { ...row.config, enabled: !row.config.enabled });
    await refreshSettings();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    mutations.value--;
  }
}
function requestDelete(row: any) {
  if (!working.value && !importVisible.value) {
    deleting.value = row;
    menu.value = '';
  }
}
async function remove() {
  if (!deleting.value || working.value) return;
  mutations.value++;
  error.value = '';
  try {
    data.value = await api(
      '/sites/manage/' + encodeURIComponent(deleting.value.config.id),
      undefined,
      'DELETE',
    );
    deleting.value = null;
    await refreshSettings();
    toast('站点已删除');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    mutations.value--;
  }
}
async function refresh(row: any) {
  if (transferBusy.value || importVisible.value || refreshing.value.includes(row.config.id)) return;
  const id = row.config.id;
  refreshing.value.push(id);
  error.value = '';
  try {
    data.value = await api('/sites/manage/' + encodeURIComponent(id) + '/refresh', {});
    const updated = data.value.sites.find((site: any) => site.config.id === id);
    if (updated?.connection.status === 'ok') toast(row.config.name + ' 连接正常');
    else toast(row.config.name + '：' + friendly(updated?.connection.error || '检测失败'), true);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    refreshing.value = refreshing.value.filter((key) => key !== id);
  }
}
async function openHistory(row: any) {
  history.value = { name: row.config.name, row, items: [], loading: true, error: '' };
  try {
    const result = await api('/sites/manage/' + encodeURIComponent(row.config.id) + '/history');
    if (history.value?.row === row) history.value = { ...history.value, ...result, loading: false };
  } catch (e) {
    if (history.value?.row === row) {
      history.value.error = errorText(e);
      history.value.loading = false;
    }
  }
}
function openImport() {
  if (!transferBlocked.value) importVisible.value = true;
}
async function imported(result: SiteImportResult) {
  if (!result.imported) return;
  loading.value = true;
  try {
    data.value = await api('/sites/manage');
    await refreshSettings();
    toast(`已导入 ${result.imported} 个站点配置`);
  } catch (e) {
    error.value = '站点已导入，但刷新配置失败：' + errorText(e);
  } finally {
    loading.value = false;
  }
}
async function exportSites() {
  if (transferBlocked.value) return;
  exporting.value = true;
  error.value = '';
  try {
    await downloadSiteExport();
    toast('站点配置已导出');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    exporting.value = false;
  }
}
function homepage(row: any) {
  return row.config.kind === 'mteam'
    ? row.config.base_url.includes('.io')
      ? 'https://kp.m-team.io'
      : 'https://kp.m-team.cc'
    : row.config.base_url;
}
function startSort() {
  if (working.value || importVisible.value) return;
  filter.value = 'all';
  query.value = '';
  sortMode.value = !sortMode.value;
}
watch([filter, query], () => {
  if (filter.value !== 'all' || query.value) sortMode.value = false;
});
async function reorder(from: string, to: string) {
  if (!sortMode.value || working.value || importVisible.value || from === to) return;
  const rows = [...data.value.sites],
    source = rows.findIndex((row) => row.config.id === from),
    target = rows.findIndex((row) => row.config.id === to);
  if (source < 0 || target < 0) return;
  rows.splice(target, 0, rows.splice(source, 1)[0]);
  mutations.value++;
  error.value = '';
  try {
    data.value = await api(
      '/sites/priorities',
      rows.map((row, index) => ({ id: row.config.id, pri: index + 1 })),
    );
    await refreshSettings();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    mutations.value--;
    dragged.value = '';
  }
}
function browse(row: any) {
  if (!sortMode.value && !working.value && !importVisible.value) resources.value = row.config;
}
onMounted(load);
</script>

<template>
  <section class="site-manager" @keydown.esc="menu = ''">
    <div class="site-toolbar">
      <input v-model="query" aria-label="查找站点" placeholder="搜索站点名称或地址" />
      <select v-model="filter" aria-label="站点状态">
        <option v-for="(title, key) in statuses" :key="key" :value="key">{{ title }}</option>
      </select>
      <button :disabled="working" :aria-pressed="sortMode" @click="startSort">
        {{ sortMode ? '退出排序' : '排序' }}
      </button>
      <button :disabled="working" @click="load">刷新</button>
    </div>
    <p v-if="error" class="error-box" role="alert">{{ error }}</p>
    <p v-if="sortMode" class="sort-notice">
      拖动站点卡片调整优先级，松开后自动保存。<button @click="sortMode = false">退出</button>
    </p>
    <p v-if="loading && !data.sites.length" class="empty">正在加载站点…</p>
    <p v-else-if="!filtered.length" class="empty">
      {{
        data.sites.length
          ? '没有匹配的站点'
          : '暂无站点，点击右下角添加站点，或从 MoviePilot 导入。'
      }}
    </p>
    <div class="site-grid">
      <article
        v-for="(row, index) in filtered"
        :key="row.config.id"
        class="site-card"
        :class="[state(row), { inactive: !row.config.enabled, sortable: sortMode }]"
        :draggable="sortMode && !working"
        :tabindex="sortMode ? -1 : 0"
        :aria-label="row.config.name + ' 资源'"
        @click="browse(row)"
        @keydown.enter.self="browse(row)"
        @dragstart="
          dragged = row.config.id;
          $event.dataTransfer?.setData('text/plain', row.config.id);
        "
        @dragover.prevent
        @drop.prevent="reorder(dragged, row.config.id)"
      >
        <div class="site-card-body">
          <div class="site-card-title">
            <span class="site-monogram">{{ row.config.name.slice(0, 1) }}</span>
            <h2>{{ row.config.name }}</h2>
            <small v-if="!row.config.enabled">已停用</small>
          </div>
          <a
            class="site-url"
            :href="homepage(row)"
            target="_blank"
            rel="noopener noreferrer"
            @click.stop
            >{{ homepage(row) }}</a
          >
          <div class="site-traffic">
            <div>
              <span>↑ {{ value(row.stats, 'uploaded') }}</span
              ><progress
                :value="row.stats?.uploaded || 0"
                :max="Math.max(row.stats?.uploaded || 0, row.stats?.downloaded || 0, 1)"
              ></progress>
            </div>
            <div>
              <span>↓ {{ value(row.stats, 'downloaded') }}</span
              ><progress
                :value="row.stats?.downloaded || 0"
                :max="Math.max(row.stats?.uploaded || 0, row.stats?.downloaded || 0, 1)"
              ></progress>
            </div>
          </div>
        </div>
        <div v-if="!sortMode" class="site-card-actions" @click.stop>
          <button
            class="connection-dot"
            :class="state(row)"
            :aria-label="'测试 ' + row.config.name"
            :title="statuses[state(row)] + ' · 测试连通性'"
            :disabled="working"
            @click="refresh(row)"
          >
            {{ refreshing.includes(row.config.id) ? '…' : '●' }}
          </button>
          <button
            :aria-label="row.config.name + ' 用户数据'"
            title="用户数据"
            @click="openHistory(row)"
          >
            ▥
          </button>
          <button
            :aria-label="'更新 ' + row.config.name + ' 凭据'"
            title="更新 Cookie / UA"
            :disabled="working"
            @click="
              edit(row);
              credentialTab = 'cookie';
            "
          >
            ↻
          </button>
          <div class="site-menu">
            <button
              :aria-label="row.config.name + ' 更多操作'"
              :aria-expanded="menu === row.config.id"
              @click="menu = menu === row.config.id ? '' : row.config.id"
            >
              ⋮
            </button>
            <div v-if="menu === row.config.id" class="site-menu-items">
              <button :disabled="working" @click="edit(row)">编辑</button
              ><button :disabled="working" @click="toggle(row)">
                {{ row.config.enabled ? '停用' : '启用' }}</button
              ><button class="danger" :disabled="working" @click="requestDelete(row)">删除</button>
            </div>
          </div>
        </div>
        <div v-else class="sort-controls" @click.stop>
          <span>{{ index + 1 }}</span
          ><button
            :disabled="working || index === 0"
            :aria-label="'上移 ' + row.config.name"
            @click="reorder(row.config.id, filtered[index - 1].config.id)"
          >
            ↑</button
          ><button
            :disabled="working || index === filtered.length - 1"
            :aria-label="'下移 ' + row.config.name"
            @click="reorder(row.config.id, filtered[index + 1].config.id)"
          >
            ↓
          </button>
        </div>
      </article>
    </div>
    <div class="site-floating-actions">
      <button aria-label="站点统计" title="站点统计" @click="statistics = true">▥</button
      ><button
        aria-label="导出站点"
        title="导出站点（文件包含 Cookie / API Key）"
        :disabled="transferBlocked || !data.sites.length"
        @click="exportSites"
      >
        {{ exporting ? '…' : '⇧' }}</button
      ><button
        aria-label="导入站点"
        title="导入站点"
        :disabled="transferBlocked"
        @click="openImport"
      >
        ⇩</button
      ><button
        class="primary"
        aria-label="添加站点"
        title="添加站点"
        :disabled="working"
        @click="create"
      >
        ＋
      </button>
    </div>
    <SiteImportDialog
      v-if="importVisible"
      @close="importVisible = false"
      @busy="importing = $event"
      @imported="imported"
    />
    <SiteResourceDialog v-if="resources" :site="resources" @close="resources = null" />
    <div v-if="editing" class="manager-backdrop" @click.self="!saving && (editing = null)">
      <form
        class="manager-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="编辑站点配置"
        @submit.prevent="save"
      >
        <header>
          <div>
            <h2>{{ original ? '编辑站点' : '添加站点' }}</h2>
            <small>{{ original?.config.name || '' }}</small>
          </div>
          <button
            type="button"
            class="icon-button"
            aria-label="关闭站点编辑"
            :disabled="saving"
            @click="editing = null"
          >
            ×
          </button>
        </header>
        <div class="manager-form-grid">
          <label class="half"
            >站点网址<input
              v-model.trim="editing.base_url"
              type="url"
              required
              aria-label="站点网址"
              placeholder="https://example.org"
            /><small>输入站点完整地址，自动识别站点类型</small></label
          >
          <label
            >优先级<input
              v-model.number="editing.pri"
              type="number"
              min="0"
              max="10000"
              required
              aria-label="优先级"
            /><small>数字越小越优先</small></label
          >
          <label
            >状态<select v-model="editing.enabled" aria-label="状态">
              <option :value="true">启用</option>
              <option :value="false">停用</option>
            </select></label
          >
          <label class="half"
            >RSS 地址<input
              v-model="editing.rss"
              type="password"
              disabled
              :placeholder="original?.credential_state?.rss ? '已保存，保留原配置' : '未配置'"
            /><small>兼容保留，RSS 调度暂未接入</small></label
          >
          <label
            >超时时间<input
              v-model.number="editing.timeout"
              type="number"
              min="0"
              max="120"
              aria-label="超时时间"
            /><small>秒；0 使用默认值</small></label
          >
          <label
            >下载器<select v-model="editing.downloader" disabled>
              <option value="">默认</option>
              <option v-if="editing.downloader" :value="editing.downloader">
                {{ editing.downloader }}
              </option></select
            ><small>兼容保留，站点指定下载器暂未接入</small></label
          >
          <div class="credential-tabs span-all" role="tablist">
            <button
              type="button"
              role="tab"
              :aria-selected="credentialTab === 'cookie'"
              @click="credentialTab = 'cookie'"
            >
              Cookie</button
            ><button
              type="button"
              role="tab"
              :aria-selected="credentialTab === 'api'"
              @click="credentialTab = 'api'"
            >
              API
            </button>
          </div>
          <template v-if="credentialTab === 'cookie'"
            ><label class="span-all"
              >Cookie<textarea
                v-model="editing.cookie"
                rows="4"
                autocomplete="off"
                aria-label="Cookie"
                :placeholder="
                  original?.credential_state?.cookie || original?.credential_configured
                    ? '已保存，留空保留原凭据'
                    : '填写浏览器登录后的 Cookie'
                "
              /></label
            ><label class="span-all"
              >User-Agent<input
                v-model="editing.ua"
                aria-label="User-Agent"
                placeholder="使用浏览器对应的 User-Agent" /></label
          ></template>
          <template v-else
            ><label class="half"
              >Authorization<input
                v-model="editing.token"
                type="password"
                disabled
                :placeholder="original?.credential_state?.token ? '已保存，保留原配置' : '未配置'"
              /><small>兼容保留，通用 Token 认证暂未接入</small></label
            ><label class="half"
              >API Key<input
                v-model="editing.api_key"
                type="password"
                autocomplete="new-password"
                aria-label="API Key"
                :placeholder="
                  original?.credential_state?.api_key || original?.credential_configured
                    ? '已保存，留空保留原凭据'
                    : '填写站点 API Key'
                " /></label
          ></template>
          <div class="span-all preserved-options">
            <label
              ><input
                type="checkbox"
                v-model="limitEnabled"
                aria-label="限制访问频率"
              />限制访问频率</label
            >
            <div v-if="limitEnabled" class="limit-fields">
              <label
                >单位周期（秒）<input
                  v-model.number="editing.limit_interval"
                  type="number"
                  min="0"
                  aria-label="单位周期（秒）" /></label
              ><label
                >周期内访问次数<input
                  v-model.number="editing.limit_count"
                  type="number"
                  min="0"
                  aria-label="周期内访问次数" /></label
              ><label
                >访问间隔（秒）<input
                  v-model.number="editing.limit_seconds"
                  type="number"
                  min="0"
                  aria-label="访问间隔（秒）"
              /></label>
            </div>
          </div>
          <div class="half preserved-options">
            <label><input type="checkbox" :checked="editing.proxy" disabled />使用代理</label
            ><small>兼容保留，站点独立代理暂未接入</small>
          </div>
          <div class="half preserved-options">
            <label><input type="checkbox" :checked="editing.render" disabled />浏览器模拟</label
            ><small>尚未配置浏览器引擎，暂不可用</small>
          </div>
        </div>
        <p class="manager-note">Cookie / UA 可手动维护；账号密码登录自动获取 Cookie 暂未接入。</p>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <footer>
          <button type="button" :disabled="saving" @click="editing = null">取消</button
          ><button type="submit" class="primary" :disabled="saving">
            {{ saving ? '保存中…' : original ? '保存' : '添加站点' }}
          </button>
        </footer>
      </form>
    </div>
    <div v-if="deleting" class="manager-backdrop" @click.self="!working && (deleting = null)">
      <section
        class="manager-dialog delete-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="删除站点确认"
      >
        <h2>删除站点</h2>
        <p>确定删除「{{ deleting.config.name }}」的连接配置和本应用统计记录？</p>
        <p class="manager-note">站点账户及下载器中的任务不会被删除。</p>
        <p v-if="error" class="error-box">{{ error }}</p>
        <footer>
          <button :disabled="working" @click="deleting = null">取消</button
          ><button class="danger" :disabled="working" @click="remove">删除</button>
        </footer>
      </section>
    </div>
    <div
      v-if="history || statistics"
      class="manager-backdrop"
      @click.self="
        history = null;
        statistics = false;
      "
    >
      <section
        class="manager-dialog manager-wide"
        role="dialog"
        aria-modal="true"
        aria-label="站点数据"
      >
        <header>
          <h2>{{ history ? history.name + ' · 用户数据' : '站点统计' }}</h2>
          <button
            class="icon-button"
            aria-label="关闭站点数据"
            @click="
              history = null;
              statistics = false;
            "
          >
            ×
          </button>
        </header>
        <template v-if="statistics"
          ><div class="site-totals">
            <div v-for="total in totals" :key="total.key">
              <small>{{ names[total.key] }}</small
              ><strong>{{
                total.observed
                  ? total.key === 'seeding'
                    ? total.value
                    : bytes(total.value)
                  : '未获取'
              }}</strong
              ><small>{{ total.observed }} / {{ data.sites.length }} 个站点提供数据</small>
            </div>
          </div></template
        >
        <template v-else-if="history"
          ><p>
            {{ history.row.stats?.username || '账户信息未获取' }} ·
            {{ history.row.stats?.level || '等级未提供' }}
          </p>
          <dl class="site-metrics">
            <div
              v-for="key in [
                'uploaded',
                'downloaded',
                'ratio',
                'seeding',
                'seeding_bytes',
                'bonus',
                'leeching',
              ]"
              :key="key"
            >
              <dt>{{ names[key] }}</dt>
              <dd>{{ value(history.row.stats, key) }}</dd>
            </div>
          </dl>
          <p v-if="history.row.connection.error" class="error-box">
            {{ friendly(history.row.connection.error) }}
          </p>
          <p class="manager-note">
            数据：{{ date(history.row.stats?.captured_at) }} · 近 24 小时请求
            {{ history.row.usage_24h || 0 }} / {{ history.row.config.daily_budget || 600 }}
          </p>
          <h3>历史数据</h3>
          <p v-if="history.loading">正在加载…</p>
          <p v-else-if="history.error" class="error-box">{{ history.error }}</p>
          <p v-else-if="!history.items.length">尚无成功快照，先测试 / 刷新站点数据。</p>
          <div v-else class="manager-table">
            <table>
              <thead>
                <tr>
                  <th>日期</th>
                  <th
                    v-for="key in ['uploaded', 'downloaded', 'ratio', 'seeding', 'bonus']"
                    :key="key"
                  >
                    {{ names[key] }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in history.items" :key="item.stats.captured_at">
                  <td>{{ date(item.stats.captured_at) }}</td>
                  <td
                    v-for="key in ['uploaded', 'downloaded', 'ratio', 'seeding', 'bonus']"
                    :key="key"
                  >
                    {{ value(item.stats, key) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div></template
        >
      </section>
    </div>
  </section>
</template>
<style scoped>
.site-manager {
  padding-bottom: 90px;
}
.site-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 22px;
}
.site-toolbar input {
  max-width: 320px;
  margin-right: auto;
}
.site-toolbar select {
  max-width: 160px;
}
.empty {
  padding: 70px 20px;
  text-align: center;
  color: var(--muted);
}
.site-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(265px, 1fr));
  gap: 16px;
}
.site-card {
  display: flex;
  min-width: 0;
  border: 1px solid var(--line);
  border-top: 3px solid #c4cbd5;
  border-radius: 11px;
  background: var(--surface);
  position: relative;
  cursor: pointer;
  transition: box-shadow 0.15s;
}
.site-card:hover {
  box-shadow: 0 5px 20px #172b4d0e;
}
.site-card.connected {
  border-top-color: #58a879;
}
.site-card.failed {
  border-top-color: var(--danger);
}
.site-card.slow {
  border-top-color: #dba24c;
}
.site-card.inactive {
  opacity: 0.7;
}
.site-card.sortable {
  cursor: grab;
}
.site-card-body {
  padding: 14px;
  min-width: 0;
  flex: 1;
}
.site-card-title {
  display: flex;
  align-items: center;
  gap: 9px;
}
.site-monogram {
  display: grid;
  place-items: center;
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 7px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 19px;
}
.site-card h2 {
  font-size: 16px;
  margin: 0;
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.site-card-title small {
  color: var(--muted);
  white-space: nowrap;
  margin-left: auto;
  font-size: 10px;
}
.site-url {
  display: block;
  margin: 14px 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 12px;
  color: var(--muted);
}
.site-traffic {
  border-top: 1px solid var(--line);
  padding-top: 10px;
}
.site-traffic > div {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 6px;
  font-size: 12px;
}
.site-traffic span {
  min-width: 90px;
}
.site-traffic progress {
  height: 4px;
  width: 100%;
  accent-color: var(--accent);
}
.site-traffic > div:last-child progress {
  accent-color: #47a885;
}
.site-card-actions {
  display: flex;
  flex-direction: column;
  width: 40px;
  padding: 5px 3px;
  background: #f7f9fc;
  border-radius: 0 8px 8px 0;
}
.site-card-actions button {
  border: 0;
  background: transparent;
  padding: 6px;
  box-shadow: none;
  font-size: 19px;
}
.connection-dot {
  color: #9aa7b7;
}
.connection-dot.connected {
  color: #4d9b6f;
}
.connection-dot.failed {
  color: var(--danger);
}
.connection-dot.slow {
  color: #c28c2c;
}
.site-menu {
  margin-top: auto;
  position: relative;
}
.site-menu > button {
  width: 100%;
}
.site-menu-items {
  position: absolute;
  right: 34px;
  bottom: 0;
  z-index: 5;
  min-width: 110px;
  background: var(--surface);
  border: 1px solid var(--line);
  padding: 5px;
  border-radius: 8px;
  box-shadow: 0 8px 25px #172b4d1a;
}
.site-menu-items button {
  width: 100%;
  justify-content: flex-start;
  font-size: 13px;
  padding: 9px 12px;
}
.site-floating-actions {
  position: fixed;
  right: 28px;
  bottom: 26px;
  display: flex;
  flex-direction: column;
  gap: 9px;
  z-index: 50;
}
.site-floating-actions button {
  width: 44px;
  height: 44px;
  padding: 0;
  border-radius: 50%;
  font-size: 23px;
  box-shadow: 0 4px 14px #172b4d1f;
}
.sort-notice {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 13px 16px;
  background: #fff5df;
  border-radius: 9px;
  font-size: 13px;
}
.sort-controls {
  display: flex;
  flex-direction: column;
  padding: 8px;
  justify-content: center;
  gap: 6px;
}
.sort-controls button {
  padding: 3px 7px;
}
.sort-controls span {
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}
.manager-backdrop {
  position: fixed;
  inset: 0;
  background: #12233270;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.manager-dialog {
  width: 720px;
  max-width: 100%;
  max-height: calc(100dvh - 40px);
  overflow: auto;
  background: var(--surface);
  padding: 24px;
  border-radius: 12px;
  box-shadow: 0 24px 80px #0003;
}
.manager-dialog header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 22px;
}
.manager-dialog h2 {
  margin: 0;
  font-size: 19px;
}
.manager-dialog header small {
  display: block;
  margin-top: 5px;
  color: var(--muted);
}
.manager-dialog footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 22px;
}
.manager-form-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 20px 14px;
}
.manager-form-grid > label {
  display: grid;
  align-content: start;
  gap: 8px;
  font-size: 12px;
}
.half {
  grid-column: span 2;
}
.span-all {
  grid-column: 1/-1;
}
.manager-form-grid small,
.manager-note {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.7;
}
.credential-tabs {
  display: flex;
  gap: 7px;
  border-bottom: 1px solid var(--line);
  padding-bottom: 8px;
}
.credential-tabs button {
  border: 0;
  background: transparent;
}
.credential-tabs button[aria-selected='true'] {
  background: var(--accent-soft);
  color: var(--accent);
}
.preserved-options > label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.preserved-options input[type='checkbox'] {
  width: 16px;
  height: 16px;
}
.preserved-options > small {
  display: block;
  margin-top: 5px;
}
.limit-fields {
  display: flex;
  gap: 10px;
  margin-top: 8px;
  font-size: 12px;
}
.limit-fields label {
  min-width: 0;
}
.delete-dialog {
  width: 460px;
}
.manager-wide {
  width: 900px;
}
.site-metrics {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
  margin: 22px 0;
}
.site-metrics dt {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 7px;
}
.site-metrics dd {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
}
.site-totals {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 15px;
}
.site-totals > div {
  display: grid;
  gap: 10px;
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: 9px;
}
.site-totals strong {
  font-size: 23px;
}
.site-totals small {
  color: var(--muted);
}
.manager-table {
  overflow: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
th,
td {
  padding: 12px;
  border-bottom: 1px solid var(--line);
  text-align: left;
  white-space: nowrap;
}
@media (max-width: 650px) {
  .site-toolbar {
    flex-wrap: wrap;
  }
  .site-toolbar input {
    flex-basis: 100%;
    max-width: none;
  }
  .site-grid {
    grid-template-columns: 1fr;
  }
  .manager-backdrop {
    padding: 10px;
  }
  .manager-dialog {
    padding: 18px;
    max-height: calc(100dvh - 20px);
  }
  .manager-form-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .half {
    grid-column: span 2;
  }
  .site-floating-actions {
    right: 18px;
    bottom: 22px;
  }
  .site-metrics {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
