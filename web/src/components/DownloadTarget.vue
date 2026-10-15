<script setup lang="ts">
import { computed, useId } from 'vue';
import AppIcon from './AppIcon.vue';
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
const listID = 'download-directories-' + useId();
const paths = computed(() => downloadDirectoryPaths(ui.settings?.directories || []));
</script>
<template>
  <div class="download-target form-grid">
    <label
      >下载器
      <div class="target-field">
        <AppIcon name="download" /><select
          :value="downloader"
          @change="
            emit('update:downloader', ($event.target as HTMLSelectElement).value);
            emit('change');
          "
        >
          <option value="">默认</option>
          <option
            v-for="d in (ui.settings?.downloaders || []).filter(
              (item: any) => item.enabled !== false,
            )"
            :key="d.id"
            :value="d.id"
          >
            {{ d.name || d.id }}
          </option>
        </select>
      </div></label
    >
    <label
      >保存目录
      <div class="target-field">
        <AppIcon name="folder" /><input
          :value="downloadPath"
          :list="listID"
          placeholder="自动"
          @input="
            emit('update:downloadPath', ($event.target as HTMLInputElement).value);
            emit('change');
          "
        />
      </div>
      <datalist :id="listID"><option v-for="path in paths" :key="path" :value="path" /></datalist>
    </label>
  </div>
</template>
<style scoped>
.download-target {
  margin: 16px 0;
  gap: 24px;
}
.download-target label {
  font-size: 12px;
  color: var(--muted);
}
.target-field {
  display: flex;
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid var(--line);
  margin-top: 3px;
}
.target-field:focus-within {
  border-bottom-color: var(--accent);
  box-shadow: 0 1px 0 var(--accent);
}
.target-field svg {
  color: var(--muted);
}
.target-field input,
.target-field select {
  min-width: 0;
  min-height: 44px;
  border: 0;
  border-radius: 0;
  padding-left: 0;
  box-shadow: none;
  background: transparent;
}
@media (max-width: 959px) {
  .download-target {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
