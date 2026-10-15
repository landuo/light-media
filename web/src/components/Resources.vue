<script setup lang="ts">
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
import MappingRules from './MappingRules.vue';
import ResourceCard from './ResourceCard.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
import {
  interruptedSearch,
  siteSearchLabel,
  searchPending,
  siteElapsed,
} from '../features/search-progress';
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
  if (selectedSites.value.length && !mappingsError.value) await search();
});
onUnmounted(() => {
  disposed = true;
  searchController?.abort();
});
</script>
<template>
  <div class="page-actions">
    <div class="button-row">
      <button @click="go('detail/' + kind + '/' + id)">影视详情</button
      ><button
        class="primary"
        @click="
          go(
            'subscriptions/new/' +
              kind +
              '/' +
              id +
              '?season=' +
              (searchedRequest?.season ?? season),
          )
        "
      >
        ＋ 订阅
      </button>
    </div>
  </div>
  <section v-if="media" class="resource-context panel">
    <img
      v-if="image(media.poster_path, 'w92')"
      :src="image(media.poster_path, 'w92')"
      :alt="media.title"
    />
    <div>
      <h2>
        {{ media.title }} <small>{{ media.year }}</small>
      </h2>
      <p class="muted">{{ media.original_title }}</p>
      <div class="tags">
        <span>{{ label(kind) }}</span
        ><span v-if="kind === 'tv'"
          >第 {{ searchedRequest?.season ?? season }} 季{{
            searchedRequest?.episodes?.length ? ' · E' + searchedRequest.episodes.join(',') : ''
          }}</span
        ><span v-if="media.vote_average">★ {{ media.vote_average.toFixed(1) }}</span
        ><span v-if="media.library_state">{{ label(media.library_state) }}</span>
      </div>
    </div>
  </section>
  <section class="panel search-options">
    <div class="search-controls">
      <label v-if="kind === 'tv'"
        >季度<select v-model.number="season">
          <option v-for="s in media?.seasons || []" :key="s.season_number" :value="s.season_number">
            {{ s.name }} · {{ s.episode_count }} 集
          </option>
          <option v-if="!media?.seasons?.length" :value="season">第 {{ season }} 季</option>
        </select></label
      >
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
      <label v-if="kind === 'tv'"
        >指定集数（可选）<input v-model="eps" placeholder="不填写搜索整季；例如 1,2,5-8" /></label
      ><Policy :model="policy" />
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
  <div class="site-progress" v-if="loading || output">
    <div
      v-for="s in sitesProgress"
      :key="s.id"
      :class="['site-progress-item', s.status]"
      :title="s.message ? friendly(s.message) : ''"
    >
      <span :class="{ spin: searchPending(s.status) }">{{
        searchPending(s.status) ? '◌' : s.status === 'ok' ? '✓' : s.status === 'failed' ? '!' : '•'
      }}</span
      ><b>{{ names[s.id] || s.id }}</b
      ><small
        >{{ siteSearchLabel(s.status) }}{{ searchPending(s.status) ? '' : ' · ' + s.count
        }}{{ siteElapsed(s) }}</small
      >
    </div>
  </div>
  <template v-if="output">
    <section class="resource-filters panel">
      <div class="filter-row">
        <input
          v-model="filters.text"
          placeholder="在结果中搜索标题或副标题"
          aria-label="过滤资源标题"
        /><select v-model="filters.site">
          <option value="">全部站点</option>
          <option v-for="s in options('site_id')" :key="s" :value="s">
            {{ names[s] || s }}
          </option></select
        ><select v-model="filters.resolution">
          <option value="">全部分辨率</option>
          <option v-for="v in options('resolution')" :key="v" :value="v">
            {{ v === 'unknown' ? '分辨率未知' : v }}
          </option></select
        ><select v-model="filters.codec">
          <option value="">全部编码</option>
          <option v-for="v in options('codec')" :key="v" :value="v">
            {{ v === 'unknown' ? '编码未知' : v }}
          </option></select
        ><select v-model="filters.source">
          <option value="">全部质量</option>
          <option v-for="v in options('source')" :key="v" :value="v">
            {{ v === 'unknown' ? '质量未知' : v }}
          </option>
        </select>
      </div>
      <div class="filter-row">
        <select v-model="filters.promotion">
          <option value="">全部促销</option>
          <option value="free">免费下载</option>
          <option value="discount">下载折扣</option>
          <option value="upload">上传加成</option>
          <option value="normal">普通</option>
          <option value="unknown">促销未知</option></select
        ><select v-model="filters.seeders">
          <option value="">全部活跃度</option>
          <option value="alive">有人做种</option>
          <option value="zero">无人做种</option>
          <option value="unknown">做种数未知</option></select
        ><select v-model="filters.state">
          <option value="">全部资源</option>
          <option value="matched">匹配规则</option>
          <option value="review">需要核对</option>
          <option value="downloaded">本应用已下载 / 下载中</option>
          <option value="new">本应用尚未下载</option></select
        ><button class="small" @click="filters = emptyResourceFilters()">清空筛选</button
        ><span class="hint push-right">{{ filtered.length }} / {{ candidates.length }} 条</span>
      </div>
    </section>
    <div class="toolbar resource-toolbar">
      <label class="check"
        ><input
          type="checkbox"
          :checked="allVisibleSelected"
          @change="selectVisible"
        />选择可下载结果</label
      ><button v-if="selectedCandidates.length" class="small primary" @click="openSelected">
        下载所选 {{ selectedCandidates.length }} 项</button
      ><select v-model="sort">
        <option value="default">默认排序</option>
        <option value="seeders">做种人数</option>
        <option value="size">文件大小</option>
        <option value="published_at">发布时间</option>
        <option value="site">站点</option></select
      ><button class="small" :disabled="sort === 'default'" @click="descending = !descending">
        {{ descending ? '↓ 降序' : '↑ 升序' }}</button
      ><label v-if="view === 'card'" class="check"
        ><input v-model="groupSources" type="checkbox" />合并相同发布</label
      >
      <div class="segmented push-right">
        <button
          :class="{ selected: view === 'card' }"
          @click="setView('card')"
          aria-label="卡片视图"
        >
          ▦ 卡片</button
        ><button
          :class="{ selected: view === 'list' }"
          @click="setView('list')"
          aria-label="列表视图"
        >
          ☰ 列表
        </button>
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
        @download="openDownload"
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
            <th>促销</th>
            <th>发布时间</th>
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
              <details v-if="c.reasons?.length">
                <summary class="hint">
                  {{ c.needs_confirmation ? '需要核对' : c.accepted ? '匹配详情' : '不符合规则' }}
                </summary>
                {{ c.reasons.map(friendly).join('；') }}
              </details>
            </td>
            <td>{{ names[c.resource.site_id] || c.resource.site_id }}</td>
            <td>
              {{ c.resource.size > 0 ? bytes(c.resource.size) : '未知'
              }}<small>{{
                [c.resource.resolution, c.resource.codec, c.resource.source]
                  .filter(Boolean)
                  .join(' · ')
              }}</small>
            </td>
            <td>
              <span class="green"
                >↑ {{ c.resource.seeders >= 0 ? c.resource.seeders : '未知' }}</span
              ><small>↓ {{ c.resource.leechers >= 0 ? c.resource.leechers : '未知' }}</small>
            </td>
            <td>
              <span :class="['badge', { green: promotionKind(c.resource) === 'free' }]">{{
                promotion(c.resource)
              }}</span
              ><small v-if="c.resource.promotion_end" :title="date(c.resource.promotion_end)">{{
                promotionRemaining(c.resource.promotion_end)
              }}</small>
            </td>
            <td>{{ date(c.resource.published_at) }}</td>
            <td>
              <button
                class="small primary"
                :disabled="!c.accepted && !c.needs_confirmation"
                @click="openDownload(c)"
              >
                ↓ 下载
              </button>
            </td>
          </tr>
        </tbody>
      </table>
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
.page-actions {
  margin-bottom: 12px;
}
.resource-context {
  display: flex;
  align-items: center;
  gap: 18px;
  margin-bottom: 16px;
  padding: 16px 18px;
  border-radius: 12px;
}
.resource-context img {
  width: 48px;
  height: 72px;
  object-fit: cover;
  border-radius: 8px;
  box-shadow: 0 3px 8px #172b3a12;
}
.resource-context h2 {
  margin: 0 0 5px;
  font-size: 20px;
  color: #172b3a;
  font-weight: 650;
}
.resource-context h2 small {
  font-weight: 400;
  color: var(--muted);
  font-size: 13px;
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
  font-size: 12px;
  color: #5b7180;
}
.search-options {
  padding: 14px 18px;
  border-radius: 12px;
}
.site-selector .check {
  min-height: 32px;
  padding: 3px 0;
  font-size: 12px;
  color: #4c6472;
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
  border-radius: 8px;
  background: var(--surface);
  color: #536b79;
  font-size: 11px;
}
.site-progress-item.ok {
  border-color: #d1e7dd;
  background: #f4faf6;
}
.site-progress-item.failed {
  border-color: #eddbdc;
  background: #fdf7f7;
}
.site-progress-item small {
  color: var(--muted);
}
.site-progress-item.ok > span {
  color: #24794e;
}
.site-progress-item.failed > span {
  color: #b42336;
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
  border-radius: 12px;
  background: #fbfcfd;
}
.filter-row input,
.filter-row select {
  min-height: 37px;
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
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 325px), 1fr));
  gap: 18px;
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
