<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
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
import { VTab, VTabs } from 'vuetify/components/VTabs';
import { VWindow, VWindowItem } from 'vuetify/components/VWindow';
import { VInfiniteScroll } from 'vuetify/components/VInfiniteScroll';
import { VBtn } from 'vuetify/components/VBtn';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VDialog } from 'vuetify/components/VDialog';
import { VIcon } from 'vuetify/components/VIcon';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { VDivider } from 'vuetify/components/VDivider';
import Draggable from 'vuedraggable';
import { api, errorText, toast } from '../api';
import MediaCards from './MediaCards.vue';
import ExploreFilters from './ExploreFilters.vue';
const ExtraSourceView = defineAsyncComponent(() => import('./ExtraSourceView.vue'));
import {
  appendExtraDiscoverSources,
  type ExtraDiscoverSource,
} from '../features/plugin-catalog-sources';
import {
  createExploreFilters,
  exploreQuery,
  exploreSources,
  normalizeExploreOrder,
  type ExploreSource,
} from '../features/explore-filters';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
  type ExploreLoadStatus,
  type ExplorePage,
} from '../features/explore-feed';

const props = withDefaults(defineProps<{ active?: boolean }>(), {
  active: true,
});
const orderKey = 'LM_EXPLORE_TAB_ORDER';
function savedOrder() {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(orderKey) || '[]');
    return normalizeExploreOrder(Array.isArray(value) ? value : []);
  } catch {
    return normalizeExploreOrder();
  }
}
const order = ref<string[]>(savedOrder());
const extraSources = ref<ExtraDiscoverSource[]>([]);
const extraTabs = ref<ExtraDiscoverSource[]>([]);
const activeTab = ref<string>(order.value[0]!);
const availableTabs = computed(() => [
  ...exploreSources,
  ...extraTabs.value.map((source) => ({
    id: source.mediaid_prefix,
    name: source.name,
    color: 'primary',
  })),
]);
const tabs = computed(() =>
  order.value.map((id) => availableTabs.value.find((source) => source.id === id)!).filter(Boolean),
);
const filters = reactive({
  themoviedb: createExploreFilters('themoviedb'),
  douban: createExploreFilters('douban'),
  bangumi: createExploreFilters('bangumi'),
  anilist: createExploreFilters('anilist'),
});
const feeds = reactive({
  themoviedb: createExploreFeed(),
  douban: createExploreFeed(),
  bangumi: createExploreFeed(),
  anilist: createExploreFeed(),
});
const activated = ref(true),
  mounted = ref(false),
  headerReady = ref(false),
  scrollY = ref(0);
const visible = computed(() => props.active && activated.value);
const display = useDisplay();
const selectedTab = ref(false),
  orderTouched = ref(false),
  savingOrder = ref(false);
let wasDeactivated = false,
  lastScroll = 0;
function setExtraSources(rows: ExtraDiscoverSource[]) {
  // Fixed discover.vue keeps appended navigation entries, while its windows
  // render the latest raw extra-source array, including duplicate prefixes.
  extraSources.value = rows;
  extraTabs.value = extraTabs.value.concat(
    appendExtraDiscoverSources(
      [
        ...exploreSources.map((source) => source.id),
        ...extraTabs.value.map((source) => source.mediaid_prefix),
      ],
      rows,
    ),
  );
}
async function loadOrder() {
  try {
    const [preferences, catalog] = await Promise.all([
      api<{ tab_order: string[] }>('/catalog/explore/preferences'),
      api<{ extra_sources: ExtraDiscoverSource[] }>('/catalog/explore/sources').catch((error) => {
        toast('插件探索来源读取失败：' + errorText(error), true);
        return { extra_sources: [] };
      }),
    ]);
    setExtraSources(catalog.extra_sources || []);
    const available = availableTabs.value.map((source) => source.id);
    let preferred: string[] = order.value;
    if (!orderTouched.value && !selectedTab.value) {
      try {
        const saved: unknown = JSON.parse(localStorage.getItem(orderKey) || '[]');
        if (Array.isArray(saved)) preferred = saved.filter((id) => typeof id === 'string');
      } catch {
        /* use current order */
      }
      if (preferences.tab_order?.length) preferred = preferences.tab_order;
    }
    order.value = [...new Set([...preferred.filter((id) => available.includes(id)), ...available])];
    if (!orderTouched.value && !selectedTab.value) activeTab.value = order.value[0]!;
    rememberOrder();
  } catch (error) {
    toast('标签顺序读取失败：' + errorText(error), true);
  }
}
async function loadExtraSources() {
  try {
    const catalog = await api<{ extra_sources: ExtraDiscoverSource[] }>('/catalog/explore/sources');
    setExtraSources(catalog.extra_sources || []);
    const available = availableTabs.value.map((source) => source.id);
    order.value = [
      ...new Set([...order.value.filter((id) => available.includes(id)), ...available]),
    ];
    if (!order.value.includes(activeTab.value)) activeTab.value = order.value[0]!;
  } catch (error) {
    toast('插件探索来源读取失败：' + errorText(error), true);
  }
}
function rememberOrder() {
  try {
    localStorage.setItem(orderKey, JSON.stringify(order.value));
  } catch {
    /* Preferences still apply for this session. */
  }
}
for (const source of exploreSources) {
  watch(
    () => exploreQuery(source.id, filters[source.id], 1),
    () => {
      selectedTab.value = true;
      resetExploreFeed(feeds[source.id]);
    },
    { flush: 'sync' },
  );
}
async function load(
  source: ExploreSource,
  { done }: { done: (status: ExploreLoadStatus) => void },
) {
  if (!visible.value || activeTab.value !== source) return done('ok');
  const query = exploreQuery(source, filters[source], feeds[source].page + 1);
  done(
    await loadExplorePage(
      feeds[source],
      () => api<ExplorePage>('/catalog/explore?' + query),
      errorText,
    ),
  );
}
const orderDialog = ref(false),
  draftOrder = ref([...tabs.value]);
