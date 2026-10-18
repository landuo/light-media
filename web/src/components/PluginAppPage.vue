<script setup lang="ts">
import { ref, shallowRef, markRaw, onMounted, onUnmounted, type Component } from 'vue';
import { VAlert } from 'vuetify/components/VAlert';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { api, errorText } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';

const props = defineProps<{ route: string }>();
const remote = shallowRef<Component>();
const remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>();
const error = ref('');
const controller = new AbortController();
let active = true;

onMounted(async () => {
  try {
    const parts = props.route.split('/');
    if (parts.length !== 3 || parts[0] !== 'plugin-app' || parts[2] !== 'main')
      throw new Error('插件全页入口无效');
    const instance = decodeURIComponent(parts[1]!);
    const cards = await api<PluginCard[]>('/plugins');
    const plugin = cards.find((card) => card.instance_id === instance && card.installed);
    if (!plugin || plugin.plugin_id !== 'SeedRescue') throw new Error('插件未安装或不提供全页入口');
    const component = await loadPluginRemote(plugin.plugin_id, instance, 'AppPage');
    if (!active) return;
    remoteAPI.value = createPluginRemoteAPI(plugin.plugin_id, instance, controller.signal);
    remote.value = markRaw(component);
  } catch (e) {
    if (active) error.value = errorText(e);
  }
});
onUnmounted(() => {
  active = false;
  controller.abort();
});
</script>
<template>
  <VThemeProvider theme="light" with-background>
    <section class="plugin-surface plugin-dialog-surface plugin-app-page">
      <VAlert v-if="error" type="error" class="ma-4" title="组件加载错误">{{ error }}</VAlert>
      <VSkeletonLoader v-else-if="!remote" class="ma-4" type="article, article, article" />
      <component v-else :is="remote" :api="remoteAPI" nav-key="main" plugin-id="SeedRescue" />
    </section>
  </VThemeProvider>
</template>
