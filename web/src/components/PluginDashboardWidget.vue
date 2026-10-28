<script setup lang="ts">
import { computed, shallowRef, markRaw, watch, onUnmounted, type Component } from 'vue';
import { VCard, VCardItem, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VAlert } from 'vuetify/components/VAlert';
import { errorText } from '../api';
import { pluginPageIssues } from '../features/plugin-forms';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';
import type { DashboardItem } from '../features/plugin-dashboard';
import PluginFormRenderer from './PluginFormRenderer.vue';
const props = defineProps<{ item: DashboardItem; allowRefresh?: boolean }>();
const emit = defineEmits<{ loaded: [] }>();
const remote = shallowRef<Component>(),
  remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>(),
  error = shallowRef('');
let controller = new AbortController(),
  active = true,
  revision = 0;
const issues = computed(() => pluginPageIssues(props.item.elements || []));
async function load() {
  controller.abort();
  controller = new AbortController();
  const current = ++revision;
  remote.value = undefined;
  remoteAPI.value = undefined;
  error.value = '';
  try {
    if (props.item.render_mode && !['vue', 'vuetify'].includes(props.item.render_mode))
      throw new Error('插件仪表盘格式不受支持');
    if (props.item.render_mode === 'vue') {
      const { component, descriptor } = await loadPluginRemote(props.item.id, 'Dashboard', {
        signal: controller.signal,
      });
      if (active && current === revision) {
        remote.value = markRaw(component);
        remoteAPI.value = createPluginRemoteAPI(
          descriptor.plugin_id,
          descriptor.instance_id,
          controller.signal,
        );
      }
    }
  } catch (e) {
    if (active && current === revision) error.value = errorText(e);
  } finally {
    if (active && current === revision) emit('loaded');
  }
}
watch(
  () => [props.item.id, props.item.key, props.item.render_mode],
  (current, previous) => {
    // The fixed DashboardElement caches a component for the current node.
    // Periodic data replacement must preserve its state and live API client.
    if (previous && current.every((value, index) => value === previous[index])) return;
    void load();
  },
  { immediate: true },
);
onUnmounted(() => {
  active = false;
  controller.abort();
});
</script>
<template>
  <div v-if="remote" class="dashboard-plugin-vue-renderer">
    <component :is="remote" :config="item" :allow-refresh="allowRefresh ?? true" :api="remoteAPI" />
  </div>
  <VCard v-else class="plugin-dashboard-widget">
    <VCardItem v-if="item.attrs?.border !== false"
      ><VCardTitle>{{ item.attrs?.title ?? item.name }}</VCardTitle
      ><VCardSubtitle v-if="item.attrs?.subtitle">{{
        item.attrs.subtitle
      }}</VCardSubtitle></VCardItem
    >
    <VCardText :class="{ 'pa-0': item.attrs?.border === false }" data-layout-size-source>
      <VAlert v-if="error" type="error">{{ error }}</VAlert>
      <VAlert v-else-if="item.render_mode === 'vue'" type="info">正在加载…</VAlert>
      <VAlert v-for="issue in issues" :key="issue" type="error">{{ issue }}</VAlert>
      <template v-if="!issues.length && !error && item.render_mode !== 'vue'"
        ><PluginFormRenderer
          v-for="(node, i) in item.elements"
          :key="i"
          :node="node"
          :model="{}"
          view="dashboard"
      /></template>
    </VCardText>
  </VCard>
</template>

<style scoped>
.dashboard-plugin-vue-renderer {
  display: flex;
  flex-direction: column;
  block-size: 100%;
  inline-size: 100%;
  min-block-size: 0;
}
</style>