function openOrder() {
  orderTouched.value = true;
  draftOrder.value = [...tabs.value];
  orderDialog.value = true;
}
async function saveOrder() {
  if (savingOrder.value) return;
  savingOrder.value = true;
  selectedTab.value = true;
  order.value = draftOrder.value.map((source) => source.id);
  rememberOrder();
  try {
    await api('/catalog/explore/preferences', { tab_order: [...order.value] }, 'PUT');
  } catch (error) {
    toast('标签顺序未保存到服务端：' + errorText(error), true);
  } finally {
    savingOrder.value = false;
    orderDialog.value = false;
  }
}
function onScroll() {
  scrollY.value = window.scrollY;
}
function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' });
}
async function locateHeader() {
  await nextTick();
  headerReady.value = Boolean(document.querySelector('#explore-header'));
}
watch(visible, () => {
  void locateHeader();
  if (!visible.value) orderDialog.value = false;
});
onMounted(() => {
  mounted.value = true;
  void locateHeader();
  void loadOrder();
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });
});
onActivated(() => {
  activated.value = true;
  void loadExtraSources();
  void locateHeader();
  if (wasDeactivated) void nextTick(() => window.scrollTo({ top: lastScroll, behavior: 'auto' }));
});
onDeactivated(() => {
  lastScroll = scrollY.value;
  wasDeactivated = true;
  activated.value = false;
});
onUnmounted(() => {
  window.removeEventListener('scroll', onScroll);
  for (const feed of Object.values(feeds)) resetExploreFeed(feed);
});
</script>

