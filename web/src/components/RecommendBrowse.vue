<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watch,
} from 'vue';
import { useDisplay } from 'vuetify';
import { VInfiniteScroll } from 'vuetify/components/VInfiniteScroll';
import { VBtn } from 'vuetify/components/VBtn';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { api, errorText } from '../api';
import MediaCards from './MediaCards.vue';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
  type ExploreLoadStatus,
  type ExplorePage,
} from '../features/explore-feed';
import { recommendBrowseSource, recommendPath, type RecommendSource } from '../features/recommend';

const props = withDefaults(defineProps<{ route: string; active?: boolean }>(), { active: true });
const display = useDisplay();
const source = computed(() => recommendBrowseSource(props.route));
const title = ref(source.value.title);
const feed = reactive(createExploreFeed());
const activated = ref(true),
  scrollY = ref(0);
const visible = computed(() => props.active && activated.value);
let wasDeactivated = false,
  lastScroll = 0,
  titleGeneration = 0;
async function load({ done }: { done: (status: ExploreLoadStatus) => void }) {
  if (!visible.value) return done('ok');
  if (feed.error) return done('error');
  if (!source.value.id) {
    feed.error = '推荐来源不存在';
    return done('error');
  }
  const id = source.value.id;
  done(await loadExplorePage(feed, (page) => api<ExplorePage>(recommendPath(id, page)), errorText));
}
function retry(props: { onClick?: () => void }) {
  feed.error = '';
  props.onClick?.();
}
async function loadTitle() {
  const token = ++titleGeneration;
  title.value = source.value.title;
  if (title.value || !source.value.id) return;
  try {
    const response = await api<{ sources: RecommendSource[] }>('/catalog/recommend/sources');
    if (token === titleGeneration)
      title.value = response.sources.find((item) => item.id === source.value.id)?.title || '';
  } catch {
    // The feed's own retry state reports source errors; the title is optional.
  }
}
function onScroll() {
  if (visible.value) scrollY.value = window.scrollY;
}
function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' });
}
watch(
  () => source.value.id,
  () => {
    resetExploreFeed(feed);
    lastScroll = 0;
    void loadTitle();
  },
);
onMounted(() => {
  void loadTitle();
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });
});
onActivated(() => {
  activated.value = true;
  if (wasDeactivated) void nextTick(() => window.scrollTo({ top: lastScroll, behavior: 'auto' }));
});
onDeactivated(() => {
  lastScroll = scrollY.value;
  wasDeactivated = true;
  activated.value = false;
});
onUnmounted(() => {
  titleGeneration++;
  resetExploreFeed(feed);
  window.removeEventListener('scroll', onScroll);
});
</script>

<template>
  <div class="recommend-browse">
    <h2 v-if="title" class="browse-title">{{ title }}</h2>
    <div
      v-if="!feed.loaded && !feed.error"
      class="browse-loading"
      role="status"
      aria-label="正在加载"
    >
      <VProgressCircular indeterminate color="primary" />
    </div>
    <VInfiniteScroll
      v-if="visible"
      :key="source.id + ':' + feed.generation"
      mode="intersect"
      side="end"
      :items="feed.results"
      class="browse-results"
      @load="load"
    >
      <template #loading />
      <template #empty />
      <template #error="{ props: retryProps }"
        ><div class="browse-error" role="alert">
          <span>{{ feed.error }}</span
          ><VBtn variant="text" size="small" @click="retry(retryProps)">重试</VBtn>
        </div></template
      >
      <MediaCards v-if="feed.results.length" :items="feed.results" quick-actions original-actions />
      <div v-else-if="feed.loaded && !feed.hasMore" class="empty">没有找到数据</div>
    </VInfiniteScroll>
    <Teleport to="body"
      ><button
        v-if="visible && scrollY > 200 && display.smAndUp.value"
        class="browse-scroll-top"
        aria-label="返回顶部"
        @click="scrollToTop"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path
            d="M7 14L12 9L17 14"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg></button
    ></Teleport>
  </div>
</template>

<style scoped>
.recommend-browse {
  padding-top: 12px;
}
.browse-title {
  margin: 0 0 12px;
  padding: 0 16px;
  color: rgb(var(--v-theme-primary));
  font-size: 1.5rem;
  font-weight: 600;
  line-height: 32px;
}
.browse-results {
  overflow: visible;
  padding: 12px 8px 0;
}
.browse-results :deep(.media-card),
.browse-results :deep(.poster-action) {
  border-radius: 12px;
}
.browse-loading {
  display: flex;
  justify-content: center;
  margin-top: 48px;
}
.browse-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--danger);
}
.browse-scroll-top {
  position: fixed;
  bottom: 2rem;
  right: 2rem;
  width: 44px;
  height: 44px;
  padding: 0;
  z-index: 100;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.05);
  border-radius: 50%;
  background: rgba(var(--v-theme-background), 0.8);
  color: rgb(var(--v-theme-on-surface));
  box-shadow: 0 5px 15px rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(10px);
  transition: all 0.3s ease;
}
.browse-scroll-top:hover {
  background: rgba(var(--v-theme-background), 0.95);
  color: rgb(var(--v-theme-primary));
  transform: translateY(-4px);
}
</style>
