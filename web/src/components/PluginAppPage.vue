<script setup lang="ts">
import { ref, shallowRef, markRaw, watch, onUnmounted, type Component } from 'vue';
import { VAlert } from 'vuetify/components/VAlert';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { errorText } from '../api';
import { loadPluginAppRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';
import { parsePluginNavigationRoute } from '../features/plugin-navigation';

const props = defineProps<{ route: string }>();
const emit = defineEmits<{ changed: [] }>();
const pluginID = ref('');
const navKey = ref('');
const remote = shallowRef<Component>();
const remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>();
const error = ref('');
let controller = new AbortController(),
  active = true,
  revision = 0;

async function load() {
  controller.abort();
  controller = new AbortController();
  const current = ++revision;
  remote.value = undefined;
  error.value = '';
  try {
    const { instance, navKey: key } = parsePluginNavigationRoute(props.route);
    // The fixed route loads an existing remote directly. Sidebar declarations
    // determine menu placement, not whether an AppPage can be opened by URL.
    const { component, descriptor } = await loadPluginAppRemote(instance, key, {
      signal: controller.signal,
    });
    if (!active || current !== revision) return;
    pluginID.value = descriptor.instance_id;
    navKey.value = key;
    remoteAPI.value = createPluginRemoteAPI(
      descriptor.plugin_id,
      descriptor.instance_id,
      controller.signal,
      () => emit('changed'),
    );
    remote.value = markRaw(component);
  } catch (e) {
    if (active && current === revision) error.value = errorText(e);
  }
}
watch(() => props.route, load, { immediate: true });
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