<template>
  <div class="explore-page">
    <Teleport v-if="mounted && visible" to="#explore-header" :disabled="!headerReady">
      <div class="explore-header">
        <VTabs
          v-model="activeTab"
          color="primary"
          height="48"
          @update:model-value="selectedTab = true"
        >
          <VTab v-for="source in tabs" :key="source.id" :value="source.id">{{ source.name }}</VTab>
        </VTabs>
        <VBtn
          icon="mdi-order-alphabetical-ascending"
          variant="text"
          color="grey"
          class="explore-sort-button"
          :disabled="savingOrder"
          aria-label="设置标签顺序"
          @click="openOrder"
        />
      </div>
    </Teleport>
    <VWindow v-model="activeTab" :touch="false" class="explore-window">
      <VWindowItem
        v-for="source in exploreSources"
        :key="source.id"
        :value="source.id"
        :transition="false"
        :reverse-transition="false"
      >
        <ExploreFilters v-model="filters[source.id]" :source="source.id" />
        <div
          v-if="
            !feeds[source.id].results.length &&
            !feeds[source.id].error &&
            (!feeds[source.id].loaded || feeds[source.id].hasMore)
          "
          class="explore-loading"
          role="status"
          aria-label="正在加载"
        >
          <VProgressCircular indeterminate color="primary" />
        </div>
        <VInfiniteScroll
          v-if="visible && activeTab === source.id"
          :key="source.id + ':' + feeds[source.id].generation"
          mode="intersect"
          side="end"
          :items="feeds[source.id].results"
          class="explore-results"
          @load="load(source.id, $event)"
        >
          <template #loading />
          <template #empty />
          <template #error="{ props: retryProps }">
            <div class="explore-error" role="alert">
              <span>{{ feeds[source.id].error }}</span>
              <VBtn v-bind="retryProps" variant="text" size="small">重试</VBtn>
            </div>
          </template>
          <MediaCards
            v-if="feeds[source.id].results.length"
            :items="feeds[source.id].results"
            quick-actions
            original-actions
          />
          <div v-else-if="feeds[source.id].loaded && !feeds[source.id].hasMore" class="empty">
            没有找到数据
          </div>
        </VInfiniteScroll>
      </VWindowItem>
      <VWindowItem
        v-for="source in extraSources"
        :key="source.mediaid_prefix"
        :value="source.mediaid_prefix"
        :transition="false"
        :reverse-transition="false"
      >
        <ExtraSourceView
          :source="source"
          :active="visible && activeTab === source.mediaid_prefix"
        />
      </VWindowItem>
    </VWindow>
    <VDialog
      v-model="orderDialog"
      :max-width="display.mdAndUp.value ? '35rem' : undefined"
      :fullscreen="!display.mdAndUp.value"
      scrollable
    >
      <VCard class="explore-order-dialog">
        <VCardTitle class="explore-order-title">
          <span
            ><VIcon
              icon="mdi-order-alphabetical-ascending"
              size="small"
              class="explore-title-icon"
            />设置标签顺序</span
          >
          <VBtn
            icon="mdi-close"
            variant="text"
            color="secondary"
            aria-label="关闭"
            @click="orderDialog = false"
          />
        </VCardTitle>
        <VDivider />
        <VCardText>
          <p class="explore-order-hint">拖动对标签页进行排序</p>
          <Draggable
            v-model="draftOrder"
            item-key="id"
            handle=".explore-drag-handle"
            class="explore-order-grid"
            :animation="200"
            :disabled="savingOrder"
          >
            <template #item="{ element }">
              <VCard
                class="explore-order-item"
                :style="{ borderLeftColor: `rgb(var(--v-theme-${element.color}))` }"
                variant="text"
              >
                <span>{{ element.name }}</span>
                <VIcon icon="mdi-drag" class="explore-drag-handle" aria-label="拖动排序" />
              </VCard>
            </template>
          </Draggable>
        </VCardText>
        <VCardActions class="explore-order-actions"
          ><VBtn
            prepend-icon="mdi-content-save"
            variant="flat"
            :loading="savingOrder"
            @click="saveOrder"
            >保存</VBtn
          ></VCardActions
        >
      </VCard>
    </VDialog>
    <Teleport to="body">
      <button
        v-if="visible && scrollY > 200 && display.smAndUp.value"
        class="explore-scroll-top"
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
        </svg>
      </button>
    </Teleport>
  </div>
</template>

<style scoped>
.explore-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  width: 100%;
}
.explore-header :deep(.v-tabs) {
  flex: 1;
  min-width: 0;
}
.explore-header :deep(.v-tab.v-btn) {
  min-width: 0;
  margin-right: 12px;
  padding: 6px 14px;
  gap: 0;
  font-size: 0.9rem;
  font-weight: 600;
  letter-spacing: normal;
  text-indent: 0;
  text-transform: none;
}
.explore-header :deep(.v-tab__slider) {
  width: 70%;
  height: 3px;
  margin-inline: auto;
  border-radius: 4px;
}
.explore-header :deep(.v-tab .v-btn__overlay) {
  opacity: 0;
}
.explore-header :deep(.v-tab:hover) {
  background: rgba(var(--v-theme-primary), 0.05);
}
.explore-header :deep(.v-slide-group__prev),
.explore-header :deep(.v-slide-group__next) {
  flex-basis: 28px;
  min-width: 28px;
}
.explore-sort-button {
  flex: none;
  width: 44px;
  height: 44px;
  border-radius: 0.95rem;
}
.explore-window {
  overflow: visible;
}
.explore-results {
  overflow: visible;
  padding: 0 8px;
}
.explore-loading {
  display: flex;
  justify-content: center;
  margin-top: 48px;
}
.explore-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--danger);
}
.explore-order-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px;
}
.explore-title-icon {
  margin-right: 8px;
}
.explore-order-hint {
  margin: 0 0 16px;
  color: var(--muted);
  font-size: 14px;
}
.explore-order-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 12px;
}
.explore-order-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-left: 4px solid;
  background: rgba(var(--v-theme-primary), 0.08);
  color: rgba(var(--v-theme-primary), 0.9);
  font-size: 0.9rem;
  font-weight: 500;
}
.explore-drag-handle {
  cursor: move;
  touch-action: none;
  color: var(--muted);
}
.explore-order-actions {
  justify-content: flex-end;
  padding: 16px 24px;
}
.explore-scroll-top {
  position: fixed;
  bottom: max(2rem, var(--app-floating-bottom, 0px));
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
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}
.explore-scroll-top:hover {
  background: rgba(var(--v-theme-background), 0.95);
  color: rgb(var(--v-theme-primary));
  transform: translateY(-4px);
}
@media (max-width: 768px) {
  .explore-sort-button {
    width: 40px;
    height: 40px;
  }
}
@media (max-width: 600px) {
  .explore-order-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
