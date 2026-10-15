<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
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
  toast,
  errorText,
  image,
} from '../api';
import { keywordResourceSearch, resourceTitleQuery } from '../features/keyword-search';
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
import DownloadTarget from './DownloadTarget.vue';
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
  identityQuery = ref(''),
  identities = ref<any[]>([]),
  chosen = ref<any>(null),
  season = ref(1),
  episodes = ref(''),
  downloader = ref(''),
  downloadPath = ref(''),
  directoryId = ref(''),
  mappings = ref<any[]>([]),
  preview = ref<any>(null),
  dialogError = ref(''),
  previewLoading = ref(false),
  seasonInfoLoading = ref(false),
  identityLoading = ref(false),
  confirmLoading = ref(false),
  acceptExtra = ref(false),
  queue = ref<Candidate[]>([]);
let controller: AbortController | null = null,
  dialogGeneration = 0,
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
      (Array.isArray(r) ? r : r.intents || r.items || [])
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
function invalidatePreview() {
  ++dialogGeneration;
  preview.value = null;
  previewLoading.value = false;
  acceptExtra.value = false;
  dialogError.value = '';
  seasonInfoLoading.value = false;
}
function changeSeason() {
  invalidatePreview();
  episodes.value = active.value
    ? suggestedDownloadEpisodes([], active.value, season.value).join(',')
    : '';
}
async function chooseWholeSeason() {
  if (!chosen.value) return;
  invalidatePreview();
  const generation = dialogGeneration;
  seasonInfoLoading.value = true;
  try {
    const result = await api('/catalog/season?id=' + chosen.value.id + '&season=' + season.value);
    const values = (result.episodes || []).map((e: any) => e.episode_number);
    if (!values.length || values.some((e: number) => !Number.isInteger(e) || e < 1 || e > 9999))
      throw new Error('本季集数尚未确认，请明确选择目标集数');
    if (generation === dialogGeneration) episodes.value = [...new Set(values)].join(',');
  } catch (e) {
    if (generation === dialogGeneration) dialogError.value = errorText(e);
  } finally {
    if (generation === dialogGeneration) seasonInfoLoading.value = false;
  }
}
function close() {
  invalidatePreview();
  active.value = null;
  queue.value = [];
}
async function findIdentity() {
  const generation = ++dialogGeneration;
  identities.value = [];
  identityLoading.value = true;
  dialogError.value = '';
  try {
    const r = await api(
      '/catalog/search?query=' + encodeURIComponent(identityQuery.value) + '&page=1',
    );
    if (generation === dialogGeneration) identities.value = r.results || [];
  } catch (e) {
    if (generation === dialogGeneration) dialogError.value = errorText(e);
  } finally {
    if (generation === dialogGeneration) identityLoading.value = false;
  }
}
function open(c: Candidate, batch = false) {
  if (!batch) queue.value = [];
  invalidatePreview();
  active.value = c;
  chosen.value = null;
  mappings.value = [];
  identities.value = [];
  identityQuery.value = resourceTitleQuery(c.resource.title, request.value?.query || '');
  season.value = c.season || 1;
  episodes.value = suggestedDownloadEpisodes([], c, season.value).join(',');
  void findIdentity();
}
function openSelected() {
  queue.value = candidates.value.filter(
    (c) => selected.value.includes(candidateKey(c)) && (c.accepted || c.needs_confirmation),
  );
  const c = queue.value.shift();
  if (c) open(c, true);
}
async function choose(m: any) {
  invalidatePreview();
  const generation = dialogGeneration;
  identityLoading.value = true;
  try {
    const [detail, response] = await Promise.all([
      api('/catalog/detail?kind=' + m.media_type + '&id=' + m.id),
      api('/subscriptions'),
    ]);
    if (generation !== dialogGeneration) return;
    chosen.value = detail;
    const subscriptions = Array.isArray(response) ? response : response.items || [];
    const rules = subscriptions
      .filter((s: any) => s.media?.media_type === m.media_type && s.media?.id === m.id)
      .flatMap((s: any) => s.mappings || [])
      .filter((r: any) => r.confirmed && r.media_id === m.media_type + ':' + m.id);
    mappings.value = [...new Map(rules.map((r: any) => [JSON.stringify(r), r])).values()];
    if (
      m.media_type === 'tv' &&
      !detail.seasons?.some((s: any) => s.season_number === season.value)
    )
      season.value = detail.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
    if (active.value)
      episodes.value = suggestedDownloadEpisodes([], active.value, season.value).join(',');
    identities.value = [];
  } catch (e) {
    if (generation === dialogGeneration) dialogError.value = errorText(e);
  } finally {
    if (generation === dialogGeneration) identityLoading.value = false;
  }
}
async function inspect() {
  if (!active.value || !chosen.value || !downloadPath.value || !downloader.value) return;
  invalidatePreview();
  const generation = dialogGeneration;
  previewLoading.value = true;
  try {
    const r = await api('/downloads/preview', {
      ...request.value,
      media_type: chosen.value.media_type,
      tmdb_id: chosen.value.id,
      season: chosen.value.media_type === 'movie' ? 0 : season.value,
      episodes: chosen.value.media_type === 'movie' ? [] : episodeNumbers(episodes.value),
      site_id: active.value.resource.site_id,
      resource_id: active.value.resource.id,
      directory_id: directoryId.value,
      download_path: downloadPath.value,
      downloader_id: downloader.value,
      mappings: mappings.value.filter((r) => r.confirmed),
      workflow_id: output.value?.workflow_id || '',
    });
    if (generation === dialogGeneration) preview.value = r;
  } catch (e) {
    if (generation === dialogGeneration) dialogError.value = errorText(e);
  } finally {
    if (generation === dialogGeneration) previewLoading.value = false;
  }
}
async function confirmDownload() {
  const p = preview.value;
  if (!p || confirmLoading.value) return;
  confirmLoading.value = true;
  const r = await action(
    () => api('/downloads/confirm', { preview_id: p.id, accept_extra_files: acceptExtra.value }),
    '下载任务已提交',
  );
  confirmLoading.value = false;
  if (r) {
    if (active.value) {
      downloaded.value[candidateKey(active.value)] = 'planned';
      selected.value = selected.value.filter((k) => k !== candidateKey(active.value!));
    }
    const next = queue.value.shift();
    if (next) open(next, true);
    else close();
  }
}
onMounted(() => {
  downloader.value = ui.settings?.default_downloader || ui.settings?.downloaders?.[0]?.id || '';
  selectedSites.value = (ui.settings?.sites || [])
    .filter((s: any) => s.enabled)
    .map((s: any) => s.id);
  void loadDownloads();
  if (query.value.trim() && selectedSites.value.length) void search();
});
watch(
  () => JSON.stringify(mappings.value),
  () => invalidatePreview(),
);
onUnmounted(() => {
  disposed = true;
  controller?.abort();
  ++dialogGeneration;
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">SEARCH</p>
      <h1>资源搜索</h1>
      <p class="muted">直接按资源标题、发布组或关键词搜索站点。</p>
    </div>
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
    <span class="hint">下载前选择影视身份，并核对实际文件。</span>
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
              <details v-if="c.reasons?.length">
                <summary class="hint">
                  {{
                    c.accepted ? '匹配信息' : c.needs_confirmation ? '需要核对' : '不符合搜索规则'
                  }}
                </summary>
                {{ c.reasons.map(friendly).join('；') }}
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
  <div v-if="active" class="modal-backdrop" @click.self="close">
    <section class="modal wide" role="dialog" aria-modal="true" aria-label="添加资源下载">
      <div class="section-heading">
        <h2>添加下载</h2>
        <button class="icon-button" @click="close" aria-label="关闭">×</button>
      </div>
      <h3>{{ active.resource.title }}</h3>
      <p class="hint">{{ names[active.resource.site_id] }} · {{ bytes(active.resource.size) }}</p>
      <template v-if="!chosen"
        ><p class="notice">请选择这份资源对应的电影或剧集。资源关键词不会被当成影视身份。</p>
        <form class="inline-form" @submit.prevent="findIdentity">
          <input v-model="identityQuery" placeholder="搜索片名" required /><button
            class="primary"
            :disabled="identityLoading"
          >
            {{ identityLoading ? '搜索中…' : '搜索影视' }}
          </button>
        </form>
        <div class="identity-list">
          <button v-for="m in identities" :key="m.media_type + ':' + m.id" @click="choose(m)">
            <img
              v-if="image(m.poster_path, 'w92')"
              :src="image(m.poster_path, 'w92')"
              :alt="m.title"
            /><span
              ><b>{{ m.title }}</b
              ><small
                >{{ label(m.media_type) }} · {{ m.year || '日期待定' }} ·
                {{ m.original_title }}</small
              ></span
            >
          </button>
        </div>
        <p v-if="!identityLoading && !identities.length" class="hint">
          没有找到时可修改片名重新搜索。
        </p></template
      ><template v-else
        ><div class="notice">
          <strong>{{ chosen.title }}</strong> · {{ chosen.year }} · {{ label(chosen.media_type)
          }}<button
            class="small"
            @click="
              chosen = null;
              invalidatePreview();
              findIdentity();
            "
          >
            更换影视
          </button>
        </div>
        <div class="form-grid">
          <label v-if="chosen.media_type === 'tv'"
            >季度<select v-model.number="season" @change="changeSeason">
              <option
                v-for="s in chosen.seasons || []"
                :key="s.season_number"
                :value="s.season_number"
              >
                {{ s.name }} · {{ s.episode_count }} 集
              </option>
              <option v-if="!chosen.seasons?.length" :value="season">第 {{ season }} 季</option>
            </select></label
          ><label v-if="chosen.media_type === 'tv'"
            >目标集数<input
              v-model="episodes"
              placeholder="例如 17 或 17-20，不会默认整季"
              @input="invalidatePreview"
          /></label>
        </div>
        <DownloadTarget
          v-model:downloader="downloader"
          v-model:download-path="downloadPath"
          v-model:directory-id="directoryId"
          :media-type="chosen.media_type"
          @change="invalidatePreview"
        />
        <div v-if="chosen.media_type === 'tv'" class="button-row">
          <button class="small" :disabled="seasonInfoLoading" @click="chooseWholeSeason">
            {{ seasonInfoLoading ? '读取本季集数…' : '选择本季全部已知集数' }}</button
          ><span v-if="!episodes" class="warning">请先确认目标集数。</span>
        </div>
        <details v-if="chosen.media_type === 'tv'">
          <summary>季集映射（高级）</summary>
          <MappingRules
            :model="mappings"
            :media-id="chosen.media_type + ':' + chosen.id"
            :season="season"
          />
        </details>
        <div v-if="previewLoading" class="notice">正在读取真实文件并核对影视、季集和目录…</div>
        <template v-if="preview"
          ><div class="notice">
            {{ preview.files?.length }} 个文件 · {{ bytes(preview.total_bytes) }}<br />保存到
            {{ preview.download_path || downloadPath }}
          </div>
          <details open>
            <summary>实际文件列表</summary>
            <div class="file-list">
              <div v-for="f in preview.files" :key="f">{{ f }}</div>
            </div>
          </details>
          <div v-if="preview.warnings?.length" class="warning">
            <p v-for="(w, i) in preview.warnings" :key="i">{{ friendly(w) }}</p>
          </div>
          <label v-if="preview.needs_confirmation || preview.extra_files" class="check warning"
            ><input
              v-model="acceptExtra"
              type="checkbox"
            />我已核对文件，接受复核提示及额外内容。</label
          ></template
        ></template
      >
      <div v-if="dialogError" class="error-box">{{ dialogError }}</div>
      <div class="button-row">
        <button @click="close">取消</button
        ><button
          v-if="chosen && !preview"
          class="primary"
          :disabled="
            previewLoading ||
            seasonInfoLoading ||
            !downloader ||
            !downloadPath ||
            (chosen.media_type === 'tv' && !episodes.trim())
          "
          @click="inspect"
        >
          核对下载文件</button
        ><button
          v-else-if="preview"
          class="primary"
          :disabled="
            confirmLoading || ((preview.needs_confirmation || preview.extra_files) && !acceptExtra)
          "
          @click="confirmDownload"
        >
          确认下载
        </button>
      </div>
      <p class="hint">
        确认后才发送到下载器。{{
          queue.length ? '还有 ' + queue.length + ' 项待选择身份和核对。' : ''
        }}
      </p>
    </section>
  </div>
</template>
<style scoped>
.filter-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.filter-row + .filter-row {
  margin-top: 12px;
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
  grid-template-columns: repeat(auto-fill, minmax(310px, 1fr));
  gap: 16px;
}
.toolbar {
  gap: 10px;
  flex-wrap: wrap;
}
.toolbar select {
  width: auto;
}
.filters {
  margin: 18px 0;
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
