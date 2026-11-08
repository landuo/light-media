<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { ref, computed, onActivated, onMounted, onUnmounted } from 'vue';
import {
  api,
  action,
  ui,
  emptyPolicy,
  bytes,
  label,
  date,
  go,
  friendly,
  toast,
  errorText,
} from '../api';
import { keywordResourceSearch } from '../features/keyword-search';
import { startSearch, useExplicitSearch } from '../features/search-session';
import {
  candidateKey,
  emptyResourceFilters,
  filterResources,
  groupResources,
  promotionKind,
  promotionRemaining,
  resourceReviewReasons,
  suggestedDownloadEpisodes,
  type Candidate,
} from '../features/resources';
import Policy from './Policy.vue';
import AppIcon from './AppIcon.vue';
import ResourceFilterBar from './ResourceFilterBar.vue';
import ResourceSearchResults from './ResourceSearchResults.vue';
import ResourceSearchProgress from './ResourceSearchProgress.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
import {
  interruptedSearch,
  siteSearchLabel,
  searchPending,
  siteElapsed,
} from '../features/search-progress';
const searchOptionsOpen = ref(false);
const props = defineProps<{ route: string }>(),
  params = new URLSearchParams(props.route.split('?')[1]);
const query = ref(params.get('query') || ''),
  page = ref(1),
  output = ref<any>(null),
  error = ref(''),
  loading = ref(false),
  initialized = ref(false),
  selectedSites = ref<string[]>([]),
  policy = ref(emptyPolicy()),
  filters = ref(emptyResourceFilters()),
  view = ref(localStorage.getItem('light-media.resource-view') || 'card'),
  sort = ref('default'),
  descending = ref(true),
  selected = ref<string[]>([]),
  downloaded = ref<Record<string, string>>({});
const request = ref<any>(null),
  active = ref<Candidate | null>(null),
  queue = ref<Candidate[]>([]);
let controller: AbortController | null = null,
  disposed = false;
const names = computed<Record<string, string>>(() =>
    Object.fromEntries((ui.settings?.sites || []).map((s: any) => [s.id, s.name || s.id])),
  ),
  candidates = computed<Candidate[]>(() => output.value?.candidates || []),
  filtered = computed(() =>
    filterResources(
      candidates.value,
      filters.value,
      sort.value,
      descending.value,
      downloaded.value,
    ),
  ),
  groups = computed(() => groupResources(filtered.value));
const eligible = computed(() =>
    (view.value === 'card' ? groups.value.map((g) => g[0]!) : filtered.value).filter(
      (c) => c.accepted || c.needs_confirmation,
    ),
  ),
  allSelected = computed(
    () =>
      eligible.value.length > 0 &&
      eligible.value.every((c) => selected.value.includes(candidateKey(c))),
  );
