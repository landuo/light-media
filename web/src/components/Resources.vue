<script setup lang="ts">
import { VSelect } from 'vuetify/components/VSelect';
import { VTextField } from 'vuetify/components/VTextField';
import { ref, computed, onMounted, onUnmounted } from 'vue';
import {
  api,
  action,
  ui,
  emptyPolicy,
  episodeNumbers,
  bytes,
  label,
  date,
  go,
  friendly,
  searchStream,
  toast,
  errorText,
  image,
} from '../api';
import {
  candidateKey,
  emptyResourceFilters,
  filterResources,
  groupResources,
  promotionKind,
  promotionRemaining,
  suggestedDownloadEpisodes,
  type Candidate,
} from '../features/resources';
import Policy from './Policy.vue';
import AppIcon from './AppIcon.vue';
import ResourceFilterBar from './ResourceFilterBar.vue';
import { useExplicitSearch } from '../features/search-session';
import MappingRules from './MappingRules.vue';
import ResourceCard from './ResourceCard.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
import {
  interruptedSearch,
  siteSearchLabel,
  searchPending,
  siteElapsed,
} from '../features/search-progress';
const searchOptionsOpen = ref(false);
const props = defineProps<{ route: string }>();
const [path, qs] = props.route.split('?'),
  parts = path!.split('/'),
  q = new URLSearchParams(qs),
  kind = parts[1]!,
  id = Number(parts[2]);
const season = ref(kind === 'movie' ? 0 : Number(q.get('season') || 1)),
  eps = ref(q.get('episodes') || ''),
  policy = ref(emptyPolicy()),
  selectedSites = ref<string[]>([]),
  output = ref<any>(null),
  media = ref<any>(null),
  page = ref(1),
  loading = ref(false),
  initialized = ref(false),
  error = ref('');
const mappings = ref<any[]>([]),
  mappingsLoading = ref(kind === 'tv'),
  mappingsError = ref('');
const view = ref(localStorage.getItem('light-media.resource-view') || 'card'),
  filters = ref(emptyResourceFilters()),
  sort = ref('default'),
  descending = ref(true),
  groupSources = ref(true),
  selected = ref<string[]>([]),
  downloaded = ref<Record<string, string>>({});
const searchedRequest = ref<any>(null);
const activeCandidate = ref<Candidate | null>(null),
  queue = ref<Candidate[]>([]);
