<script setup lang="ts">
import { computed } from 'vue';
import { VSelect } from 'vuetify/components/VSelect';
import { VCombobox } from 'vuetify/components/VCombobox';
import { ui } from '../api';
import { downloadDirectoryPaths } from '../features/download-directories';
defineProps<{
  downloader: string;
  downloadPath: string;
  directoryId?: string;
  mediaType?: string;
}>();
const emit = defineEmits<{
  'update:downloader': [value: string];
  'update:downloadPath': [value: string];
  'update:directoryId': [value: string];
  change: [];
}>();
const paths = computed(() => downloadDirectoryPaths(ui.settings?.directories || []));
</script>
<template>
  <div class="download-target form-grid">
    <VSelect
      :model-value="downloader"
      label="下载器"
      prepend-inner-icon="mdi-download"
      :items="[
        { title: '默认', value: '' },
        ...(ui.settings?.downloaders || [])
          .filter((d: any) => d.enabled !== false)
          .map((d: any) => ({ title: d.name || d.id, value: d.id })),
      ]"
      @update:model-value="
        emit('update:downloader', $event);
        emit('change');
      "
    />
    <VCombobox
      :model-value="downloadPath"
      label="保存目录"
      prepend-inner-icon="mdi-folder-outline"
      :items="paths"
      :return-object="false"
      placeholder="自动"
      persistent-placeholder
      @update:model-value="
        emit('update:downloadPath', String($event ?? ''));
        emit('change');
      "
    />
  </div>
</template>
<style scoped>
.download-target {
  margin: 16px 0;
  gap: 24px;
}
@media (max-width: 959px) {
  .download-target {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
