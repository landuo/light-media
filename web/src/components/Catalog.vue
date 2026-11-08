<script setup lang="ts">
import CatalogRelated from './CatalogRelated.vue';
import MediaDetailLayout from './MediaDetailLayout.vue';
import MediaDetailSeasons from './MediaDetailSeasons.vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VSelect } from 'vuetify/components/VSelect';
import { VInfiniteScroll } from 'vuetify/components/VInfiniteScroll';
import { ref, shallowRef, onMounted, onDeactivated, onUnmounted, computed } from 'vue';
import { api, action, go, image, date, ui, errorText } from '../api';
import MediaCards from './MediaCards.vue';
import Subscriptions from './Subscriptions.vue';
import ExploreActions from './ExploreActions.vue';
import { startSearch, useExplicitSearch } from '../features/search-session';
import { mediaDetailSeasons } from '../features/subscriptions';
let loadSeq = 0,
  seasonSeq = 0;
const creditsKind = ref('all'),
  creditsLimit = ref(24),
  creditsSort = ref('popularity');
const props = defineProps<{ route: string }>();
const subscriptionEditor = shallowRef<{ view: string; media: any } | null>(null);
const subscriptionActions = ref<InstanceType<typeof ExploreActions>>();
const [routePath, routeQuery] = props.route.split('?'),
  routeParams = new URLSearchParams(routeQuery),
  parts = routePath!.split('/'),
  page = ref(parts[0] === 'discover' ? 0 : 1),
  feedVersion = ref(0),
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
  loading = ref(!['search', 'discover'].includes(parts[0]!)),
  error = ref('');
