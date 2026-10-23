<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { api, action, go, image, date, ui, label, errorText } from '../api';
import MediaCards from './MediaCards.vue';
import { startSearch, useExplicitSearch } from '../features/search-session';
let loadSeq = 0,
  seasonSeq = 0;
const creditsKind = ref('all'),
  creditsLimit = ref(24),
  creditsSort = ref('popularity');
const props = defineProps<{ route: string }>();
const [routePath, routeQuery] = props.route.split('?'),
  routeParams = new URLSearchParams(routeQuery),
  parts = routePath!.split('/'),
  page = ref(1),
  kind = ref(parts[0] === 'detail' ? parts[1] : 'movie'),
  list = ref('popular'),
  query = ref(routeParams.get('query') || routeParams.get('q') || ''),
  hasMore = ref(false),
  results = ref<any[]>([]),
  total = ref(0),
  cached = ref(''),
  detail = ref<any>(null),
  person = ref<any>(null),
  credits = ref<any[]>([]),
  season = ref(1),
  seasonData = ref<any>(null),
  seasonLoading = ref(false),
  selected = ref<number[]>([]),
  loading = ref(parts[0] !== 'search'),
  error = ref('');
const visibleResults = computed(() => results.value);
const filteredCredits = computed(() =>
  credits.value
    .filter((m) => creditsKind.value === 'all' || m.media_type === creditsKind.value)
    .sort((a, b) =>
      creditsSort.value === 'year'
        ? (b.year || 0) - (a.year || 0) || b.popularity - a.popularity
        : b.popularity - a.popularity || b.year - a.year,
    ),
);
async function load() {
  const generation = ++loadSeq;
  loading.value = true;
  error.value = '';
  try {
    if (parts[0] === 'detail') {
      detail.value = await api('/catalog/detail?kind=' + kind.value + '&id=' + parts[2]);
      season.value =
        detail.value.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
      if (kind.value === 'tv') await loadSeason();
    } else if (parts[0] === 'person') {
      [person.value, credits.value] = await Promise.all([
        api('/catalog/person?id=' + parts[1]),
        api('/catalog/person/credits?id=' + parts[1]).then((r: any) => r.results || []),
      ]);
    } else {
      const q = new URLSearchParams({ page: String(page.value) });
      if (query.value.trim()) {
        q.set('query', query.value.trim());
      } else {
        q.set('kind', kind.value!);
        q.set('list', list.value);
      }
      const r = await api('/catalog/' + (query.value ? 'search' : 'browse') + '?' + q);
      if (generation !== loadSeq) return;
      results.value = r.results || [];
      total.value = r.total_pages || 0;
      hasMore.value = r.has_more ?? page.value < total.value;
      cached.value = r.cached_at || '';
    }
  } catch (e) {
    if (generation === loadSeq) error.value = errorText(e);
  } finally {
    if (generation === loadSeq) loading.value = false;
  }
}
async function loadSeason() {
  const generation = ++seasonSeq;
  selected.value = [];
  seasonData.value = null;
  seasonLoading.value = true;
  try {
    const result = await action(() =>
      api('/catalog/season?id=' + parts[2] + '&season=' + season.value),
    );
    if (generation === seasonSeq) seasonData.value = result;
  } finally {
    if (generation === seasonSeq) seasonLoading.value = false;
  }
}
function change() {
  page.value = 1;
  load();
}
function resource() {
  startSearch(
    'resources/' +
      kind.value +
      '/' +
      detail.value.id +
      '?season=' +
      season.value +
      '&episodes=' +
      selected.value.join(','),
  );
}
if (parts[0] === 'search') {
  useExplicitSearch(props.route, () => {
    page.value = 1;
    return load();
  });
}
function subscribe() {
  go(
    'subscriptions/new/' +
      kind.value +
      '/' +
      detail.value.id +
      '?season=' +
      season.value +
      '&start_episode=1',
  );
}
onMounted(() => {
  if (parts[0] !== 'search') void load();
});
</script>
<template>
  <div v-if="error" class="error-box">
    {{ error }} <button class="small" @click="load">重试</button
    ><button v-if="!ui.settings?.secret_state?.tmdb" class="small" @click="go('settings')">
      检查 TMDB 设置
    </button>
  </div>
  <template v-if="parts[0] === 'discover' || parts[0] === 'search'"
    ><h2 v-if="parts[0] === 'search' && query" class="search-keyword">{{ query }}</h2>
    <div v-if="parts[0] === 'discover'" class="toolbar">
      <div class="segmented">
        <button
          :class="{ selected: kind === 'movie' }"
          @click="
            kind = 'movie';
            change();
          "
        >
          电影</button
        ><button
          :class="{ selected: kind === 'tv' }"
          @click="
            kind = 'tv';
            change();
          "
        >
          剧集
        </button>
      </div>
      <select
        v-if="!query"
        v-model="list"
        @change="
          query = '';
          change();
        "
      >
        <option value="popular">热门推荐</option>
        <option value="top_rated">高分榜单</option>
        <option value="trending_day">今日趋势</option></select
      ><span v-if="cached" class="hint push-right">更新于 {{ date(cached) }}</span>
    </div>
    <div v-if="loading" class="skeleton-grid">
      <div v-for="i in 12" :key="i" class="skeleton"></div>
    </div>
    <template v-else
      ><MediaCards :items="visibleResults" :quick-actions="true" />
      <div v-if="!visibleResults.length && !error" class="empty-state">
        <span>⌕</span>
        <h3>{{ query ? '没有找到相关作品' : '暂无搜索结果' }}</h3>
        <p>
          {{ query ? '点击顶部搜索按钮搜索，或尝试其他关键词。' : '在顶部输入关键词并点击搜索。' }}
        </p>
      </div>
      <div class="pagination" v-if="total > 1 || hasMore">
        <button
          :disabled="page <= 1"
          @click="
            page--;
            load();
          "
        >
          上一页</button
        ><span>{{ page }} / {{ total }}</span
        ><button
          :disabled="!hasMore"
          @click="
            page++;
            load();
          "
        >
          下一页
        </button>
      </div></template
    ></template
  >
  <template v-else-if="detail"
    ><section
      class="detail-hero"
      :style="
        image(detail.backdrop_path, 'w1280')
          ? {
              backgroundImage: `linear-gradient(180deg,rgba(244,245,250,.72),rgba(244,245,250,.98)),url(${image(detail.backdrop_path, 'w1280')})`,
            }
          : {}
      "
    >
      <img
        v-if="image(detail.poster_path)"
        class="detail-poster"
        :src="image(detail.poster_path)"
        :alt="detail.title"
      />
      <div class="detail-copy">
        <p class="eyebrow">{{ label(detail.media_type) }} · {{ detail.year || '上映待定' }}</p>
        <h1>{{ detail.title }}</h1>
        <p class="original-title">{{ detail.original_title }}</p>
        <div class="tags">
          <span class="gold" v-if="detail.vote_average">★ {{ detail.vote_average.toFixed(1) }}</span
          ><span v-for="g in detail.genres" :key="g.id">{{ g.name }}</span
          ><span v-if="detail.runtime">{{ detail.runtime }} 分钟</span
          ><span>{{ detail.status }}</span>
        </div>
        <p class="overview">{{ detail.overview || '暂无剧情简介。' }}</p>
        <div class="button-row">
          <button
            class="detail-search"
            @click="resource"
            :disabled="kind === 'tv' && (!seasonData || seasonLoading)"
          >
            ⌕ 搜索资源</button
          ><button
            class="detail-subscribe"
            @click="subscribe"
            :disabled="kind === 'tv' && (!seasonData || seasonLoading)"
          >
            ＋ 添加订阅
          </button>
        </div>
        <div class="tags">
          <span v-if="['present', 'managed', 'external'].includes(detail.library_state)"
            >已入库</span
          ><span v-if="detail.subscription_id">已订阅</span
          ><span v-if="detail.download_state">{{ label(detail.download_state) }}</span>
        </div>
        <p class="hint">TMDB {{ detail.id }} · {{ detail.release_date }}</p>
      </div>
    </section>
    <section class="panel" v-if="kind === 'tv'">
      <div class="section-heading">
        <h2>季度</h2>
        <select v-model.number="season" @change="loadSeason">
          <option v-for="s in detail.seasons" :key="s.season_number" :value="s.season_number">
            {{ s.name }} · {{ s.episode_count }} 集
          </option>
        </select>
      </div>
      <p class="hint">订阅以季为单位，添加订阅时可设置从哪一集开始，后续更新自动追踪。</p>
      <p v-if="seasonLoading" class="hint">正在加载所选季度的标准单集信息…</p>
      <button v-else-if="!seasonData" class="small" @click="loadSeason">重试加载本季</button>
      <details>
        <summary>本季剧集 · 可选集数搜索资源</summary>
        <div class="episode-grid">
          <label
            v-for="e in seasonData?.episodes || []"
            :key="e.id"
            :class="['episode-option', { selected: selected.includes(e.episode_number) }]"
            ><input type="checkbox" :value="e.episode_number" v-model="selected" /><span
              ><strong>E{{ String(e.episode_number).padStart(2, '0') }} · {{ e.name }}</strong
              ><small>{{ e.air_date || '日期待定' }}</small></span
            ></label
          >
        </div>
      </details>
    </section>
    <section v-if="detail.cast?.length" class="section">
      <div class="section-heading">
        <h2>演员阵容</h2>
        <span class="hint">点击查看人物资料与参演作品</span>
      </div>
      <div class="cast-row">
        <button
          v-for="p in detail.cast.slice(0, 20)"
          :key="p.id"
          class="cast-card"
          @click="go('person/' + p.id)"
        >
          <img
            v-if="image(p.profile_path, 'w185')"
            :src="image(p.profile_path, 'w185')"
            :alt="p.name"
            loading="lazy"
          />
          <div v-else class="cast-placeholder">人</div>
          <strong>{{ p.name }}</strong
          ><small>{{ p.character }}</small>
        </button>
      </div>
    </section>
    <section v-if="detail.recommendations?.length" class="section">
      <div class="section-heading"><h2>你可能也喜欢</h2></div>
      <MediaCards :items="detail.recommendations" /></section
  ></template>
  <template v-else-if="person"
    ><section class="panel person-header">
      <img v-if="image(person.profile_path)" :src="image(person.profile_path)" :alt="person.name" />
      <div>
        <p class="eyebrow">{{ person.known_for_department }}</p>
        <h1>{{ person.name }}</h1>
        <p class="muted">
          {{ person.birthday || '生日未知' }} · {{ person.place_of_birth || '出生地未知' }}
        </p>
        <p v-if="person.also_known_as?.length" class="hint">
          又名：{{ person.also_known_as.join(' / ') }}
        </p>
        <p class="biography">{{ person.biography || '暂无人物简介。' }}</p>
      </div>
    </section>
    <section class="section">
      <div class="section-heading">
        <h2>参演与创作作品</h2>
        <select v-model="creditsSort" @change="creditsLimit = 24">
          <option value="popularity">按热度排序</option>
          <option value="year">按年份排序（新 → 旧）</option></select
        ><select v-model="creditsKind" @change="creditsLimit = 24">
          <option value="all">全部作品</option>
          <option value="movie">电影</option>
          <option value="tv">剧集</option>
        </select>
      </div>
      <MediaCards :items="filteredCredits.slice(0, creditsLimit)" />
      <div class="pagination" v-if="filteredCredits.length > creditsLimit">
        <button @click="creditsLimit += 24">加载更多 · 共 {{ filteredCredits.length }} 部</button>
      </div>
    </section></template
  >
  <div v-else-if="loading" class="panel loading-state">正在加载影视资料…</div>
