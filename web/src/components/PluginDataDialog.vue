<script setup lang="ts">
import {
  ref,
  useId,
  shallowRef,
  markRaw,
  computed,
  onMounted,
  onUnmounted,
  type Component,
} from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { api, errorText } from '../api';
import { pluginPageIssues, type PluginCard, type PluginFormNode } from '../features/plugin-forms';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
import PluginFormRenderer from './PluginFormRenderer.vue';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';

const props = withDefaults(defineProps<{ plugin: PluginCard; showSwitch?: boolean }>(), {
  showSwitch: true,
});
const emit = defineEmits<{ close: []; switch: []; changed: [] }>();
const display = useDisplay();
const pageScope = 'plugin-page-' + useId().replace(/[^A-Za-z0-9_-]/g, '_');
const renderMode = ref<'vuetify' | 'vue'>('vuetify');
const remote = shallowRef<Component>();
const controller = new AbortController();
const remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>();
const loading = ref(true),
  acting = ref(false),
  actionError = ref(''),
  error = ref(''),
  nodes = ref<PluginFormNode[]>([]);
const issues = computed(() => pluginPageIssues(nodes.value));
let active = true,
  fetching = false;
const pageAPI = createPluginRemoteAPI(
  props.plugin.plugin_id,
  props.plugin.instance_id,
  controller.signal,
  () => emit('changed'),
);
async function pageAction(event: NonNullable<PluginFormNode['events']>[string]) {
  if (acting.value) return;
  acting.value = true;
  actionError.value = '';
  try {
    // Original PageRender sends GET params as query and all other methods as POST.
    if (event.method.toUpperCase() === 'GET')
      await pageAPI.get(event.api, { params: event.params || {} });
    else await pageAPI.post(event.api, event.params || {});
    if (active) await load();
  } catch (error) {
    if (active) actionError.value = errorText(error);
  } finally {
    if (active) acting.value = false;
  }
}
async function load() {
  if (fetching) return;
  fetching = true;
  loading.value = true;
  try {
    const result = await api('/plugins/' + encodeURIComponent(props.plugin.instance_id) + '/page');
    if (!active) return;
    if (!['vuetify', 'vue'].includes(result?.render_mode)) throw new Error('插件页面返回格式无效');
    renderMode.value = result.render_mode;
    if (renderMode.value === 'vue') {
      const { component, descriptor } = await loadPluginRemote(props.plugin.instance_id, 'Page', {
        signal: controller.signal,
        version: props.plugin.version,
      });
      if (!active) return;
      remoteAPI.value = createPluginRemoteAPI(
        descriptor.plugin_id,
        descriptor.instance_id,
        controller.signal,
        () => emit('changed'),
      );
      remote.value = markRaw(component);
    } else {
      if (!Array.isArray(result.page)) throw new Error('插件页面返回格式无效');
      nodes.value = result.page;
    }
    error.value = '';
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    fetching = false;
    if (active) loading.value = false;
  }
}
onMounted(load);
onUnmounted(() => {
  active = false;
  controller.abort();
});
</script>
<template>
  <VDialog
    :model-value="true"
    scrollable
    max-width="80rem"
    :fullscreen="!display.mdAndUp.value"
    content-class="plugin-dialog-surface"
    @update:model-value="emit('close')"
  >
    <VCard v-if="renderMode === 'vuetify'" :title="plugin.name" class="plugin-data-card">
      <PluginDialogCloseBtn aria-label="关闭插件详情" @click="emit('close')" />
      <VCardText class="plugin-data-content" :data-plugin-page-scope="pageScope">
        <p v-if="loading" role="status">正在加载…</p>
        <VAlert v-if="actionError" type="error" variant="tonal">{{ actionError }}</VAlert>
        <VAlert v-if="error" type="error" variant="tonal"
          >{{ error }}<VBtn variant="text" @click="load">重试</VBtn></VAlert
        >
        <VAlert v-for="issue in issues" :key="issue" type="error" variant="tonal">{{
          issue
        }}</VAlert>
        <template v-if="!loading && !error && !issues.length"
          ><PluginFormRenderer
            v-for="(node, index) in nodes"
            :key="index"
            :node="node"
            :model="{}"
            :page-scope="pageScope"
            view="page"
            :action="pageAction"
          />
          <p v-if="!nodes.length">此插件没有详情页面</p></template
        >
      </VCardText>
      <VBtn
        v-if="showSwitch"
        class="plugin-data-settings"
        color="primary"
        icon="mdi-cog"
        size="x-large"
        aria-label="插件设置"
        @click="emit('switch')"
      />
    </VCard>
    <VCard v-else class="plugin-data-card">
      <PluginDialogCloseBtn
        v-if="!remote || error"
        aria-label="关闭插件详情"
        @click="emit('close')"
      />
      <VCardText class="pa-0">
        <p v-if="loading" class="pa-4" role="status">正在加载…</p>
        <VAlert v-if="error" type="error" variant="tonal" class="ma-4"
          >{{ error }}<VBtn variant="text" @click="load">重试</VBtn></VAlert
        >
        <component
          v-if="remote && !loading && !error"
          :is="remote"
          :api="remoteAPI"
          :show_switch="showSwitch"
          @switch="emit('switch')"
          @close="emit('close')"
        />
      </VCardText>
    </VCard>
  </VDialog>
  <VDialog :model-value="acting" :scrim="false" width="25rem" content-class="plugin-dialog-surface">
    <VCard elevation="3" color="primary">
      <VCardText role="status" class="text-center">
        正在处理...
        <VProgressLinear indeterminate color="white" class="mb-0 mt-1" />
      </VCardText>
    </VCard>
  </VDialog>
</template>
<style scoped>
.plugin-data-content {
  min-height: 160px;
}
.plugin-data-settings {
  /* The host has no Vuetify app layout; retain VFab's bottom-end position
     without requiring its global layout provider. */
  position: fixed;
  bottom: 12px;
  right: 12px;
  z-index: 2;
}
.plugin-data-content :deep(.v-table) {
  background: transparent;
}
.plugin-data-content :deep(.whitespace-nowrap) {
  white-space: nowrap;
}
.plugin-data-content :deep(th) {
  text-align: left;
}
.plugin-data-content :deep(.text-center) {
  text-align: center;
}
.plugin-data-content :deep(.d-flex) {
  display: flex;
}
.plugin-data-content :deep(.align-center) {
  align-items: center;
}
.plugin-data-content :deep(.flex-wrap) {
  flex-wrap: wrap;
}
.plugin-data-content :deep(.me-3) {
  margin-inline-end: 12px;
}
.plugin-data-content :deep(.text-caption) {
  font-size: 12px;
  line-height: 20px;
}
.plugin-data-content :deep(.text-h6) {
  font-size: 20px;
  line-height: 28px;
  font-weight: 500;
}
.plugin-data-content :deep(.text-break) {
  overflow-wrap: anywhere;
}
.plugin-data-content :deep(.text-success) {
  color: rgb(var(--v-theme-success));
}
.plugin-data-content :deep(.text-error) {
  color: rgb(var(--v-theme-error));
}
.plugin-data-content :deep(.break-keep) {
  word-break: keep-all;
}
.plugin-data-content :deep(.v-avatar .v-img) {
  width: 40px;
  height: 40px;
}
</style>
