<script setup lang="ts">
import { ref, shallowRef, markRaw, computed, onMounted, onUnmounted, type Component } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText } from '../api';
import { pluginPageIssues, type PluginCard, type PluginFormNode } from '../features/plugin-forms';
import AppIcon from './AppIcon.vue';
import PluginFormRenderer from './PluginFormRenderer.vue';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: []; switch: []; changed: [] }>();
const display = useDisplay();
const renderMode = ref<'vuetify' | 'vue'>('vuetify');
const remote = shallowRef<Component>();
const controller = new AbortController();
const remoteAPI = createPluginRemoteAPI(
  props.plugin.plugin_id,
  props.plugin.instance_id,
  controller.signal,
  props.plugin.plugin_id === 'BrushFlowLanduo' ? () => emit('changed') : undefined,
);
const loading = ref(true),
  error = ref(''),
  nodes = ref<PluginFormNode[]>([]);
const issues = computed(() => pluginPageIssues(nodes.value));
let active = true,
  fetching = false;
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
      const component = await loadPluginRemote(
        props.plugin.plugin_id,
        props.plugin.instance_id,
        'Page',
      );
      if (!active) return;
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
    <VCard v-if="renderMode === 'vuetify'" class="plugin-data-card">
      <VCardTitle class="plugin-data-title"
        >{{ plugin.name
        }}<VBtn
          variant="text"
          color="secondary"
          icon
          aria-label="关闭插件详情"
          @click="emit('close')"
          ><AppIcon name="close" /></VBtn
      ></VCardTitle>
      <VCardText class="plugin-data-content">
        <p v-if="loading" role="status">正在加载…</p>
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
          />
          <p v-if="!nodes.length">此插件没有详情页面</p></template
        >
      </VCardText>
      <VBtn
        class="plugin-data-settings"
        color="primary"
        icon
        size="x-large"
        aria-label="插件设置"
        @click="emit('switch')"
        ><AppIcon name="settings"
      /></VBtn>
    </VCard>
    <VCard v-else class="plugin-data-card">
      <VCardText class="pa-0">
        <p v-if="loading" class="pa-4" role="status">正在加载…</p>
        <VAlert v-if="error" type="error" variant="tonal" class="ma-4"
          >{{ error }}<VBtn variant="text" @click="load">重试</VBtn></VAlert
        >
        <component
          v-if="remote && !loading && !error"
          :is="remote"
          :api="remoteAPI"
          :show_switch="true"
          @switch="emit('switch')"
          @close="emit('close')"
        />
      </VCardText>
    </VCard>
  </VDialog>
</template>
<style scoped>
.plugin-data-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px;
  font-size: 20px;
}
.plugin-data-content {
  padding: 16px 24px 88px;
  min-height: 160px;
}
.plugin-data-settings {
  position: absolute;
  bottom: 16px;
  left: 50%;
  transform: translateX(-50%);
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