const names = computed<Record<string, string>>(() =>
  Object.fromEntries((ui.settings?.sites || []).map((s: any) => [s.id, s.name || s.id])),
);
const candidates = computed<Candidate[]>(() => output.value?.candidates || []);
const filtered = computed(() =>
  filterResources(candidates.value, filters.value, sort.value, descending.value, downloaded.value),
);
const groups = computed(() =>
  groupSources.value ? groupResources(filtered.value) : filtered.value.map((c) => [c]),
);
const selectedCandidates = computed(() =>
  candidates.value.filter((c) => selected.value.includes(candidateKey(c))),
);
const visibleEligible = computed(() =>
  (view.value === 'card' ? groups.value.map((g) => g[0]!) : filtered.value).filter(
    (c) => c.accepted || c.needs_confirmation,
  ),
);
const allVisibleSelected = computed(
  () =>
    visibleEligible.value.length > 0 &&
    visibleEligible.value.every((c) => selected.value.includes(candidateKey(c))),
);
const sitesProgress = computed(() =>
  (searchedRequest.value?.site_ids || selectedSites.value).map(
    (id: string) =>
      output.value?.sites?.find((s: any) => s.id === id) || {
        id,
        status: loading.value ? 'loading' : 'pending',
        count: 0,
      },
  ),
);
function options(key: string) {
  return [...new Set(candidates.value.map((c) => String(c.resource[key] || 'unknown')))].sort();
}
function setView(value: string) {
  view.value = value;
  localStorage.setItem('light-media.resource-view', value);
}
function select(c: Candidate) {
  const key = candidateKey(c);
  selected.value = selected.value.includes(key)
    ? selected.value.filter((v) => v !== key)
    : [...selected.value, key];
}
function selectVisible() {
  const keys = visibleEligible.value.map(candidateKey);
  selected.value = keys.every((k) => selected.value.includes(k))
    ? selected.value.filter((k) => !keys.includes(k))
    : [...new Set([...selected.value, ...keys])];
}
function promotion(r: any) {
  const p = promotionKind(r);
  return p === 'free'
    ? 'FREE'
    : p === 'unknown'
      ? '未知'
      : r.download_factor < 1
        ? `${r.download_factor}×下载`
        : r.upload_factor > 1
          ? `${r.upload_factor}×上传`
          : '普通';
}
let searchController: AbortController | null = null;
let disposed = false;
async function loadMappings() {
  if (kind !== 'tv') return;
  mappingsLoading.value = true;
  mappingsError.value = '';
  try {
    const response = await api('/subscriptions'),
      subscriptions = Array.isArray(response) ? response : response?.items || [],
      unique = new Map<string, any>();
    for (const s of subscriptions) {
      if (s.media?.media_type !== kind || Number(s.media?.id) !== id) continue;
      for (const rule of s.mappings || [])
        if (rule.confirmed && rule.media_id === kind + ':' + id)
          unique.set(JSON.stringify(rule), rule);
    }
    mappings.value = [...unique.values()];
  } catch (e) {
    mappingsError.value = errorText(e);
  } finally {
    mappingsLoading.value = false;
  }
}
async function loadDownloads() {
  try {
    const r = await api('/downloads'),
      items = Array.isArray(r)
        ? r
        : [...(r?.intents || r?.items || []), ...(r?.native_downloads || [])];
    downloaded.value = Object.fromEntries(
      items
        .filter((i: any) => !['cancelled', 'failed'].includes(i.state))
        .map((i: any) => [i.site_id + ':' + i.resource_id, i.state]),
    );
  } catch {
    /* Downloads are an optional annotation; the search is still usable. */
  }
}
async function search() {
  searchController?.abort();
  const controller = new AbortController();
  searchController = controller;
  loading.value = true;
  error.value = '';
  output.value = null;
  selected.value = [];
  try {
    const episodes = episodeNumbers(eps.value);
    searchedRequest.value = JSON.parse(
      JSON.stringify({
        media_type: kind,
        tmdb_id: id,
        season: season.value,
        episodes,
        policy: policy.value,
        mappings: mappings.value.filter((r) => r.confirmed),
        site_ids: selectedSites.value,
        page: page.value,
      }),
    );
    await searchStream(
      searchedRequest.value,
      (v) => {
        if (searchController === controller) output.value = { candidates: [], sites: [], ...v };
      },
      controller.signal,
    );
  } catch (e) {
    if (searchController === controller && !disposed) {
      output.value = interruptedSearch(
        output.value,
        searchedRequest.value?.site_ids || [],
        controller.signal.aborted ? 'cancelled' : 'interrupted',
      );
      if (controller.signal.aborted) toast('已停止搜索，完成的站点结果已保留');
      else error.value = errorText(e);
    }
  } finally {
    if (searchController === controller) {
      loading.value = false;
      searchController = null;
    }
  }
}
function cancelSearch() {
  searchController?.abort();
}
function openDownload(c: Candidate) {
  queue.value = [];
  activeCandidate.value = c;
}
function openSelected() {
  queue.value = selectedCandidates.value;
  activeCandidate.value = queue.value.shift() || null;
}
function closeDownload() {
  activeCandidate.value = null;
  queue.value = [];
}
function downloadAdded() {
  if (activeCandidate.value) {
    const key = candidateKey(activeCandidate.value);
    downloaded.value[key] = 'planned';
    selected.value = selected.value.filter((v) => v !== key);
  }
  toast('下载任务已添加');
  activeCandidate.value = queue.value.shift() || null;
}
onMounted(async () => {
  selectedSites.value = (ui.settings?.sites || [])
    .filter((s: any) => s.enabled)
    .map((s: any) => s.id);
  void loadDownloads();
  await Promise.all([
    loadMappings(),
    (async () => {
      media.value = await action(() => api('/catalog/detail?kind=' + kind + '&id=' + id));
    })(),
  ]);
  if (disposed) return;
  if (kind === 'tv' && !q.has('season'))
    season.value = media.value?.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
  initialized.value = true;
});
useExplicitSearch(
  props.route,
  () => {
    page.value = 1;
    if (selectedSites.value.length && !mappingsError.value) return search();
  },
  initialized,
);
onUnmounted(() => {
  disposed = true;
  searchController?.abort();
});
</script>
<template>
  <div class="resource-page-actions">
    <span class="resource-search-term">{{ media?.title || '' }}</span>
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
  <section v-if="searchOptionsOpen" class="panel search-options">
    <div class="search-controls">
      <VSelect
        v-if="kind === 'tv'"
        v-model.number="season"
        label="季度"
        :items="[
          ...(media?.seasons || []).map((s: any) => ({
            title: (String(s.name) + ' · ' + String(s.episode_count) + ' 集 ').trim(),
            value: s.season_number,
          })),
          ...(!media?.seasons?.length
            ? [{ title: ('第 ' + String(season) + ' 季').trim(), value: season }]
            : []),
        ]"
      />
      <div class="site-selector">
        <label
          v-for="s in (ui.settings?.sites || []).filter((s: any) => s.enabled)"
          :key="s.id"
          class="check"
          ><input type="checkbox" v-model="selectedSites" :value="s.id" />{{
            s.name || s.id
          }}</label
        >
      </div>
      <button
        class="primary"
        :disabled="loading || !selectedSites.length || mappingsLoading || !!mappingsError"
        @click="
          page = 1;
          search();
        "
      >
        {{ loading ? '搜索中…' : '重新搜索' }}</button
      ><button v-if="loading" @click="cancelSearch">停止</button>
    </div>
    <details>
      <summary>高级搜索设置</summary>
      <VTextField
        v-if="kind === 'tv'"
        v-model="eps"
        placeholder="不填写搜索整季；例如 1,2,5-8"
        label="指定集数（可选）"
      /><Policy :model="policy" />
      <details v-if="kind === 'tv'">
        <summary>已确认的季集映射 · {{ mappings.filter((r) => r.confirmed).length }}</summary>
        <MappingRules :model="mappings" :media-id="kind + ':' + id" :season="season" />
      </details>
      <p class="hint">筛选规则作用于匹配与下载核验，全部站点结果仍在下方显示；更改后请重新搜索。</p>
    </details>
  </section>
  <div v-if="mappingsError" class="error-box">
    无法加载已有季集映射：{{ mappingsError }} <button @click="loadMappings">重试</button>
  </div>
  <div v-if="error" class="error-box">{{ error }} <button @click="search">重试</button></div>
  <details
    v-if="loading || searchOptionsOpen || sitesProgress.some((s: any) => s.status !== 'ok')"
    class="search-progress-details"
    :open="loading"
  >
    <summary>站点搜索进度 · {{ output?.candidates?.length || 0 }} 个资源</summary>
    <div class="site-progress">
      <div
        v-for="s in sitesProgress"
        :key="s.id"
        :class="['site-progress-item', s.status]"
        :title="s.message ? friendly(s.message) : ''"
      >
        <span :class="{ spin: searchPending(s.status) }">{{
          searchPending(s.status)
            ? '◌'
            : s.status === 'ok'
              ? '✓'
              : s.status === 'failed'
                ? '!'
                : '•'
        }}</span
        ><b>{{ names[s.id] || s.id }}</b
        ><small
          >{{ siteSearchLabel(s.status) }}{{ searchPending(s.status) ? '' : ' · ' + s.count
          }}{{ siteElapsed(s) }}</small
        >
      </div>
    </div>
  </details>
  <template v-if="output">
    <ResourceFilterBar
      :candidates="candidates"
      :count="filtered.length"
      :names="names"
      v-model:filters="filters"
      v-model:sort="sort"
      v-model:descending="descending"
    />
    <div v-if="selected.length" class="toolbar resource-toolbar">
      <button class="small primary" @click="openSelected">下载所选 {{ selected.length }} 项</button>
      <button class="small" @click="selectVisible">全选 / 取消全选</button>
      <button class="small" @click="selected = []">取消选择</button>
    </div>
    <button
      class="resource-view-toggle"
      :aria-label="view === 'card' ? '列表视图' : '卡片视图'"
      :title="view === 'card' ? '列表视图' : '卡片视图'"
      @click="setView(view === 'card' ? 'list' : 'card')"
    >
      <AppIcon :name="view === 'card' ? 'menu' : 'jobs'" />
    </button>
    <div v-if="view === 'card'" class="release-grid">
      <ResourceCard
        v-for="g in groups"
        :key="candidateKey(g[0]!)"
        :candidates="g"
        :names="names"
        :downloads="downloaded"
        :selected="selected"
        @download="openDownload"
        @select="select"
      />
    </div>
    <div v-else class="resource-list">
      <ResourceCard
        v-for="c in filtered"
        :key="candidateKey(c)"
        layout="list"
        :candidates="[c]"
        :names="names"
        :downloads="downloaded"
        :selected="selected"
        @download="openDownload"
        @select="select"
      />
    </div>
    <div v-if="!filtered.length" class="empty-state">
      <h3>{{ candidates.length ? '没有符合筛选条件的资源' : '没有搜索到资源' }}</h3>
      <p>
        {{
          candidates.length ? '调整筛选条件或清空筛选。' : '查看站点状态，尝试其他季或稍后搜索。'
        }}
      </p>
      <button v-if="candidates.length" @click="filters = emptyResourceFilters()">清空筛选</button>
    </div>
    <div class="pagination">
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
  <div v-else-if="loading || !initialized" class="loading-state panel">正在获取站点结果…</div>
  <div v-else-if="!selectedSites.length" class="empty-state">
    <h3>请先添加并启用站点</h3>
    <button @click="go('sites')">配置站点</button>
  </div>
  <AddDownloadDialog
    v-if="activeCandidate"
    :candidate="activeCandidate"
    :request="searchedRequest"
    :media="media"
    @close="closeDownload"
    @done="downloadAdded"
  />
