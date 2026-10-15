<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { api, action, go, image, date, ui, label, errorText } from '../api';
import MediaCards from './MediaCards.vue';
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
  searchKind = ref('all'),
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
  loading = ref(true),
  error = ref('');
const visibleResults = computed(() =>
  results.value.filter(
    (m) => !query.value || searchKind.value === 'all' || m.media_type === searchKind.value,
  ),
);
const spotlight = computed(() => results.value.find((m) => m.backdrop_path) || results.value[0]);
const listName = computed(
  () =>
    ({ popular: '热门推荐', top_rated: '高分榜单', trending_day: '今日趋势' })[list.value] ||
    '热门推荐',
);
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
  go(
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
function submitSearch() {
  if (query.value.trim()) {
    const target = 'search?query=' + encodeURIComponent(query.value.trim());
    if (props.route === target) {
      page.value = 1;
      load();
    } else go(target);
  } else {
    page.value = 1;
    load();
  }
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
onMounted(load);
</script>
<template>
  <div v-if="error" class="error-box">
    {{ error }} <button class="small" @click="load">重试</button
    ><button v-if="!ui.settings?.secret_state?.tmdb" class="small" @click="go('settings')">
      检查 TMDB 设置
    </button>
  </div>
  <template v-if="parts[0] === 'discover' || parts[0] === 'search'"
    ><div v-if="parts[0] === 'search'" class="page-actions">
      <form class="search-field" @submit.prevent="submitSearch">
        <span>⌕</span
        ><input v-model="query" placeholder="搜索电影、剧集…" aria-label="搜索影视" /><button
          class="small primary"
        >
          搜索
        </button>
      </form>
    </div>
    <section
      v-if="!query && !loading && spotlight"
      class="discovery-spotlight"
      :style="
        image(spotlight.backdrop_path, 'w1280')
          ? {
              backgroundImage: `linear-gradient(90deg,rgba(13,31,39,.97) 0%,rgba(13,31,39,.81) 38%,rgba(13,31,39,.12) 100%),url(${image(spotlight.backdrop_path, 'w1280')})`,
            }
          : {}
      "
    >
      <div class="spotlight-copy">
        <span class="spotlight-kicker"
          ><span></span>{{ listName }} · {{ label(spotlight.media_type) }}</span
        >
        <h2>{{ spotlight.title }}</h2>
        <p class="spotlight-meta">
          <span v-if="spotlight.vote_average > 0" class="spotlight-rating"
            >★ {{ spotlight.vote_average.toFixed(1) }}</span
          >{{ spotlight.year || '上映待定' }}
        </p>
        <p v-if="spotlight.overview" class="spotlight-overview">{{ spotlight.overview }}</p>
        <div class="spotlight-actions">
          <button @click="go('detail/' + spotlight.media_type + '/' + spotlight.id)">
            查看详情 <span aria-hidden="true">↗</span>
          </button>
          <button
            class="spotlight-secondary"
            @click="go('resources/' + spotlight.media_type + '/' + spotlight.id)"
          >
            搜索资源
          </button>
        </div>
      </div>
    </section>
    <div class="toolbar">
      <div v-if="query" class="segmented">
        <button class="selected">影视</button>
        <button @click="go('resource-search?query=' + encodeURIComponent(query))">资源</button>
      </div>
      <div v-if="query" class="segmented">
        <button
          v-for="t in [
            { id: 'all', name: '全部' },
            { id: 'movie', name: '电影' },
            { id: 'tv', name: '剧集' },
          ]"
          :key="t.id"
          :class="{ selected: searchKind === t.id }"
          @click="searchKind = t.id"
        >
          {{ t.name }}
          <small>{{ results.filter((m) => t.id === 'all' || m.media_type === t.id).length }}</small>
        </button>
      </div>
      <div v-else class="segmented">
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
        <h3>没有找到相关作品</h3>
        <p>试试原名、中文名或其他关键词。</p>
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
              backgroundImage: `linear-gradient(90deg,rgba(15,20,30,.98),rgba(15,20,30,.72)),url(${image(detail.backdrop_path, 'w1280')})`,
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
            class="primary"
            @click="resource"
            :disabled="kind === 'tv' && (!seasonData || seasonLoading)"
          >
            ⌕ 搜索资源</button
          ><button @click="subscribe" :disabled="kind === 'tv' && (!seasonData || seasonLoading)">
            ＋ 添加订阅
          </button>
        </div>
        <div class="tags">
          <span v-if="detail.library_state">{{
            detail.library_state === 'present' ? '已入库' : label(detail.library_state)
          }}</span
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
.discovery-spotlight {
  position: relative;
  min-height: 290px;
  padding: 31px 36px;
  margin: 0 0 25px;
  border: 1px solid #ffffff18;
  border-radius: 14px;
  background: #152f39;
  background-size: cover;
  background-position: center 35%;
  overflow: hidden;
  color: #fff;
  box-shadow: 0 8px 24px #172b3a10;
}
.spotlight-copy {
  max-width: 520px;
}
.spotlight-kicker {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #cfdfdf;
  font-size: 11px;
  font-weight: 550;
  letter-spacing: 1px;
}
.spotlight-kicker > span {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #73c9b7;
  box-shadow: 0 0 0 4px #73c9b715;
}
.spotlight-copy h2 {
  margin: 18px 0 10px;
  color: #fff;
  font-size: clamp(27px, 2.6vw, 36px);
  line-height: 1.3;
  font-weight: 700;
  letter-spacing: -0.8px;
}
.spotlight-meta {
  display: flex;
  gap: 16px;
  margin: 0 0 12px;
  color: #bbd0d3;
  font-size: 12px;
}
.spotlight-rating {
  color: #f3cf7b;
  font-weight: 600;
}
.spotlight-overview {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  max-width: 440px;
  margin-bottom: 23px;
  color: #c9d8db;
  font-size: 13px;
  line-height: 1.85;
}
.spotlight-actions {
  display: flex;
  gap: 10px;
  margin-top: 22px;
}
.spotlight-actions button {
  min-height: 36px;
  padding: 9px 16px;
  border-color: #fff;
  border-radius: 8px;
  background: #fff;
  color: #1c3e45;
  font-size: 12px;
  font-weight: 600;
}
.spotlight-actions button:hover {
  border-color: #dbf1eb;
  background: #dbf1eb;
  color: #126a61;
}
.spotlight-actions .spotlight-secondary {
  border-color: #ffffff38;
  background: #ffffff0a;
  color: #fff;
}
.spotlight-actions .spotlight-secondary:hover {
  border-color: #ffffff70;
  background: #ffffff16;
  color: #fff;
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
  border-radius: 14px;
  background-color: #17323b;
}
.detail-poster {
  width: 184px;
  height: 276px;
  border: 1px solid #ffffff20;
  border-radius: 10px;
}
.detail-copy h1 {
  margin: 5px 0 8px;
  color: #fff;
  font-size: clamp(26px, 2.7vw, 36px);
  line-height: 1.3;
  letter-spacing: -0.6px;
}
.detail-copy .eyebrow {
  color: #a2cdca;
  font-size: 11px;
}
.detail-copy .overview {
  margin: 17px 0 21px;
  color: #cfdbdf;
  font-size: 13px;
  line-height: 1.9;
}
.detail-copy .tags span {
  padding: 4px 8px;
  border: 1px solid #ffffff20;
  border-radius: 6px;
  background: #ffffff08;
  color: #d3e0e3;
}
.detail-copy .tags .gold {
  color: #efce87;
}
.detail-copy .hint {
  color: #adc3cb;
}
.episode-option {
  padding: 13px;
  border-color: var(--line);
  border-radius: 9px;
  background: #f8fafb;
}
.episode-option.selected {
  border-color: #9acbc4;
  background: var(--accent-soft);
}
.episode-option strong {
  color: #344d5b;
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
  border-radius: 10px;
}
.cast-card strong {
  margin-top: 10px;
  color: #243d4c;
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
  border-radius: 14px;
}
.person-header > img {
  width: 155px;
  border-radius: 10px;
}
.person-header h1 {
  margin: 6px 0 12px;
  color: #172b3a;
  font-size: 28px;
}
.person-header .eyebrow {
  color: var(--accent);
}
.biography {
  color: #5b707e;
  font-size: 13px;
  line-height: 1.9;
}
@media (max-width: 760px) {
  .discovery-spotlight {
    min-height: 270px;
    padding: 27px;
    background-position: 60% center;
  }
  .spotlight-copy {
    max-width: 420px;
  }
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
  .discovery-spotlight {
    min-height: 255px;
    padding: 23px;
    border-radius: 12px;
  }
  .spotlight-copy h2 {
    margin-top: 18px;
    font-size: 27px;
  }
  .spotlight-overview {
    max-width: 330px;
    font-size: 12px;
  }
  .spotlight-actions button {
    padding: 8px 13px;
  }
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