function setView(value: string) {
  view.value = value;
  localStorage.setItem('light-media.resource-view', value);
}
onActivated(() => { view.value = localStorage.getItem('light-media.resource-view') || 'card'; });
function options(key: string) {
  return [...new Set(candidates.value.map((c) => String(c.resource[key] || 'unknown')))].sort();
}
function promotion(resource: any) {
  const kind = promotionKind(resource);
  return kind === 'free'
    ? 'FREE'
    : kind === 'unknown'
      ? '促销未知'
      : resource.download_factor < 1
        ? resource.download_factor + '×下载'
        : resource.upload_factor > 1
          ? resource.upload_factor + '×上传'
          : '普通';
}
function select(c: Candidate) {
  const key = candidateKey(c);
  selected.value = selected.value.includes(key)
    ? selected.value.filter((v) => v !== key)
    : [...selected.value, key];
}
function selectAll() {
  const keys = eligible.value.map(candidateKey);
  selected.value = allSelected.value
    ? selected.value.filter((k) => !keys.includes(k))
    : [...new Set([...selected.value, ...keys])];
}
async function loadDownloads() {
  try {
    const r = await api('/downloads');
    downloaded.value = Object.fromEntries(
      (Array.isArray(r) ? r : [...(r.intents || r.items || []), ...(r.native_downloads || [])])
        .filter((i: any) => !['failed', 'cancelled'].includes(i.state))
        .map((i: any) => [i.site_id + ':' + i.resource_id, i.state]),
    );
  } catch {}
}
async function search() {
  if (!query.value.trim()) {
    error.value = '请输入资源关键词';
    return;
  }
  controller?.abort();
  const c = new AbortController();
  controller = c;
  loading.value = true;
  output.value = null;
  selected.value = [];
  error.value = '';
  request.value = JSON.parse(
    JSON.stringify({
      query: query.value.trim(),
      site_ids: selectedSites.value,
      page: page.value,
      policy: policy.value,
    }),
  );
  try {
    const r = await keywordResourceSearch(request.value, c.signal, (value) => {
      if (controller === c) output.value = value;
    });
    if (controller === c) output.value = r;
  } catch (e) {
    if (controller === c && !disposed) {
      output.value = interruptedSearch(
        output.value,
        request.value?.site_ids || [],
        c.signal.aborted ? 'cancelled' : 'interrupted',
      );
      if (c.signal.aborted) toast('已停止搜索，完成的站点结果已保留');
      else error.value = errorText(e);
    }
  } finally {
    if (controller === c) {
      loading.value = false;
      controller = null;
    }
  }
}
function submitSearch() {
  const target = 'resource-search?query=' + encodeURIComponent(query.value.trim());
  if (query.value.trim()) startSearch(target);
}
function close() {
  active.value = null;
  queue.value = [];
}
function open(c: Candidate, batch = false) {
  if (!batch) queue.value = [];
  active.value = c;
}
function openSelected() {
  queue.value = candidates.value.filter((c) => selected.value.includes(candidateKey(c)));
  const c = queue.value.shift();
  if (c) open(c, true);
}
function downloadAdded() {
  if (active.value) {
    downloaded.value[candidateKey(active.value)] = 'accepted';
    selected.value = selected.value.filter((k) => k !== candidateKey(active.value!));
  }
  toast('下载任务已添加');
  const next = queue.value.shift();
  if (next) open(next, true);
  else close();
}
onMounted(() => {
  selectedSites.value = (ui.settings?.sites || [])
    .filter((s: any) => s.enabled)
    .map((s: any) => s.id);
  void loadDownloads();
  initialized.value = true;
});
useExplicitSearch(
  props.route,
  () => {
    page.value = 1;
    if (query.value.trim() && selectedSites.value.length) return search();
  },
  initialized,
);
onUnmounted(() => {
  disposed = true;
  controller?.abort();
});
</script>
<template>
  <div class="resource-page-actions">
    <span class="resource-search-term">{{ request?.query || query }}</span>
    <button
      class="icon-button"
      aria-label="搜索设置"
      title="搜索设置"
      @click="searchOptionsOpen = !searchOptionsOpen"
    >
      <AppIcon name="settings" />
    </button>
    <button
      class="icon-button"
      aria-label="重新搜索"
      title="重新搜索"
      :disabled="loading"
      @click="search"
    >
      <AppIcon name="refresh" />
    </button>
  </div>
  <section v-if="searchOptionsOpen" class="panel">
    <form class="search-field" @submit.prevent="submitSearch">
      <VTextField v-model="query" label="搜索资源关键词" density="compact" required /><button
        class="primary"
      >
        搜索
      </button>
    </form>
    <div class="site-selector">
      <label
        v-for="s in (ui.settings?.sites || []).filter((s: any) => s.enabled)"
        :key="s.id"
        class="check"
        ><input v-model="selectedSites" type="checkbox" :value="s.id" />{{ s.name || s.id }}</label
      >
    </div>
    <details>
      <summary>搜索规则</summary>
      <Policy :model="policy" />
    </details>
    <div class="button-row">
      <button
        class="primary"
        :disabled="loading || !selectedSites.length"
        @click="
          page = 1;
          search();
        "
      >
        {{ loading ? '搜索中…' : '重新搜索' }}</button
      ><button v-if="loading" @click="controller?.abort()">停止搜索</button>
    </div>
  </section>
  <div v-if="error" class="error-box">{{ error }}</div>
  <ResourceSearchProgress
    v-if="loading"
    :keyword="request?.query || query"
    :site-ids="request?.site_ids || selectedSites"
    :sites="output?.sites || []"
  />
  <details
    v-if="
      output && (loading || searchOptionsOpen || output.sites.some((s: any) => s.status !== 'ok'))
    "
    class="search-progress-details"
    :open="loading"
  >
    <summary>站点搜索进度 · {{ candidates.length }} 个资源</summary>
    <div class="status-strip">
      <span
        v-for="s in output.sites"
        :key="s.id"
        :class="[
          'badge',
          { bad: ['failed', 'partial', 'timeout', 'interrupted'].includes(s.status) },
        ]"
        :title="friendly(s.message || '')"
      >
        {{ names[s.id] || s.id }} · {{ siteSearchLabel(s.status)
        }}{{ searchPending(s.status) ? '' : ' · ' + s.count + ' 条' }}{{ siteElapsed(s) }}</span
      >
    </div>
  </details>
  <template v-if="output || loading">
    <ResourceFilterBar
      v-if="candidates.length"
      :candidates="candidates"
      :count="filtered.length"
      :names="names"
      v-model:filters="filters"
      v-model:sort="sort"
      v-model:descending="descending"
    />
    <div v-if="selected.length" class="toolbar resource-toolbar">
      <button class="small primary" @click="openSelected">下载所选 {{ selected.length }} 项</button>
      <button class="small" @click="selectAll">全选 / 取消全选</button>
      <button class="small" @click="selected = []">取消选择</button>
    </div>
    <ResourceSearchResults
      :view="view"
      :groups="groups"
      :results="filtered"
      :names="names"
      :downloads="downloaded"
      :selected="selected"
      :show-identity-review="false"
      :has-candidates="!!candidates.length"
      :loading="loading"
      @update:view="setView"
      @download="open"
      @select="select"
      @clear="filters = emptyResourceFilters()"
    />
    <div v-if="output" class="pagination">
      <button
        :disabled="page <= 1 || loading"
        @click="
          page--;
          search();
        "
      >
        上一页</button
      ><span>第 {{ page }} 页</span
      ><button
        :disabled="loading"
        @click="
          page++;
          search();
        "
      >
        搜索下一页
      </button>
    </div>
  </template>
  <AddDownloadDialog
    v-if="active"
    :candidate="active"
    :request="request"
    @close="close"
    @done="downloadAdded"
    @error="close"
  />
