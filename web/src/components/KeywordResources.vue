<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
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
import ResourceCard from './ResourceCard.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
import {
  interruptedSearch,
  siteSearchLabel,
  searchPending,
  siteElapsed,
} from '../features/search-progress';
const props = defineProps<{ route: string }>(),
  params = new URLSearchParams(props.route.split('?')[1]);
const query = ref(params.get('query') || ''),
  page = ref(1),
  output = ref<any>(null),
  error = ref(''),
  loading = ref(false),
  selectedSites = ref<string[]>([]),
  policy = ref(emptyPolicy()),
  filters = ref(emptyResourceFilters()),
  view = ref('card'),
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
  if (props.route === target) {
    page.value = 1;
    void search();
  } else go(target);
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
    downloaded.value[candidateKey(active.value)] = 'planned';
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
  if (query.value.trim() && selectedSites.value.length) void search();
});
onUnmounted(() => {
  disposed = true;
  controller?.abort();
});
</script>
<template>
  <div class="page-actions">
    <form class="search-field" @submit.prevent="submitSearch">
      <input v-model="query" placeholder="搜索资源关键词" required /><button class="small primary">
        搜索
      </button>
    </form>
  </div>
  <div class="toolbar">
    <div class="segmented">
      <button @click="go('search?query=' + encodeURIComponent(query))">影视</button
      ><button class="selected">资源</button>
    </div>
    <span class="hint"></span>
  </div>
  <section class="panel">
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
  <div v-if="loading" class="notice">
    正在并行搜索 {{ selectedSites.length }} 个站点，结果将陆续显示…
  </div>
  <div v-if="output" class="status-strip">
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
  <template v-if="output"
    ><section class="panel filters">
      <div class="filter-row">
        <input v-model="filters.text" placeholder="在结果中搜索" /><select v-model="filters.site">
          <option value="">全部站点</option>
          <option v-for="s in options('site_id')" :key="s" :value="s">
            {{ names[s] || s }}
          </option></select
        ><select v-model="filters.resolution">
          <option value="">全部分辨率</option>
          <option v-for="v in options('resolution')" :key="v" :value="v">
            {{ v === 'unknown' ? '未知' : v }}
          </option></select
        ><select v-model="filters.codec">
          <option value="">全部编码</option>
          <option v-for="v in options('codec')" :key="v" :value="v">
            {{ v === 'unknown' ? '未知' : v }}
          </option></select
        ><select v-model="filters.source">
          <option value="">全部质量</option>
          <option v-for="v in options('source')" :key="v" :value="v">
            {{ v === 'unknown' ? '未知' : v }}
          </option>
        </select>
      </div>
      <div class="filter-row">
        <select v-model="filters.promotion">
          <option value="">全部促销</option>
          <option value="free">免费</option>
          <option value="discount">下载折扣</option>
          <option value="upload">上传加成</option>
          <option value="normal">普通</option>
          <option value="unknown">未知</option></select
        ><select v-model="filters.seeders">
          <option value="">全部活跃度</option>
          <option value="alive">有人做种</option>
          <option value="zero">无人做种</option>
          <option value="unknown">做种数未知</option></select
        ><select v-model="filters.state">
          <option value="">全部资源</option>
          <option value="downloaded">本应用已下载 / 下载中</option>
          <option value="new">本应用尚未下载</option></select
        ><button class="small" @click="filters = emptyResourceFilters()">清空筛选</button
        ><span class="hint">{{ filtered.length }} / {{ candidates.length }} 条</span>
      </div>
    </section>
    <div class="toolbar">
      <label class="check"
        ><input type="checkbox" :checked="allSelected" @change="selectAll" />选择可下载结果</label
      ><button v-if="selected.length" class="small primary" @click="openSelected">
        下载所选 {{ selected.length }} 项</button
      ><select v-model="sort">
        <option value="default">默认排序</option>
        <option value="seeders">做种人数</option>
        <option value="size">体积</option>
        <option value="published_at">发布时间</option>
        <option value="site">站点</option></select
      ><button class="small" :disabled="sort === 'default'" @click="descending = !descending">
        {{ descending ? '↓ 降序' : '↑ 升序' }}
      </button>
      <div class="segmented push-right">
        <button :class="{ selected: view === 'card' }" @click="view = 'card'">▦ 卡片</button
        ><button :class="{ selected: view === 'list' }" @click="view = 'list'">☰ 列表</button>
      </div>
    </div>
    <div v-if="view === 'card'" class="release-grid">
      <ResourceCard
        v-for="g in groups"
        :key="candidateKey(g[0]!)"
        :candidates="g"
        :names="names"
        :downloads="downloaded"
        :selected="selected"
        :show-identity-review="false"
        @download="open"
        @select="select"
      />
    </div>
    <div v-else class="table-wrap">
      <table>
        <thead>
          <tr>
            <th></th>
            <th>资源</th>
            <th>站点</th>
            <th>大小 / 质量</th>
            <th>活跃度</th>
            <th>促销 / 时间</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in filtered" :key="candidateKey(c)">
            <td>
              <input
                type="checkbox"
                :checked="selected.includes(candidateKey(c))"
                :disabled="!c.accepted && !c.needs_confirmation"
                @change="select(c)"
              />
            </td>
            <td class="resource-title">
              <a
                v-if="c.resource.details_url?.startsWith('https://')"
                :href="c.resource.details_url"
                target="_blank"
                rel="noopener noreferrer"
                ><strong>{{ c.resource.title }}</strong></a
              ><strong v-else>{{ c.resource.title }}</strong
              ><small>{{ c.resource.subtitle }}</small
              ><span v-if="downloaded[candidateKey(c)]" class="badge green">{{
                label(downloaded[candidateKey(c)]!)
              }}</span>
              <details v-if="resourceReviewReasons(c, false).length">
                <summary class="hint">
                  {{ c.accepted ? '资源信息' : '不符合搜索规则' }}
                </summary>
                {{ resourceReviewReasons(c, false).map(friendly).join('；') }}
              </details>
            </td>
            <td>{{ names[c.resource.site_id] || c.resource.site_id }}</td>
            <td>
              {{ c.resource.size > 0 ? bytes(c.resource.size) : '体积未知'
              }}<small>{{
                [c.resource.resolution, c.resource.codec, c.resource.source]
                  .filter(Boolean)
                  .join(' · ')
              }}</small>
            </td>
            <td>
              ↑ {{ c.resource.seeders >= 0 ? c.resource.seeders : '未知'
              }}<small>↓ {{ c.resource.leechers >= 0 ? c.resource.leechers : '未知' }}</small>
            </td>
            <td>
              <span class="badge">{{ promotion(c.resource) }}</span
              ><small>{{ date(c.resource.published_at) }}</small
              ><small v-if="c.resource.promotion_end">{{
                promotionRemaining(c.resource.promotion_end)
              }}</small>
            </td>
            <td>
              <button
                class="small primary"
                :disabled="!c.accepted && !c.needs_confirmation"
                @click="open(c)"
              >
                ↓ 下载
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!filtered.length" class="empty-state">
      <h3>{{ candidates.length ? '没有符合条件的资源' : '没有搜索到资源' }}</h3>
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
    </div></template
  >
  <AddDownloadDialog
    v-if="active"
    :candidate="active"
    :request="request"
    @close="close"
    @done="downloadAdded"
  />
</template>
<style scoped>
.page-actions {
  margin-bottom: 12px;
}
.page-actions .search-field {
  width: min(100%, 620px);
  border-radius: 10px;
}
.panel {
  padding: 14px 18px;
  border-color: var(--line);
  border-radius: 12px;
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
  color: #4c6472;
  font-size: 12px;
}
.status-strip {
  gap: 8px;
  margin: 12px 0;
}
.status-strip > span {
  padding: 6px 10px;
  border-radius: 7px;
  font-size: 11px;
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
.release-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 325px), 1fr));
  gap: 18px;
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
  background: #fbfcfd;
}
.filter-row input,
.filter-row select {
  min-height: 37px;
  background: var(--surface);
}
.resource-title > strong {
  display: block;
  margin-bottom: 5px;
  line-height: 1.6;
  color: #243c4a;
  font-size: 12px;
}
.resource-title > small {
  color: var(--muted);
  line-height: 1.6;
}
.pagination {
  margin-top: 28px;
  padding-top: 20px;
  border-top: 1px solid var(--line);
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
  .release-grid,
  .identity-list {
    grid-template-columns: 1fr;
  }
  .toolbar .push-right {
    margin-left: 0;
  }
}
</style>
