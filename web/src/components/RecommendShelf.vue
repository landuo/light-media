<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { useDisplay } from 'vuetify';
import { VBtn } from 'vuetify/components/VBtn';
import { VCard } from 'vuetify/components/VCard';
import { api, errorText } from '../api';
import MediaCards from './MediaCards.vue';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
  type ExplorePage,
} from '../features/explore-feed';
import {
  ensureRecommendShelf,
  recommendBrowseRoute,
  recommendPath,
  type RecommendSource,
} from '../features/recommend';

const props = defineProps<{ source: RecommendSource; active: boolean }>();
const display = useDisplay();
const feed = reactive(createExploreFeed());
const container = ref<HTMLElement>(),
  viewport = ref<HTMLElement>();
const scrollLeft = ref(0),
  viewportWidth = ref(0),
  scrolling = ref(false);
const itemWidth = 144,
  itemGap = 16,
  itemStep = itemWidth + itemGap,
  overscan = 4;
const start = computed(() => Math.max(0, Math.floor(scrollLeft.value / itemStep) - overscan));
const end = computed(() =>
  Math.min(
    feed.results.length,
    Math.ceil((scrollLeft.value + viewportWidth.value) / itemStep) + overscan,
  ),
);
const items = computed(() => feed.results.slice(start.value, end.value));
const totalWidth = computed(() => Math.max(0, feed.results.length * itemStep - itemGap));
const leadingWidth = computed(() => start.value * itemStep);
const trailingWidth = computed(() =>
  Math.max(0, totalWidth.value - leadingWidth.value - (items.value.length * itemStep - itemGap)),
);
const canLeft = computed(() => scrollLeft.value > 1);
const canRight = computed(() => scrollLeft.value < totalWidth.value - viewportWidth.value + 24 - 2);
let intersection: IntersectionObserver | undefined;
let resize: ResizeObserver | undefined;
let scrollTimer: ReturnType<typeof setTimeout> | undefined;

function request(page: number) {
  return api<ExplorePage>(recommendPath(props.source.id, page));
}
async function load() {
  await ensureRecommendShelf(feed, props.active, request, errorText);
  await nextTick();
  syncLayout();
}
async function retry() {
  if (!props.active) return;
  await loadExplorePage(feed, request, errorText);
  await nextTick();
  syncLayout();
}
function syncLayout() {
  if (!props.active || !viewport.value) return;
  viewportWidth.value = viewport.value.clientWidth || window.innerWidth;
  scrollLeft.value = viewport.value.scrollLeft;
}
function onScroll() {
  if (!props.active) return;
  syncLayout();
  scrolling.value = true;
  if (scrollTimer) clearTimeout(scrollTimer);
  scrollTimer = setTimeout(() => {
    scrolling.value = false;
  }, 1500);
}
function slide(next: boolean) {
  const element = viewport.value;
  if (!element) return;
  const count = Math.max(1, Math.trunc(element.clientWidth / itemStep));
  const current =
    element.scrollLeft === 0 ? 0 : Math.trunc((element.scrollLeft + itemStep / 2) / itemStep);
  element.scrollTo({
    left: next
      ? Math.min((current + count) * itemStep, element.scrollWidth - element.clientWidth)
      : Math.max((current - count) * itemStep, 0),
    top: 0,
    behavior: 'smooth',
  });
}
async function observe() {
  intersection?.disconnect();
  await nextTick();
  if (!props.active || !container.value) return;
  if (viewport.value) viewport.value.scrollLeft = scrollLeft.value;
  syncLayout();
  if (feed.loaded || feed.error) return;
  intersection = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting) && props.active) {
        intersection?.disconnect();
        void load();
      }
    },
    { rootMargin: '300px' },
  );
  intersection.observe(container.value);
}
watch(
  () => props.active,
  () => {
    void observe();
  },
);
onMounted(() => {
  resize = new ResizeObserver(syncLayout);
  if (viewport.value) resize.observe(viewport.value);
  void observe();
});
onUnmounted(() => {
  intersection?.disconnect();
  resize?.disconnect();
  if (scrollTimer) clearTimeout(scrollTimer);
  resetExploreFeed(feed);
});
</script>

