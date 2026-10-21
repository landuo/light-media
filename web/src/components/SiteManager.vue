<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, bytes, clone, date, errorText, friendly, refreshSettings, toast, ui } from '../api';
import { downloadSiteExport, type SiteImportResult } from '../features/site-transfer';
import SiteImportDialog from './SiteImportDialog.vue';
import SiteResourceDialog from './SiteResourceDialog.vue';
import AppIcon from './AppIcon.vue';

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
type SiteTestState = { status: 'queued' | 'testing' | 'ok' | 'failed'; message: string };
type SiteTestJob = { id: string; name: string; quiet: boolean; resolve: (ok: boolean) => void };
const siteTests = ref<Record<string, SiteTestState>>({});
const batch = ref({ running: false, ids: [] as string[], done: 0, ok: 0, failed: 0 });
const testQueue: SiteTestJob[] = [];
const testRequests = new Map<string, Promise<boolean>>();
let activeTests = 0,
  testReloadNeeded = false,
  disposed = false;
const transferBusy = computed(() => importing.value || exporting.value);
const testBlocked = computed(
  () =>
    transferBusy.value ||
    loading.value ||
    saving.value ||
    mutations.value > 0 ||
    importVisible.value ||
    !!editing.value ||
    !!deleting.value ||
    sortMode.value,
);
const working = computed(
  () =>
    transferBusy.value ||
    loading.value ||
    saving.value ||
    mutations.value > 0 ||
    refreshing.value.length > 0 ||
    batch.value.running,
);
const transferBlocked = computed(
  () => working.value || !!editing.value || !!deleting.value || importVisible.value,
);
const savedState = (row: any) => {
  return row.connection.status === 'failed'
    ? 'failed'
    : row.connection.status === 'ok'
      ? row.connection.duration_ms >= 5000
        ? 'slow'
        : 'connected'
      : 'unknown';
};
const state = (row: any) => {
  const test = siteTests.value[row.config.id];
  return test && test.status !== 'ok' ? test.status : savedState(row);
};
const connectionLabel = (row: any) => {
  const current = state(row);
  const message =
    current === 'queued' ? '排队中' : current === 'testing' ? '测试中…' : statuses[current];
  return row.config.enabled
    ? message
    : '已停用' + (siteTests.value[row.config.id] ? ' · ' + message : '');
};
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
    const test = siteTests.value[row.config.id];
    const current =
      test?.status === 'queued' || test?.status === 'testing' ? savedState(row) : state(row);
    if (
      ['connected', 'slow', 'failed', 'unknown'].includes(filter.value) &&
      current !== filter.value
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
  if (transferBusy.value || loading.value || importVisible.value || refreshing.value.length) return;
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
  editing.value = { timeout: 0, downloader: '', ...clone(row.config) };
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
function refresh(row: any, quiet = false): Promise<boolean> {
  const id = row.config.id;
  const pending = testRequests.get(id);
  if (pending) return pending;
  if (disposed || testBlocked.value) return Promise.resolve(false);
  let finish!: (ok: boolean) => void;
  const promise = new Promise<boolean>((resolve) => {
    finish = resolve;
  });
  testRequests.set(id, promise);
  refreshing.value.push(id);
  siteTests.value[id] = { status: 'queued', message: '' };
  error.value = '';
  testReloadNeeded = true;
  testQueue.push({ id, name: row.config.name, quiet, resolve: finish });
  pumpTests();
  return promise;
}
function pumpTests() {
  while (!disposed && activeTests < 3 && testQueue.length) {
    const job = testQueue.shift()!;
    activeTests++;
    void runTest(job);
  }
  if (!disposed && activeTests === 0 && !testQueue.length && testReloadNeeded) {
    testReloadNeeded = false;
    void load();
  }
}
async function runTest(job: SiteTestJob) {
  let ok = false;
  siteTests.value[job.id] = { status: 'testing', message: '' };
  try {
    const result = await api('/sites/manage/' + encodeURIComponent(job.id) + '/refresh', {});
    if (disposed) return;
    const updated = result.sites?.find((site: any) => site.config.id === job.id);
    if (!updated || !data.value.sites.some((site: any) => site.config.id === job.id))
      throw new Error('站点已变化，请刷新后重试');
    // Concurrent endpoints return full snapshots. Only this request's row is
    // authoritative; replacing the table can undo another completed test.
    data.value.sites = data.value.sites.map((site: any) =>
      site.config.id === job.id ? updated : site,
    );
    ok = updated.connection?.status === 'ok';
    siteTests.value[job.id] = {
      status: ok ? 'ok' : 'failed',
      message: ok ? '连接正常' : friendly(updated.connection?.error || '检测失败'),
    };
    if (!job.quiet && !batch.value.running)
      toast(job.name + '：' + siteTests.value[job.id]!.message, !ok);
  } catch (e) {
    if (!disposed) {
      siteTests.value[job.id] = { status: 'failed', message: errorText(e) };
      if (!job.quiet && !batch.value.running) toast(job.name + '：' + errorText(e), true);
    }
  } finally {
    activeTests--;
    testRequests.delete(job.id);
    refreshing.value = refreshing.value.filter((key) => key !== job.id);
    job.resolve(ok);
    pumpTests();
  }
}
async function refreshVisible() {
  if (testBlocked.value || batch.value.running || !filtered.value.length) return;
  const targets = [...filtered.value];
  batch.value = {
    running: true,
    ids: targets.map((row) => row.config.id),
    done: 0,
    ok: 0,
    failed: 0,
  };
  await Promise.all(
    targets.map(async (row) => {
      const ok = await refresh(row, true);
      if (disposed) return;
      batch.value.done++;
      if (ok) batch.value.ok++;
      else batch.value.failed++;
    }),
  );
  if (disposed) return;
  batch.value.running = false;
  toast(`站点测试完成：成功 ${batch.value.ok}，失败 ${batch.value.failed}`, batch.value.failed > 0);
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
onUnmounted(() => {
  disposed = true;
  testReloadNeeded = false;
  for (const job of testQueue.splice(0)) {
    testRequests.delete(job.id);
    job.resolve(false);
  }
});
</script>

<template>
  <section class="site-manager" @keydown.esc="menu = ''">
    <div class="site-toolbar">
      <div class="site-search">
        <AppIcon name="search" />
        <input v-model="query" aria-label="查找站点" placeholder="搜索站点名称或地址" />
      </div>
      <select v-model="filter" aria-label="站点状态">
        <option v-for="(title, key) in statuses" :key="key" :value="key">{{ title }}</option>
      </select>
      <button
        :disabled="testBlocked || batch.running || !filtered.length"
        aria-label="测试连接"
        title="测试当前列表中的站点"
        @click="refreshVisible"
      >
        <AppIcon :name="batch.running ? 'refresh' : 'check'" :class="{ spinning: batch.running }" />
        {{ batch.running ? '测试中…' : '测试连接' }}
      </button>
      <button
        :disabled="working"
        :aria-pressed="sortMode"
        :aria-label="sortMode ? '退出排序' : '排序'"
        @click="startSort"
      >
        <AppIcon name="sort" />
        {{ sortMode ? '退出排序' : '排序' }}
      </button>
      <button :disabled="working" aria-label="刷新" @click="load">
        <AppIcon name="refresh" />刷新
      </button>
    </div>
    <div
      v-if="batch.ids.length"
      class="site-test-summary"
      role="status"
      aria-live="polite"
      aria-label="站点连接测试进度"
    >
      <span
        >{{ batch.running ? '正在测试' : '测试完成' }} · 已完成 {{ batch.done }} /
        {{ batch.ids.length }}</span
      >
      <span class="test-success">成功 {{ batch.ok }}</span>
      <span :class="{ 'test-failure': batch.failed }">失败 {{ batch.failed }}</span>
      <progress
        :value="batch.done"
        :max="batch.ids.length"
        aria-label="批量测试完成进度"
      ></progress>
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
            <div class="site-card-identity">
              <h2>{{ row.config.name }}</h2>
              <span
                class="site-status"
                :class="!row.config.enabled && !siteTests[row.config.id] ? 'disabled' : state(row)"
              >
                <span aria-hidden="true"></span>{{ connectionLabel(row) }}
              </span>
            </div>
          </div>
          <a
            class="site-url"
            :href="homepage(row)"
            target="_blank"
            rel="noopener noreferrer"
            @click.stop
            ><span>{{ homepage(row) }}</span
            ><AppIcon name="external"
          /></a>
          <p
            v-if="siteTests[row.config.id]?.status === 'failed'"
            class="site-test-error"
            :aria-label="row.config.name + ' 测试结果'"
            :title="siteTests[row.config.id]!.message"
          >
            {{ siteTests[row.config.id]!.message }}
          </p>
          <div class="site-traffic">
            <div>
              <span class="traffic-label"><AppIcon name="arrow-up" />上传</span
              ><strong>{{ value(row.stats, 'uploaded') }}</strong
              ><progress
                :value="row.stats?.uploaded || 0"
                :max="Math.max(row.stats?.uploaded || 0, row.stats?.downloaded || 0, 1)"
              ></progress>
            </div>
            <div>
              <span class="traffic-label"><AppIcon name="arrow-down" />下载</span
              ><strong>{{ value(row.stats, 'downloaded') }}</strong
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
            :title="connectionLabel(row) + ' · 测试连通性'"
            :disabled="testBlocked || refreshing.includes(row.config.id)"
            :aria-busy="siteTests[row.config.id]?.status === 'testing'"
            @click="refresh(row)"
          >
            <AppIcon
              :name="
                siteTests[row.config.id]?.status === 'testing'
                  ? 'refresh'
                  : state(row) === 'connected'
                    ? 'check'
                    : state(row) === 'failed'
                      ? 'warning'
                      : 'clock'
              "
              :class="{ spinning: siteTests[row.config.id]?.status === 'testing' }"
            />
          </button>
          <button
            :aria-label="row.config.name + ' 用户数据'"
            title="用户数据"
            @click="openHistory(row)"
          >
            <AppIcon name="chart" />
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
            <AppIcon name="refresh" />
          </button>
          <div class="site-menu">
            <button
              :aria-label="row.config.name + ' 更多操作'"
              :aria-expanded="menu === row.config.id"
              @click="menu = menu === row.config.id ? '' : row.config.id"
            >
              <AppIcon name="more" />
            </button>
            <div v-if="menu === row.config.id" class="site-menu-items">
              <button :disabled="working" aria-label="编辑" @click="edit(row)">编辑</button
              ><button
                :disabled="working"
                :aria-label="row.config.enabled ? '停用' : '启用'"
                @click="toggle(row)"
              >
                {{ row.config.enabled ? '停用' : '启用' }}</button
              ><button
                class="danger"
                :disabled="working"
                aria-label="删除"
                @click="requestDelete(row)"
              >
                删除
              </button>
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
            <AppIcon name="arrow-up" /></button
          ><button
            :disabled="working || index === filtered.length - 1"
            :aria-label="'下移 ' + row.config.name"
            @click="reorder(row.config.id, filtered[index + 1].config.id)"
          >
            <AppIcon name="arrow-down" />
          </button>
        </div>
      </article>
    </div>
    <div class="site-floating-actions">
      <button aria-label="站点统计" title="站点统计" @click="statistics = true">
        <AppIcon name="chart" /></button
      ><button
        aria-label="导出站点"
        title="导出站点（文件包含 Cookie / API Key）"
        :disabled="transferBlocked || !data.sites.length"
        @click="exportSites"
      >
        <AppIcon
          :name="exporting ? 'refresh' : 'upload'"
          :class="{ spinning: exporting }"
        /></button
      ><button
        aria-label="导入站点"
        title="导入站点"
        :disabled="transferBlocked"
        @click="openImport"
      >
        <AppIcon name="download" /></button
      ><button
        class="primary"
        aria-label="添加站点"
        title="添加站点"
        :disabled="working"
        @click="create"
      >
        <AppIcon name="plus" />
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
        class="manager-dialog site-editor"
        role="dialog"
        aria-modal="true"
        aria-label="编辑站点配置"
        @submit.prevent="save"
      >
        <header>
          <div class="site-editor-heading">
            <AppIcon name="globe" />
            <div>
              <h2>{{ original ? '编辑站点' : '添加站点' }}</h2>
              <small>{{ original?.config.name || '' }}</small>
            </div>
          </div>
          <button
            type="button"
            class="icon-button"
            aria-label="关闭站点编辑"
            :disabled="saving"
            @click="editing = null"
          >
            <AppIcon name="close" />
          </button>
        </header>
        <div class="site-editor-body">
          <div class="manager-form-grid">
            <label class="half"
              ><span class="field-label">站点网址</span
              ><input
                v-model.trim="editing.base_url"
                type="url"
                required
                aria-label="站点网址"
                placeholder="https://example.org"
              /><small>输入站点完整地址，自动识别站点类型</small></label
            >
            <label
              ><span class="field-label">优先级</span
              ><input
                v-model.number="editing.pri"
                type="number"
                min="0"
                max="10000"
                required
                aria-label="优先级"
              /><small>数字越小越优先</small></label
            >
            <label
              ><span class="field-label">状态</span
              ><select v-model="editing.enabled" aria-label="状态">
                <option :value="true">启用</option>
                <option :value="false">停用</option>
              </select></label
            >
            <label class="half"
              ><span class="field-label">RSS 地址</span
              ><input
                v-model="editing.rss"
                type="password"
                disabled
                :placeholder="original?.credential_state?.rss ? '已保存，保留原配置' : '未配置'"
              /><small>暂不支持 RSS 调度</small></label
            >
            <label
              ><span class="field-label">超时时间</span
              ><input
                v-model.number="editing.timeout"
                type="number"
                min="0"
                max="120"
                aria-label="超时时间"
              /><small>秒；0 使用默认值</small></label
            >
            <label
              ><span class="field-label">下载器</span
              ><select v-model="editing.downloader" disabled>
                <option value="">默认</option>
                <option v-if="editing.downloader" :value="editing.downloader">
                  {{ editing.downloader }}
                </option></select
              ><small>暂不支持站点绑定</small></label
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
                ><span class="field-label">Cookie</span
                ><textarea
                  v-model="editing.cookie"
                  rows="3"
                  autocomplete="off"
                  aria-label="Cookie"
                  :placeholder="
                    original?.credential_state?.cookie || original?.credential_configured
                      ? '已保存，留空保留原凭据'
                      : '填写浏览器登录后的 Cookie'
                  "
                /></label
              ><label class="span-all"
                ><span class="field-label">User-Agent</span
                ><input
                  v-model="editing.ua"
                  aria-label="User-Agent"
                  placeholder="使用浏览器对应的 User-Agent" /></label
            ></template>
            <template v-else
              ><label class="half"
                ><span class="field-label">Authorization</span
                ><input
                  v-model="editing.token"
                  type="password"
                  disabled
                  :placeholder="original?.credential_state?.token ? '已保存，保留原配置' : '未配置'"
                /><small>暂不支持 Token 认证</small></label
              ><label class="half"
                ><span class="field-label">API Key</span
                ><input
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
            <div class="span-all site-option-list">
              <label class="site-option-row">
                <span class="site-option-copy">
                  <span class="site-option-title">限制访问频率</span>
                  <small>控制请求间隔和单位周期内的访问次数</small>
                </span>
                <input
                  class="site-switch"
                  type="checkbox"
                  role="switch"
                  v-model="limitEnabled"
                  aria-label="限制访问频率"
                />
              </label>
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
              <label class="site-option-row site-option-unavailable">
                <span class="site-option-copy">
                  <span class="site-option-title"
                    >使用代理 <span class="option-unavailable">暂不可用</span></span
                  >
                  <small>站点独立代理尚未接入</small>
                </span>
                <input
                  class="site-switch"
                  type="checkbox"
                  role="switch"
                  :checked="editing.proxy"
                  aria-label="使用代理"
                  disabled
                />
              </label>
              <label class="site-option-row site-option-unavailable">
                <span class="site-option-copy">
                  <span class="site-option-title"
                    >浏览器模拟 <span class="option-unavailable">暂不可用</span></span
                  >
                  <small>浏览器验证引擎尚未接入</small>
                </span>
                <input
                  class="site-switch"
                  type="checkbox"
                  role="switch"
                  :checked="editing.render"
                  aria-label="浏览器模拟"
                  disabled
                />
              </label>
            </div>
          </div>
        </div>
        <p v-if="error" class="error-box site-editor-error" role="alert">{{ error }}</p>
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
            <AppIcon name="close" />
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
                <tr
                  v-for="(item, index) in history.items"
                  :key="`${item.stats.captured_at}:${index}`"
                >
                  <td>
                    {{ date(item.stats.captured_at) }}
                    <small v-if="item.connection?.status === 'failed'" class="history-error">
                      {{ item.connection.error || '刷新失败' }}
                    </small>
                  </td>
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
.history-error {
  display: block;
  color: var(--danger, #b3261e);
  max-width: 24rem;
  overflow-wrap: anywhere;
}
.site-manager {
  padding-bottom: 90px;
}
.site-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 16px;
}
.site-search {
  position: relative;
  width: min(360px, 100%);
  margin-right: auto;
}
.site-search > .app-icon {
  position: absolute;
  left: 13px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--muted);
  pointer-events: none;
  width: 18px;
  height: 18px;
}
.site-search input {
  padding-left: 41px;
}
.site-toolbar select {
  width: 154px;
}
.site-toolbar button {
  gap: 7px;
  border-color: transparent;
  background: transparent;
  box-shadow: none;
}
.site-toolbar button .app-icon {
  width: 17px;
  height: 17px;
}
.site-toolbar button[aria-pressed='true'] {
  background: var(--accent-soft);
  border-color: var(--accent);
  color: var(--accent);
}
.site-test-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  padding: 12px 16px;
  margin: -8px 0 20px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  color: var(--muted);
  font-size: 13px;
}
.site-test-summary > span:first-child {
  color: var(--text);
  margin-right: auto;
}
.site-test-summary progress {
  width: 100%;
  height: 4px;
  accent-color: var(--accent);
}
.test-success {
  color: var(--success, var(--accent));
}
.test-failure,
.site-test-error {
  color: var(--danger);
}
.site-test-error {
  margin: 0 0 12px;
  font-size: 12px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.empty {
  padding: 70px 20px;
  text-align: center;
  color: var(--muted);
}
.site-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(256px, 1fr));
  gap: 16px;
  padding: 0 8px 16px;
}
.site-card {
  display: flex;
  min-width: 0;
  min-height: 168px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  position: relative;
  cursor: pointer;
  box-shadow: none;
  transition:
    box-shadow 0.18s,
    border-color 0.18s,
    transform 0.18s;
}
.site-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 8px;
  right: 8px;
  height: 2px;
  opacity: 0.5;
  background: linear-gradient(90deg, transparent, var(--muted), transparent);
}
.site-card.connected::before {
  background: linear-gradient(90deg, transparent, var(--success, var(--accent)), transparent);
}
.site-card.failed::before {
  background: linear-gradient(90deg, transparent, var(--danger), transparent);
}
.site-card.slow::before {
  background: linear-gradient(90deg, transparent, var(--warning, var(--accent)), transparent);
}
.site-card:focus-visible {
  outline: 3px solid var(--accent-soft);
  outline-offset: 3px;
}
.site-card:hover {
  transform: translateY(-4px);
}
.site-card.inactive {
  opacity: 0.7;
}
.site-card.inactive::before {
  display: none;
}
.site-card.inactive .site-monogram {
  color: var(--muted);
  background: color-mix(in srgb, var(--line) 65%, var(--surface));
}
.site-card.sortable {
  cursor: grab;
}
.site-card.sortable:hover {
  transform: none;
}
.site-card-body {
  padding: 12px 48px 12px 12px;
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
}
.site-card-title {
  display: flex;
  align-items: center;
  gap: 8px;
}
.site-card-identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}
.site-monogram {
  display: grid;
  place-items: center;
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 18px;
  font-weight: 600;
}
.site-card h2 {
  font-size: 18px;
  line-height: 1.25;
  font-weight: 600;
  margin: 0;
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.site-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  white-space: nowrap;
  font-size: 11px;
}
.site-status > span {
  width: 5px;
  height: 5px;
  background: currentColor;
  border-radius: 50%;
}
.site-status.connected,
.site-status.testing,
.site-status.queued {
  color: var(--accent);
}
.site-status.connected {
  color: var(--success, var(--accent));
}
.site-status.failed {
  color: var(--danger);
}
.site-status.slow {
  color: var(--warning, var(--accent));
}
.site-url {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 12px 0;
  font-size: 13px;
  color: var(--muted);
}
.site-url span {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.site-url .app-icon {
  width: 12px;
  height: 12px;
  opacity: 0;
  transition: opacity 0.15s;
}
.site-card:hover .site-url .app-icon,
.site-url:focus-visible .app-icon {
  opacity: 1;
}
.site-traffic {
  border-top: 1px solid var(--line);
  padding-top: 6px;
  margin-top: auto;
  display: grid;
  gap: 6px;
}
.site-traffic > div {
  display: grid;
  grid-template-columns: 14px minmax(62px, auto) minmax(0, 1fr);
  align-items: center;
  gap: 4px;
  font-size: 12px;
}
.traffic-label {
  display: inline-flex;
  gap: 5px;
  align-items: center;
  color: var(--info, var(--accent));
  font-size: 0;
}
.traffic-label .app-icon {
  width: 14px;
  height: 14px;
}
.site-traffic strong {
  font-weight: 400;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.site-traffic progress {
  height: 4px;
  width: 100%;
  accent-color: var(--info, var(--accent));
  border: 0;
  border-radius: 4px;
  overflow: hidden;
  background: var(--line);
}
.site-traffic progress::-webkit-progress-bar {
  background: var(--line);
}
.site-traffic progress::-webkit-progress-value {
  background: var(--info, var(--accent));
  border-radius: 4px;
}
.site-traffic > div:last-child progress {
  accent-color: var(--warning, var(--accent));
}
.site-traffic > div:last-child progress::-webkit-progress-value {
  background: var(--warning, var(--accent));
}
.site-traffic > div:last-child .traffic-label {
  color: var(--success, var(--accent));
}
.site-card-actions {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  width: 44px;
  padding: 8px 4px;
  background: var(--surface);
  border-radius: 0 8px 8px 0;
  gap: 0;
}
.site-card-actions button {
  border: 0;
  background: transparent;
  width: 36px;
  height: 36px;
  min-height: 36px;
  padding: 8px;
  box-shadow: none;
  color: var(--muted);
  border-radius: 50%;
}
.site-card-actions button:hover {
  color: var(--accent);
  background: var(--accent-soft);
}
.site-card-actions .app-icon {
  width: 20px;
  height: 20px;
}
.site-card-actions .connection-dot {
  position: relative;
  color: var(--muted);
}
.site-card-actions .connection-dot:not([aria-busy='true'])::before {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  left: 11px;
  top: 11px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 15%, transparent);
}
.site-card-actions .connection-dot:not([aria-busy='true']) .app-icon {
  visibility: hidden;
}
.site-card-actions .connection-dot.connected {
  color: var(--success, var(--accent));
}
.site-card-actions .connection-dot.failed {
  color: var(--danger);
}
.site-card-actions .connection-dot.slow {
  color: var(--warning, var(--accent));
}
.site-card-actions .connection-dot.testing,
.site-card-actions .connection-dot.queued {
  color: var(--accent);
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
  right: 38px;
  bottom: 0;
  z-index: 5;
  min-width: 130px;
  background: var(--surface);
  border: 1px solid var(--line);
  padding: 5px;
  border-radius: 8px;
  box-shadow: var(--shadow);
}
.site-menu-items button {
  width: 100%;
  justify-content: flex-start;
  font-size: 13px;
  padding: 10px 12px;
  gap: 9px;
  height: auto;
  border-radius: 4px;
}
.site-menu-items button.danger {
  color: var(--danger);
}
.site-floating-actions {
  position: fixed;
  right: max(16px, env(safe-area-inset-right));
  bottom: max(16px, env(safe-area-inset-bottom));
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 50;
}
.site-floating-actions button {
  width: 48px;
  height: 48px;
  padding: 0;
  border-radius: 50%;
  box-shadow: none;
  color: var(--accent);
  background: var(--accent-soft);
}
.site-floating-actions button.primary {
  border-radius: 50%;
  box-shadow: none;
  color: white;
  background: var(--accent);
}
.site-floating-actions button:nth-child(1) {
  color: var(--info, var(--accent));
  background: color-mix(in srgb, var(--info, var(--accent)) 12%, var(--surface));
}
.site-floating-actions button:nth-child(2) {
  color: var(--warning, var(--accent));
  background: color-mix(in srgb, var(--warning, var(--accent)) 12%, var(--surface));
}
.site-floating-actions button:nth-child(3) {
  color: var(--success, var(--accent));
  background: color-mix(in srgb, var(--success, var(--accent)) 12%, var(--surface));
}
.site-floating-actions button.primary .app-icon {
  width: 28px;
  height: 28px;
}
.sort-notice {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 13px 16px;
  background: var(--accent-soft);
  border-radius: 10px;
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
  padding: 6px;
}
.sort-controls .app-icon {
  width: 16px;
  height: 16px;
}
.sort-controls span {
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}
.manager-backdrop {
  position: fixed;
  inset: 0;
  background: color-mix(in srgb, var(--text) 50%, transparent);
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.manager-dialog {
  --manager-dialog-padding: 24px;
  width: 720px;
  max-width: 100%;
  max-height: calc(100dvh - 40px);
  overflow: auto;
  background: var(--surface);
  padding: var(--manager-dialog-padding);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow);
}
.manager-dialog header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 24px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--line);
}
.manager-dialog h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 500;
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
  margin-top: 24px;
  padding-top: 16px;
}
.site-editor {
  display: flex;
  flex-direction: column;
  width: 720px;
  max-height: calc(100dvh - 48px);
  padding: 0;
  overflow: hidden;
}
.site-editor > header {
  flex: none;
  margin: 0;
  padding: 12px 24px;
  background: var(--surface);
}
.site-editor > header .icon-button {
  width: 32px;
  height: 32px;
  min-height: 32px;
}
.site-editor-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.site-editor-heading > .app-icon {
  width: 24px;
  height: 24px;
}
.site-editor-body {
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 24px;
}
.site-editor > footer {
  flex: none;
  margin: 0;
  padding: 16px 24px;
  border-top: 1px solid var(--line);
  background: var(--surface);
}
.site-editor .manager-form-grid {
  gap: 24px;
}
.site-editor-error {
  flex: none;
  max-height: 120px;
  overflow: auto;
  margin: 0 24px 14px;
}
.manager-form-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 24px;
}
.manager-form-grid > label {
  position: relative;
  display: grid;
  align-content: start;
  gap: 8px;
  font-size: 14px;
  min-width: 0;
}
.field-label {
  position: absolute;
  top: -9px;
  left: 12px;
  padding: 0 4px;
  background: var(--surface);
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  z-index: 1;
}
.manager-form-grid > label:focus-within > .field-label {
  color: var(--accent);
}
.manager-form-grid > label > input,
.manager-form-grid > label > select {
  height: 56px;
  padding: 12px 16px;
  font-size: 16px;
  border-radius: 8px;
}
.manager-form-grid > label > textarea {
  min-height: 122px;
  padding: 16px;
  border-radius: 8px;
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
  font-size: 12px;
  line-height: 1.7;
}
.credential-tabs {
  display: flex;
  gap: 8px;
  margin-top: -4px;
}
.credential-tabs button {
  border: 0;
  background: transparent;
  border-radius: 6px;
  padding: 10px 20px;
  min-width: 88px;
  box-shadow: none;
}
.credential-tabs button[aria-selected='true'] {
  background: var(--accent-soft);
  color: var(--accent);
}
.manager-form-grid textarea {
  font-family: inherit;
  font-size: 14px;
  line-height: 1.5;
}
.site-option-list {
  min-width: 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
}
.site-option-row {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  margin: 0;
  padding: 4px 0;
  cursor: pointer;
}
.site-option-row:first-child {
  grid-column: 1/-1;
}
.site-option-row > input {
  grid-row: 1;
  grid-column: 1;
}
.site-option-row > .site-option-copy {
  grid-row: 1;
  grid-column: 2;
}
.site-option-copy {
  display: grid;
  gap: 3px;
  min-width: 0;
}
.site-option-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
  font-size: 14px;
  font-weight: 400;
  color: var(--text);
}
.site-option-copy small {
  font-size: 12px;
  font-weight: 400;
  line-height: 1.5;
}
.site-option-unavailable {
  cursor: default;
}
.site-option-unavailable .site-option-title {
  color: var(--muted);
}
.option-unavailable {
  padding: 1px 5px;
  border: 1px solid var(--line);
  border-radius: 4px;
  color: var(--muted);
  font-size: 10px;
  font-weight: 400;
  line-height: 1.6;
  white-space: nowrap;
}
input.site-switch[type='checkbox'] {
  appearance: none;
  -webkit-appearance: none;
  position: relative;
  display: block;
  width: 36px;
  height: 14px;
  min-width: 36px;
  flex: none;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 99px;
  background: color-mix(in srgb, var(--muted) 45%, transparent);
  box-shadow: none;
  cursor: pointer;
  transition: background 0.18s;
}
input.site-switch[type='checkbox']::before {
  content: '';
  position: absolute;
  width: 20px;
  height: 20px;
  top: -3px;
  left: 0;
  border-radius: 50%;
  background: var(--surface);
  box-shadow: 0 1px 3px color-mix(in srgb, var(--text) 25%, transparent);
  transition: transform 0.18s;
}
input.site-switch[type='checkbox']:checked {
  background: color-mix(in srgb, var(--accent) 45%, transparent);
}
input.site-switch[type='checkbox']:checked::before {
  transform: translateX(16px);
  background: var(--accent);
}
input.site-switch[type='checkbox']:focus-visible {
  outline: 3px solid var(--accent-soft);
  outline-offset: 3px;
}
input.site-switch[type='checkbox']:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.limit-fields {
  grid-column: 1/-1;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 24px;
  padding: 0;
  font-size: 12px;
}
.limit-fields label {
  min-width: 0;
  display: grid;
  gap: 7px;
}
.limit-fields input {
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
  gap: 12px;
  margin: 22px 0;
}
.site-metrics > div {
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: 8px;
}
.site-metrics dt {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 7px;
}
.site-metrics dd {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.site-totals {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 15px;
}
.site-totals > div {
  display: grid;
  gap: 10px;
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
}
.site-totals strong {
  font-size: 23px;
}
.site-totals small {
  color: var(--muted);
}
.manager-table {
  overflow: auto;
  border: 1px solid var(--line);
  border-radius: 8px;
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
.manager-table th {
  color: var(--muted);
  font-weight: 550;
  background: color-mix(in srgb, var(--line) 20%, var(--surface));
}
.spinning {
  animation: site-spin 1s linear infinite;
}
@keyframes site-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .spinning {
    animation: none;
  }
  .site-card {
    transition: none;
  }
}
@media (hover: hover) and (pointer: fine) {
  .site-card-actions {
    opacity: 0;
    visibility: hidden;
    transform: translateX(8px);
    transition:
      opacity 0.2s,
      transform 0.2s,
      visibility 0.2s;
  }
  .site-card:hover .site-card-actions,
  .site-card:focus-within .site-card-actions,
  .site-card:has([aria-busy='true']) .site-card-actions {
    opacity: 1;
    visibility: visible;
    transform: none;
  }
}
@media (max-width: 959px) {
  .manager-backdrop:has(.site-editor) {
    padding: 0;
  }
  .site-editor {
    width: 100%;
    height: 100dvh;
    max-height: 100dvh;
    border-radius: 0;
  }
  .site-editor .manager-form-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .site-option-list {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 650px) {
  .site-toolbar {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .site-search {
    grid-column: 1 / -1;
    width: 100%;
    max-width: none;
  }
  .site-grid {
    grid-template-columns: 1fr;
  }
  .manager-backdrop {
    padding: 10px;
  }
  .manager-dialog {
    --manager-dialog-padding: 18px;
    max-height: calc(100dvh - 20px);
  }
  .site-editor {
    max-height: 100dvh;
    padding: 0;
  }
  .site-editor > header {
    padding: 16px 18px;
  }
  .site-editor-body {
    padding: 24px 18px;
  }
  .site-editor > footer {
    padding: 13px 18px;
  }
  .site-editor-error {
    margin-right: 18px;
    margin-left: 18px;
  }
  .limit-fields {
    grid-template-columns: 1fr;
  }
  .manager-form-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .half {
    grid-column: span 2;
  }
  .site-floating-actions {
    right: 14px;
    bottom: 14px;
  }
  .site-metrics {
    grid-template-columns: repeat(2, 1fr);
  }
  .site-toolbar select {
    width: 100%;
  }
  .site-card-body {
    padding: 12px 48px 12px 12px;
  }
  .limit-fields {
    flex-direction: column;
  }
}
</style>