</template>
<style scoped>
.search-keyword {
  margin: 12px 16px 24px;
  color: var(--accent);
  font-size: 24px;
  font-weight: 500;
  line-height: 32px;
}
.search-keyword ~ .media-grid {
  margin: 0 8px;
}
.toolbar {
  gap: 12px;
  margin-bottom: 22px;
}
.toolbar > select {
  min-width: 140px;
  width: auto;
}
.pagination {
  gap: 12px;
  margin-top: 32px;
  padding-top: 22px;
  border-top: 1px solid var(--line);
}
.pagination > span {
  min-width: 68px;
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  text-align: center;
}
.detail-hero {
  gap: 32px;
  padding: 32px;
  border-radius: var(--radius);
  background-color: var(--bg);
  color: var(--text);
}
.detail-poster {
  width: 184px;
  height: 276px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
}
.detail-copy h1 {
  margin: 5px 0 8px;
  color: var(--text);
  font-size: clamp(26px, 2.7vw, 36px);
  line-height: 1.3;
  letter-spacing: -0.6px;
}
.detail-copy .eyebrow {
  color: var(--muted);
  font-size: 11px;
}
.detail-copy .overview {
  margin: 17px 0 21px;
  color: var(--text);
  font-size: 13px;
  line-height: 1.9;
}
.detail-copy .tags span {
  padding: 4px 8px;
  border: 1px solid #ffffff20;
  border-radius: 6px;
  background: var(--surface);
  color: var(--muted);
}
.detail-copy .tags .gold {
  color: #efce87;
}
.detail-copy .hint {
  color: var(--muted);
}
.detail-copy .original-title {
  color: var(--muted);
}
.detail-copy .button-row .detail-search {
  color: var(--info);
  background: rgba(22, 177, 255, 0.12);
  border-color: transparent;
}
.detail-copy .button-row .detail-subscribe {
  color: var(--danger);
  background: rgba(255, 76, 81, 0.12);
  border-color: transparent;
}
.episode-option {
  padding: 13px;
  border-color: var(--line);
  border-radius: var(--radius);
  background: #f8fafb;
}
.episode-option.selected {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.episode-option strong {
  color: var(--text);
  font-size: 12px;
}
.episode-option small {
  color: var(--muted);
}
.cast-row {
  gap: 17px;
  padding: 2px 0 16px;
}
.cast-card {
  flex-basis: 105px;
}
.cast-card > img,
.cast-placeholder {
  border-radius: var(--radius);
}
.cast-card strong {
  margin-top: 10px;
  color: var(--text);
  font-size: 12px;
}
.cast-card small {
  margin-top: 4px;
  color: var(--muted);
  font-size: 11px;
}
.person-header {
  align-items: flex-start;
  gap: 28px;
  padding: 28px;
  border-radius: var(--radius);
}
.person-header > img {
  width: 155px;
  border-radius: var(--radius);
}
.person-header h1 {
  margin: 6px 0 12px;
  color: var(--text);
  font-size: 28px;
}
.person-header .eyebrow {
  color: var(--accent);
}
.biography {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.9;
}
@media (max-width: 760px) {
  .toolbar {
    flex-wrap: wrap;
  }
  .detail-hero {
    gap: 22px;
    padding: 25px;
  }
  .detail-poster {
    width: 140px;
    height: 210px;
  }
  .detail-copy .overview {
    font-size: 12px;
  }
  .person-header {
    gap: 20px;
    padding: 22px;
  }
  .person-header > img {
    width: 120px;
  }
}
@media (max-width: 520px) {
  .detail-hero {
    flex-direction: column;
    align-items: flex-start;
    padding: 23px;
  }
  .detail-poster {
    width: 126px;
    height: 189px;
  }
  .detail-copy h1 {
    font-size: 27px;
  }
  .person-header {
    flex-direction: column;
    gap: 16px;
  }
  .person-header h1 {
    font-size: 25px;
  }
  .section-heading {
    flex-wrap: wrap;
  }
}
</style>