<template>
  <section
    ref="container"
    class="recommend-shelf"
    :class="{ 'is-scrolling': scrolling }"
    :aria-label="source.title"
  >
    <div class="slider-header">
      <div class="title-section">
        <span class="title-badge"></span>
        <h3 class="title-text">{{ source.title }}</h3>
      </div>
      <a :href="'#' + recommendBrowseRoute(source)" class="view-all-button"
        ><span>更多</span
        ><svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z" /></svg
      ></a>
    </div>
    <div class="slider-content-wrapper">
      <div
        ref="viewport"
        class="slider-content"
        tabindex="0"
        :aria-label="source.title + '横向列表'"
        @scroll="onScroll"
      >
        <div
          v-if="!feed.loaded && !feed.error"
          class="loading-track"
          aria-label="正在加载"
          role="status"
        >
          <VCard v-for="index in 10" :key="index" class="poster-placeholder" />
        </div>
        <div v-else-if="feed.error" class="shelf-message" role="alert">
          <span>{{ feed.error }}</span
          ><VBtn variant="text" size="small" :loading="feed.loading" @click="retry">重试</VBtn>
        </div>
        <div
          v-else-if="feed.results.length"
          class="virtual-track"
          :style="{ width: totalWidth + 'px' }"
        >
          <div
            v-if="leadingWidth"
            class="virtual-spacer"
            :style="{ width: leadingWidth + 'px' }"
          ></div>
          <MediaCards :items="items" quick-actions original-actions />
          <div
            v-if="trailingWidth"
            class="virtual-spacer"
            :style="{ width: trailingWidth + 'px' }"
          ></div>
        </div>
        <div v-else class="shelf-message empty">没有找到数据</div>
      </div>
      <VBtn
        v-show="canLeft && !display.mobile.value && feed.results.length"
        class="nav-button nav-button-left"
        variant="text"
        icon
        color="secondary"
        aria-label="向左翻动"
        @click.stop="slide(false)"
        ><svg width="24" height="24" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M15.41,16.58L10.83,12L15.41,7.41L14,6L8,12L14,18L15.41,16.58Z" /></svg
      ></VBtn>
      <VBtn
        v-show="canRight && !display.mobile.value && feed.results.length"
        class="nav-button nav-button-right"
        variant="text"
        icon
        color="secondary"
        aria-label="向右翻动"
        @click.stop="slide(true)"
        ><svg width="24" height="24" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z" /></svg
      ></VBtn>
    </div>
  </section>
</template>

<style scoped>
.recommend-shelf {
  position: relative;
  isolation: isolate;
  margin-block-end: 8px;
  --slider-shadow-bleed-start: 28px;
  --slider-shadow-bleed-end: 56px;
}
.slider-header {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-block-end: 12px;
  padding: 0 8px;
}
.title-section {
  display: flex;
  align-items: center;
  flex-grow: 1;
  min-width: 0;
}
.title-badge {
  flex: none;
  border-radius: 2px;
  background: rgb(var(--v-theme-primary));
  height: 16px;
  width: 3px;
  margin-inline-end: 8px;
}
.title-text {
  padding: 0;
  margin: 0;
  color: rgba(var(--v-theme-on-background), 0.95);
  font-size: 19px;
  font-weight: 600;
  line-height: 1.6;
}
.view-all-button {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  border-radius: 8px;
  background: transparent;
  color: rgb(var(--v-theme-primary));
  font-size: 0.85rem;
  font-weight: 500;
  padding: 5px 12px;
  text-decoration: none;
  transition: all 0.25s ease;
}
.view-all-button svg {
  fill: currentColor;
  margin-inline-start: 2px;
  transition: transform 0.3s ease;
}
.view-all-button span {
  margin-inline-end: 4px;
}
.view-all-button:hover {
  background: rgba(var(--v-theme-primary), 0.08);
  transform: translateY(-1px);
}
.view-all-button:hover svg {
  transform: translateX(3px);
}
.slider-content-wrapper {
  position: relative;
  z-index: 1;
  width: 100%;
}
.slider-content {
  margin-block: calc(var(--slider-shadow-bleed-start) * -1)
    calc(var(--slider-shadow-bleed-end) * -1);
  overflow: auto hidden;
  overscroll-behavior-x: contain;
  padding-block: var(--slider-shadow-bleed-start) var(--slider-shadow-bleed-end);
  padding-inline: 12px;
  scroll-behavior: smooth;
  scrollbar-width: none;
}
.slider-content::-webkit-scrollbar {
  display: none;
}
.virtual-track,
.loading-track {
  display: flex;
  width: max-content;
  padding-bottom: 12px;
}
.loading-track {
  min-width: 100%;
  gap: 16px;
}
.virtual-spacer,
.poster-placeholder {
  flex: none;
}
.poster-placeholder {
  width: 144px;
  height: 216px;
  border: 0;
  border-radius: var(--radius);
}
.virtual-track :deep(.media-grid) {
  flex: none;
  display: flex;
  gap: 16px;
}
.virtual-track :deep(.media-card) {
  flex: none;
  width: 144px;
  height: 216px;
  border-radius: 12px;
}
.virtual-track :deep(.poster-action) {
  border-radius: 12px;
}
.shelf-message {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  min-height: 228px;
  color: var(--danger);
  padding: 16px;
}
.shelf-message.empty {
  color: var(--muted);
}
.nav-button {
  position: absolute;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border-radius: 50%;
  backdrop-filter: blur(8px);
  background: rgba(var(--v-theme-background), 0.3);
  width: 36px;
  height: 36px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
  cursor: pointer;
  top: 50%;
  opacity: 0;
  pointer-events: none;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
  transform: translateY(-50%);
  transition:
    opacity 0.3s ease,
    transform 0.3s ease;
}
.nav-button svg {
  width: 22px;
  height: 22px;
  fill: currentColor;
  opacity: 0.7;
}
.nav-button:hover {
  color: rgb(var(--v-theme-primary));
  transform: translateY(-50%) scale(1.05);
}
.nav-button:hover svg {
  opacity: 1;
}
.nav-button-left {
  left: 8px;
}
.nav-button-right {
  right: 8px;
}
.recommend-shelf.is-scrolling .nav-button,
.nav-button:focus-visible {
  opacity: 1;
  pointer-events: auto;
}
@media (hover: hover) {
  .recommend-shelf:hover .nav-button {
    opacity: 1;
    pointer-events: auto;
  }
}
</style>
