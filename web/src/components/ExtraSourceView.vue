<script setup lang="ts">
import { defineAsyncComponent, nextTick, reactive, watch } from 'vue';
import { VInfiniteScroll } from 'vuetify/components/VInfiniteScroll';
import { VBtn } from 'vuetify/components/VBtn';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { errorText } from '../api';
const PluginFormRenderer = defineAsyncComponent(() => import('./PluginFormRenderer.vue'));
import MediaCards from './MediaCards.vue';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
  type ExploreLoadStatus,
} from '../features/explore-feed';
import {
  applyExtraSourceDependencies,
  requestPluginCatalogPage,
  type ExtraDiscoverSource,
} from '../features/plugin-catalog-sources';

const props = defineProps<{ source: ExtraDiscoverSource; active: boolean }>();
const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
const defaults = copy(props.source.filter_params || {});
const filters = reactive(props.source.filter_params || {});
let previous = copy(defaults);
const feed = reactive(createExploreFeed());
watch(filters, (current) => {
  applyExtraSourceDependencies(current, previous, defaults, props.source.depends);
  previous = copy(current);
  resetExploreFeed(feed);
});
async function load({ done }: { done: (status: ExploreLoadStatus) => void }) {
  if (!props.source.api_path) return;
  if (!props.active) return done('ok');
  if (feed.loading) return done('ok');
  if (feed.error) return done('error');
  let status: ExploreLoadStatus;
  do {
    status = await loadExplorePage(
      feed,
      (page) => requestPluginCatalogPage(props.source.api_path, page, filters),
      errorText,
    );
    await nextTick();
  } while (props.active && status === 'ok' && document.body.scrollHeight - window.innerHeight <= 2);
  done(status);
}
function retry(action: { onClick?: () => void }) {
  feed.error = '';
  action.onClick?.();
}
</script>
<template>
  <div class="px-3 plugin-surface">
    <PluginFormRenderer
      v-for="(node, index) in source.filter_ui"
      :key="index"
      :node="node"
      :model="filters"
      @error="feed.error = $event"
    />
  </div>
  <div
    v-if="active && !feed.loaded && !feed.error"
    class="extra-source-loading"
    role="status"
    aria-label="正在加载"
  >
    <VProgressCircular indeterminate color="primary" />
  </div>
  <VInfiniteScroll
    v-if="active"
    :key="feed.generation"
    :items="feed.results"
    class="explore-results"
    mode="intersect"
    side="end"
    @load="load"
  >
    <template #loading />
    <template #empty />
    <template #error="{ props: retryProps }"
      ><div class="explore-error" role="alert">
        {{ feed.error }}<VBtn variant="text" size="small" @click="retry(retryProps)">重试</VBtn>
      </div></template
    >
    <MediaCards v-if="feed.results.length" :items="feed.results" quick-actions original-actions />
    <div v-else-if="feed.loaded && !feed.hasMore" class="empty">没有找到数据</div>
  </VInfiniteScroll>
</template>
<style scoped>
.extra-source-loading {
  display: flex;
  justify-content: center;
  padding-top: 48px;
}
</style>
