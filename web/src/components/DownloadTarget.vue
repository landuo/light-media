<script setup lang="ts">
import { computed, useId } from 'vue';
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
      >下载器<select
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
      </select></label
    >
    <label
      >保存目录<input
        :value="downloadPath"
        :list="listID"
        placeholder="自动"
        @input="
          emit('update:downloadPath', ($event.target as HTMLInputElement).value);
          emit('change');
        "
      />
      <datalist :id="listID"><option v-for="path in paths" :key="path" :value="path" /></datalist>
    </label>
  </div>
</template>
<style scoped>
.download-target {
  margin: 14px 0;
}
</style>