</template>
<style scoped>
.resource-page-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 8px 0 20px;
}
.resource-search-term {
  margin-right: auto;
  color: var(--accent);
  font-size: 20px;
  font-weight: 500;
}
.resource-page-actions .icon-button {
  border: 0;
  background: none;
}
.search-progress-details {
  margin: 0 0 12px;
  padding: 0;
  border: 0;
  background: none;
}
.search-progress-details summary {
  font-size: 12px;
  color: var(--muted);
  padding: 4px;
}

.page-actions {
  margin-bottom: 12px;
}
.page-actions .search-field {
  width: min(100%, 620px);
  border-radius: 24px;
}
.panel {
  padding: 16px;
  border-color: var(--line);
  border-radius: 6px;
  box-shadow: var(--shadow);
}
.site-selector {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 18px;
  margin-bottom: 12px;
}
.site-selector .check {
  min-height: 30px;
  padding: 3px 0;
  color: var(--text);
  font-size: 14px;
}
.status-strip {
  gap: 8px;
  margin: 12px 0;
}
.status-strip > span {
  padding: 6px 10px;
  border-radius: 4px;
  font-size: 12px;
}
.filter-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.filter-row + .filter-row {
  margin-top: 8px;
}
.filter-row input {
  flex: 1;
  min-width: 200px;
}
.filter-row select {
  flex: 1;
  min-width: 120px;
}
.toolbar {
  gap: 12px;
  flex-wrap: wrap;
  margin: 16px 0;
}
.toolbar select {
  width: auto;
}
.filters {
  padding: 14px;
  margin: 16px 0;
  background: var(--surface);
}
.filter-row input,
.filter-row select {
  min-height: 40px;
  background: var(--surface);
}
.resource-title strong {
  display: block;
  margin-bottom: 5px;
  line-height: 1.6;
  color: var(--text);
  font-size: 14px;
  font-weight: 500;
}
.resource-title > small {
  color: var(--muted);
  line-height: 1.6;
}
.table-wrap {
  border-radius: 6px;
}
.table-wrap td {
  padding: 8px 16px;
  font-size: 14px;
}
.table-wrap th {
  padding: 10px 16px;
  font-size: 12px;
}
.pagination {
  margin-top: 24px;
  padding-top: 16px;
}
.resource-title {
  min-width: 300px;
  max-width: 500px;
  overflow-wrap: anywhere;
}
.identity-list {
  max-height: 40vh;
  overflow: auto;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 14px 0;
}
.identity-list button {
  display: flex;
  gap: 12px;
  align-items: center;
  text-align: left;
}
.identity-list img {
  width: 42px;
  height: 63px;
  object-fit: cover;
  border-radius: 4px;
}
.identity-list span {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.identity-list small {
  color: var(--muted);
  font-size: 11px;
}
.file-list {
  max-height: 250px;
  margin: 12px 0;
}
.notice .small {
  margin-left: 12px;
}
@media (max-width: 650px) {
  .identity-list {
    grid-template-columns: 1fr;
  }
  .toolbar .push-right {
    margin-left: 0;
  }
}
</style>