const visibleResults = computed(() => results.value);
const detailSeasons = computed(() => mediaDetailSeasons(detail.value));
const filteredCredits = computed(() =>
  credits.value
    .filter((m) => creditsKind.value === 'all' || m.media_type === creditsKind.value)
    .sort((a, b) =>
      creditsSort.value === 'year'
        ? (b.year || 0) - (a.year || 0) || b.popularity - a.popularity
        : b.popularity - a.popularity || b.year - a.year,
    ),
);
async function loadRecommendations({ done }: { done: (status: 'ok' | 'empty' | 'error') => void }) {
  if (loading.value) return;
  const generation = loadSeq,
    nextPage = page.value + 1;
  loading.value = true;
  error.value = '';
  try {
    const params = new URLSearchParams({
      page: String(nextPage),
      kind: kind.value!,
      list: list.value,
    });
    const result = await api('/catalog/browse?' + params);
    if (generation !== loadSeq) return done('empty');
    const incoming = result.results || [],
      seen = new Set(results.value.map((item) => `${item.media_type}/${item.id}`));
    results.value = results.value.concat(
      incoming.filter((item: any) => {
        const key = `${item.media_type}/${item.id}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      }),
    );
    page.value = nextPage;
    total.value = result.total_pages || 0;
    hasMore.value = incoming.length > 0 && (result.has_more ?? nextPage < total.value);
    cached.value = result.cached_at || '';
    done(hasMore.value ? 'ok' : 'empty');
  } catch (e) {
    if (generation !== loadSeq) return done('empty');
    error.value = errorText(e);
    done('error');
  } finally {
    if (generation === loadSeq) loading.value = false;
  }
}
async function load() {
  const generation = ++loadSeq;
  loading.value = true;
  error.value = '';
  try {
    if (parts[0] === 'detail') {
      detail.value = await api('/catalog/detail?kind=' + kind.value + '&id=' + parts[2]);
      season.value = detailSeasons.value.find((s: any) => s.season_number > 0)?.season_number || 1;
      if (kind.value === 'tv') void loadSeason();
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
  ++loadSeq;
  page.value = 0;
  results.value = [];
  total.value = 0;
  hasMore.value = false;
  cached.value = '';
  error.value = '';
  loading.value = false;
  ++feedVersion.value;
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
function openSubscriptionEditor(media: any, selectedSeason?: number) {
  const query = selectedSeason === undefined ? '' : `?season=${selectedSeason}&start_episode=1`;
  subscriptionEditor.value = {
    view: `subscriptions/new/${media.media_type}/${media.id}${query}`,
    media,
  };
}
function openSubscription(media: any) {
  if (media.media_type === 'tv') subscriptionActions.value?.open(media, 'add-seasons');
  else openSubscriptionEditor(media);
}
function subscriptionSaved(subscription: any) {
  const media = subscriptionEditor.value?.media;
  if (media && subscription?.id) media.subscription_id ||= subscription.id;
}
onMounted(() => {
  if (!['search', 'discover'].includes(parts[0]!)) void load();
});
onDeactivated(() => {
  subscriptionEditor.value = null;
});
onUnmounted(() => {
  ++loadSeq;
  ++seasonSeq;
});
</script>
<template>
  <div v-if="error && parts[0] !== 'discover'" class="error-box">
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
      <VSelect
        v-if="!query"
        v-model="list"
        @update:model-value="
          query = '';
          change();
        "
        density="compact"
        aria-label="推荐榜单"
        :items="
          ['热门推荐', '高分榜单', '今日趋势'].map((title, i) => ({
            title,
            value: ['popular', 'top_rated', 'trending_day'][i],
          }))
        "
      /><span v-if="cached" class="hint push-right">更新于 {{ date(cached) }}</span>
    </div>
    <VInfiniteScroll
      v-if="parts[0] === 'discover'"
      :key="feedVersion"
      mode="intersect"
      side="end"
      @load="loadRecommendations"
    >
      <div v-if="loading && !visibleResults.length" class="skeleton-grid">
        <div v-for="i in 12" :key="i" class="skeleton"></div>
      </div>
      <MediaCards
        v-else
        :items="visibleResults"
        :quick-actions="true"
        @subscribe="openSubscription"
      />
      <template #loading><span class="hint" role="status">正在加载…</span></template>
      <template #empty>
        <span class="hint" role="status">{{
          visibleResults.length ? '没有更多内容了' : '暂无推荐内容'
        }}</span>
      </template>
      <template #error="{ props: retryProps }">
        <div class="error-box" role="alert">
          {{ error }} <button class="small" @click="retryProps.onClick">重试</button>
          <button v-if="!ui.settings?.secret_state?.tmdb" class="small" @click="go('settings')">
            检查 TMDB 设置
          </button>
        </div>
      </template>
    </VInfiniteScroll>
    <div v-else-if="loading" class="skeleton-grid">
      <div v-for="i in 12" :key="i" class="skeleton"></div>
    </div>
    <template v-else
      ><MediaCards :items="visibleResults" :quick-actions="true" @subscribe="openSubscription" />
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
  <template v-else-if="detail">
    <MediaDetailLayout
      :media="detail"
      :poster-url="image(detail.poster_path, 'w500')"
      :backdrop-url="image(detail.backdrop_path, 'w1280')"
    >
      <template #actions>
        <VBtn
          color="primary"
          variant="tonal"
          prepend-icon="mdi-magnify"
          :disabled="kind === 'tv' && (!seasonData || seasonLoading)"
          @click="resource"
        >搜索资源</VBtn>
        <VBtn color="warning" variant="tonal" prepend-icon="mdi-heart-outline" @click="openSubscription(detail)">
          添加订阅
        </VBtn>
      </template>
      <template v-if="kind === 'tv'" #seasons>
        <MediaDetailSeasons
          v-model:season="season"
          v-model:selected="selected"
          :seasons="detailSeasons"
          :season-data="seasonData"
          :loading="seasonLoading"
          :media-id="detail.id"
          :groups="detail.original_media?.episode_groups"
          @change-season="loadSeason"
          @retry="loadSeason"
        />
      </template>
    </MediaDetailLayout>
    <CatalogRelated :key="route + 'credits'" :media="detail" part="credits" />
    <CatalogRelated :key="route + 'recommendations'" :media="detail" part="recommendations" />
  </template>
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
        <VSelect
          v-model="creditsSort"
          @update:model-value="creditsLimit = 24"
          density="compact"
          aria-label="作品排序"
          :items="[
            { title: '按热度排序', value: 'popularity' },
            { title: '按年份排序（新 → 旧）', value: 'year' },
          ]"
        /><VSelect
          v-model="creditsKind"
          @update:model-value="creditsLimit = 24"
          density="compact"
          aria-label="作品类型"
          :items="[
            { title: '全部作品', value: 'all' },
            { title: '电影', value: 'movie' },
            { title: '剧集', value: 'tv' },
          ]"
        />
      </div>
      <MediaCards :items="filteredCredits.slice(0, creditsLimit)" />
      <div class="pagination" v-if="filteredCredits.length > creditsLimit">
        <button @click="creditsLimit += 24">加载更多 · 共 {{ filteredCredits.length }} 部</button>
      </div>
    </section></template
  >
  <div v-else-if="loading" class="panel loading-state">正在加载影视资料…</div>
  <ExploreActions ref="subscriptionActions" @single-season="openSubscriptionEditor" />
  <Teleport v-if="subscriptionEditor" to="body">
    <Subscriptions
      editor-only
      :view="subscriptionEditor.view"
      @close="subscriptionEditor = null"
      @saved="subscriptionSaved"
    />
  </Teleport>
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
  .person-header {
    gap: 20px;
    padding: 22px;
  }
  .person-header > img {
    width: 120px;
  }
}
@media (max-width: 520px) {
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
