<script setup lang="ts">
import { computed, shallowRef, markRaw, onMounted, onUnmounted, type Component } from 'vue';
import { VCard, VCardItem, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText } from '../api';
import { pluginPageIssues, type PluginCard } from '../features/plugin-forms';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';
import type { DashboardItem } from '../features/plugin-dashboard';
import PluginFormRenderer from './PluginFormRenderer.vue';
const props = defineProps<{ item: DashboardItem }>();
const emit = defineEmits<{ loaded: [] }>();
const remote = shallowRef<Component>(),
  remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>(),
  error = shallowRef('');
const controller = new AbortController();
let active = true;
const issues = computed(() => pluginPageIssues(props.item.elements || []));
onMounted(async () => {
  try {
    if (props.item.render_mode === 'vue') {
      const cards = await api<PluginCard[]>('/plugins');
      const card = cards.find((x) => x.instance_id === props.item.id);
      if (!card) throw Error('插件未安装');
      const component = await loadPluginRemote(card.plugin_id, card.instance_id, 'Dashboard');
      if (active) {
        remote.value = markRaw(component);
        remoteAPI.value = createPluginRemoteAPI(
          card.plugin_id,
          card.instance_id,
          controller.signal,
        );
      }
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) emit('loaded');
  }
});
onUnmounted(() => {
  active = false;
  controller.abort();
});
</script>
<template>
  <component v-if="remote" :is="remote" :config="item" :allow-refresh="true" :api="remoteAPI" />
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
        ><PluginFormRenderer v-for="(node, i) in item.elements" :key="i" :node="node" :model="{}"
      /></template>
    </VCardText>
  </VCard>
</template>
