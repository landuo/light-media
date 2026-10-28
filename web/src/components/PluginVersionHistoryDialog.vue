<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardText, VCardItem } from 'vuetify/components/VCard';
import { VList, VListItem, VListItemTitle } from 'vuetify/components/VList';
import { VDivider } from 'vuetify/components/VDivider';
import { VBtn } from 'vuetify/components/VBtn';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { VAlert } from 'vuetify/components/VAlert';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
import type { PluginCard } from '../features/plugin-forms';
import { api, errorText } from '../api';
import { renderPluginHistory } from '../features/plugin-history-markdown';

const props = defineProps<{ plugin: PluginCard; showUpdateAction?: boolean; updating?: boolean; updateError?: string }>();
const emit = defineEmits<{ close: []; update: [] }>();
const loading = ref(false), error = ref('');
const result = ref<{ history: Record<string, string>; source?: { success: boolean; message?: string } }>();
const notes = computed(() => Object.entries(result.value?.history || {}));
async function load() {
  loading.value = true;
  error.value = '';
  try {
    const query = new URLSearchParams({ force: 'true' });
    const value = await api('/plugins/history/' + encodeURIComponent(props.plugin.instance_id) + '?' + query);
    if (!value || !value.history || typeof value.history !== 'object' || Array.isArray(value.history) ||
      !Object.values(value.history).every((note) => typeof note === 'string')) {
      throw new Error('版本历史响应无效');
    }
    result.value = value;
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
onMounted(load);
</script>
<template>
  <VDialog :model-value="true" width="600" max-height="85vh" :persistent="updating" scrollable content-class="plugin-dialog-surface" @update:model-value="!updating && emit('close')">
    <VCard :title="plugin.name + ' 版本历史'">
      <PluginDialogCloseBtn aria-label="关闭版本历史" :disabled="updating" @click="emit('close')" />
      <VDivider />
      <VCardText class="plugin-dialog-content">
        <div v-if="loading" class="plugin-version-history-dialog__loading" role="status" aria-label="正在读取版本历史"><VProgressCircular indeterminate color="primary" /></div>
        <VAlert v-if="error" type="warning" variant="tonal">{{ error }}</VAlert>
        <VAlert v-if="updateError" type="warning" variant="tonal">{{ updateError }}</VAlert>
        <VBtn v-if="error" :disabled="loading" variant="text" @click="load">重新读取</VBtn>
        <VAlert v-if="result?.source?.success === false" type="warning" variant="tonal">{{ result.source.message }}；当前显示已安装插件信息。</VAlert>
        <template v-if="result">
          <VAlert v-if="!notes.length" type="info" variant="tonal" density="compact">暂未获取到更新说明</VAlert>
          <VList v-else>
            <VListItem v-for="[version, note] in notes" :key="version">
              <VListItemTitle class="font-weight-bold text-lg">{{ version }}</VListItemTitle>
              <div class="markdown-body" v-html="renderPluginHistory(note)" />
            </VListItem>
          </VList>
        </template>
      </VCardText>
      <template v-if="showUpdateAction">
        <VDivider />
        <VCardItem>
        <VBtn block
          :loading="updating" :disabled="updating || plugin.available === false || !!plugin.update_blocked_reason"
          color="primary" variant="flat" prepend-icon="mdi-arrow-up-circle-outline" @click="emit('update')">更新到最新版本</VBtn>
        </VCardItem>
      </template>
    </VCard>
  </VDialog>
</template>
<style scoped>
.plugin-version-history-dialog__loading { min-height: 12rem; display: flex; align-items: center; justify-content: center; }
.markdown-body { overflow-wrap: anywhere; color: rgb(107 114 128); }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) { margin-block: 0.5rem; font-weight: 600; }
.markdown-body :deep(h1) { font-size: 1.5rem; }
.markdown-body :deep(h2) { font-size: 1.25rem; }
.markdown-body :deep(h3) { font-size: 1.1rem; }
.markdown-body :deep(ul), .markdown-body :deep(ol) { padding-inline-start: 1.5rem; margin-block: 0.5rem; }
.markdown-body :deep(li) { margin-block: 0.25rem; }
.markdown-body :deep(p) { margin-block: 0.5rem; }
.markdown-body :deep(a) { color: rgb(99 102 241); text-decoration: none; }
.markdown-body :deep(a:hover) { text-decoration: underline; }
.markdown-body :deep(code) { padding: 0.15rem 0.4rem; border-radius: 0.25rem; font-size: 0.875em; background-color: rgba(127,127,127,0.15); }
.markdown-body :deep(pre) { padding: 0.75rem 1rem; margin-block: 0.5rem; overflow-x: auto; border-radius: 0.375rem; background-color: rgba(127,127,127,0.15); }
.markdown-body :deep(pre code) { padding: 0; background-color: transparent; }
.markdown-body :deep(blockquote) { padding-inline-start: 1rem; margin-block: 0.5rem; border-inline-start: 3px solid rgba(127,127,127,0.4); color: rgba(127,127,127,0.8); }
</style>
