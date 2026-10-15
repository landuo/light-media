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
const parts = props.route.split('/'),
  page = ref(1),
  kind = ref(parts[0] === 'detail' ? parts[1] : 'movie'),
  list = ref('popular'),
  query = ref(''),
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
      const q = new URLSearchParams({ kind: kind.value!, page: String(page.value) });
      if (query.value) {
        q.set('q', query.value);
      } else q.set('list', list.value);
      const r = await api('/catalog/' + (query.value ? 'search' : 'browse') + '?' + q);
      if (generation !== loadSeq) return;
      results.value = r.results || [];
      total.value = r.total_pages || 0;
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
function subscribe() {
  go(
    'subscriptions/new/' +
      kind.value +
      '/' +
      detail.value.id +
      '?season=' +
      season.value +
      '&episodes=' +
      selected.value.join(','),
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
  <template v-if="parts[0] === 'discover'"
    ><div class="page-heading">
      <div>
        <p class="eyebrow">DISCOVER</p>
        <h1>发现下一部好作品</h1>
        <p class="muted">从榜单开始，让喜欢的故事自动来到媒体库。</p>
      </div>
      <form class="search-field" @submit.prevent="change">
        <span>⌕</span
        ><input v-model="query" placeholder="搜索电影、剧集…" aria-label="搜索影视" /><button
          class="small primary"
        >
          搜索
        </button>
      </form>
    </div>
    <div class="toolbar">
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
      ><MediaCards :items="results" />
      <div v-if="!results.length && !error" class="empty-state">
        <span>⌕</span>
        <h3>没有找到相关作品</h3>
        <p>试试原名、中文名或其他关键词。</p>
      </div>
      <div class="pagination" v-if="total > 1">
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
          :disabled="page >= total"
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
        <h2>季度与剧集</h2>
        <select v-model.number="season" @change="loadSeason">
          <option v-for="s in detail.seasons" :key="s.season_number" :value="s.season_number">
            {{ s.name }} · {{ s.episode_count }} 集
          </option>
        </select>
      </div>
      <p class="hint">选择需要的集数后搜索或订阅；不选择表示该季全部。</p>
      <p v-if="seasonLoading" class="hint">正在加载所选季度的标准单集信息…</p>
      <button v-else-if="!seasonData" class="small" @click="loadSeason">重试加载本季</button>
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