</template>
<style scoped>
.resource-list {
  display: grid;
  gap: 12px;
}
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
.resource-view-toggle {
  position: fixed;
  bottom: 20px;
  right: 20px;
  width: 48px;
  height: 48px;
  padding: 12px;
  border: 0;
  border-radius: 50%;
  background: var(--accent);
  color: white;
  box-shadow: var(--shadow);
  z-index: 5;
}
.resource-view-toggle svg {
  width: 24px;
  height: 24px;
}

.page-actions {
  margin-bottom: 12px;
}
.resource-context {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
  padding: 16px;
  border-radius: 6px;
}
.resource-context img {
  width: 48px;
  height: 72px;
  object-fit: cover;
  border-radius: 4px;
}
.resource-context h2 {
  margin: 0 0 5px;
  font-size: 20px;
  color: var(--text);
  font-weight: 500;
}
.resource-context h2 small {
  font-weight: 400;
  color: var(--muted);
  font-size: 14px;
  margin-left: 6px;
}
.resource-context p {
  margin: 5px 0 10px;
}
.search-controls {
  display: flex;
  align-items: center;
  gap: 17px;
  flex-wrap: wrap;
}
.search-controls > label {
  min-width: 145px;
  font-size: 14px;
  color: var(--muted);
}
.search-options {
  padding: 16px;
  border-radius: 6px;
}
.site-selector .check {
  min-height: 32px;
  padding: 3px 0;
  font-size: 14px;
  color: var(--text);
}
.search-controls .site-selector {
  flex: 1;
  display: flex;
  flex-wrap: wrap;
  gap: 8px 17px;
  margin: 0;
}
.search-options > details {
  margin: 8px 0 0;
  padding: 8px 0 0;
  border-top: 1px solid var(--line);
  border-right: 0;
  border-bottom: 0;
  border-left: 0;
  border-radius: 0;
}
.search-options > details > summary {
  padding: 0;
  border: 0;
  border-radius: 0;
  background: none;
  min-height: 0;
  margin: 0;
}
.search-options > details[open] > summary {
  margin-bottom: 12px;
}
.search-options > details > label {
  display: block;
  margin-top: 12px;
  max-width: 400px;
}
.site-progress {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 12px 0;
}
.site-progress-item {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 11px;
  border: 1px solid var(--line);
  border-radius: 4px;
  background: var(--surface);
  color: var(--muted);
  font-size: 12px;
}
.site-progress-item.ok {
  border-color: #56ca0033;
  background: #56ca0014;
}
.site-progress-item.failed {
  border-color: #ff4c5133;
  background: #ff4c5114;
}
.site-progress-item small {
  color: var(--muted);
}
.site-progress-item.ok > span {
  color: var(--success);
}
.site-progress-item.failed > span {
  color: var(--danger);
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
  min-width: 130px;
  flex: 1;
}
.resource-filters {
  padding: 14px;
  border-radius: 6px;
  background: var(--surface);
}
.filter-row input,
.filter-row select {
  min-height: 40px;
  background: var(--surface);
}
.resource-toolbar {
  gap: 12px;
  flex-wrap: wrap;
  margin: 16px 0;
}
.resource-toolbar select {
  width: auto;
}
.release-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 360px), 1fr));
  gap: 16px;
}
.resource-title {
  min-width: 300px;
  max-width: 500px;
}
.resource-title strong {
  overflow-wrap: anywhere;
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
.resource-title details {
  margin-top: 6px;
}
.preview-files {
  margin: 16px 0;
}
.preview-files .file-list {
  margin-top: 10px;
  max-height: 270px;
}
.spin {
  display: inline-block;
  animation: rotate 2s linear infinite;
}
@keyframes rotate {
  to {
    transform: rotate(360deg);
  }
}
@media (max-width: 650px) {
  .release-grid {
    grid-template-columns: 1fr;
  }
  .resource-context {
    gap: 12px;
  }
  .filter-row select {
    min-width: 110px;
  }
  .resource-toolbar .push-right {
    margin-left: 0;
  }
  .search-controls > label {
    width: 100%;
  }
  .site-progress-item {
    flex-wrap: wrap;
  }
}
</style>
