<script setup lang="ts">
import { ref, shallowRef, markRaw, onMounted, onUnmounted, type Component } from 'vue';
import { VAlert } from 'vuetify/components/VAlert';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { api, errorText } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';
import { parsePluginNavigationRoute, type PluginNavigationItem } from '../features/plugin-navigation';

const props = defineProps<{ route: string }>();
const emit = defineEmits<{ changed: [] }>();
const pluginID = ref('');
const navKey = ref('');
const remote = shallowRef<Component>();
const remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>();
const error = ref('');
const controller = new AbortController();
let active = true;

onMounted(async () => {
  try {
    const { instance, navKey: key } = parsePluginNavigationRoute(props.route);
    const [cards, navigation] = await Promise.all([
      api<PluginCard[]>('/plugins'), api<PluginNavigationItem[]>('/plugins/navigation'),
    ]);
    if (!active) return;
    const plugin = cards.find((card) => card.instance_id === instance && card.installed);
    if (!plugin || !navigation.some((item) => item.instance_id === instance && item.nav_key === key))
      throw new Error('插件未安装或未声明此全页入口');
    const { component, descriptor } = await loadPluginRemote(instance, 'AppPage', {
      signal: controller.signal, version: plugin.version,
    });
    if (!active) return;
    pluginID.value = descriptor.plugin_id;
    navKey.value = key;
    remoteAPI.value = createPluginRemoteAPI(
      descriptor.plugin_id, descriptor.instance_id, controller.signal, () => emit('changed'),
    );
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
      <component v-else :is="remote" :api="remoteAPI" :nav-key="navKey" :plugin-id="pluginID" />
    </section>
  </VThemeProvider>
</template>
