<script setup lang="ts">
import { computed, watch, useId } from 'vue';
import { ui, label, go } from '../api';
import { directoriesFor, matchingDirectory } from '../features/download-directories';
const props = defineProps<{
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
const client = computed(() =>
  ui.settings?.downloaders?.find((d: any) => d.id === props.downloader),
);
const options = computed(() =>
  directoriesFor(ui.settings?.directories || [], props.downloader, props.mediaType),
);
const paths = computed(() => [
  ...new Set(
    [client.value?.download_path, ...options.value.map((d) => d.download_path)].filter(Boolean),
  ),
]);
const rule = computed(() =>
  matchingDirectory(
    ui.settings?.directories || [],
    props.downloader,
    props.downloadPath,
    props.mediaType,
    client.value?.mappings || [],
  ),
);
const target = computed(() => ui.settings?.roots?.find((r: any) => r.id === rule.value?.root_id));
function pathChanged(value: string) {
  emit('update:downloadPath', value);
  emit('change');
}
watch(
  () => props.downloader,
  (value, previous) => {
    if (!props.downloadPath || (previous !== undefined && value !== previous)) {
      pathChanged(client.value?.download_path || options.value[0]?.download_path || '');
    }
  },
  { immediate: true },
);
watch(
  () => [props.downloadPath, props.mediaType, props.downloader, rule.value?.id],
  () => {
    const id = rule.value?.id || '';
    if (props.directoryId !== id) emit('update:directoryId', id);
  },
  { immediate: true },
);
</script>
<template>
  <div class="download-target">
    <div class="form-grid">
      <label
        >下载器<select
          :value="downloader"
          @change="
            emit('update:downloader', ($event.target as HTMLSelectElement).value);
            emit('change');
          "
        >
          <option value="" disabled>选择下载器</option>
          <option v-for="d in ui.settings?.downloaders || []" :key="d.id" :value="d.id">
            {{ d.name || d.id }}
          </option>
        </select></label
      >
      <label
        >下载目录<input
          :value="downloadPath"
          :list="listID"
          placeholder="选择或输入下载器内的目录"
          @input="pathChanged(($event.target as HTMLInputElement).value)"
        />
        <datalist :id="listID"><option v-for="path in paths" :key="path" :value="path" /></datalist>
        <small class="hint">此路径是文件在下载器中的保存位置。</small>
      </label>
    </div>
    <div
      class="destination-note"
      :class="{ inactive: !rule?.enabled || rule?.monitor_type === 'none' }"
    >
      <template v-if="rule?.enabled && rule.monitor_type !== 'none'">
        <strong>{{ rule.monitor_type === 'monitor' ? '目录监听整理' : '下载完成后整理' }}</strong>
        <span
          >{{ rule.name }} · {{ label(rule.mode) }} →
          {{ target?.name || target?.path || rule.root_id }}</span
        >
      </template>
      <template v-else
        ><strong>仅下载</strong
        ><span>{{
          rule ? '此目录的自动整理尚未启用。' : '此下载目录尚未配置整理规则。'
        }}</span></template
      >
      <button type="button" class="small ghost" @click="go('directories')">目录管理</button>
    </div>
  </div>
</template>
<style scoped>
.download-target {
  margin: 14px 0;
}
.destination-note {
  display: flex;
  align-items: center;
  gap: 9px 14px;
  flex-wrap: wrap;
  background: var(--accent-soft);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 10px 12px;
  font-size: 12px;
}
.destination-note > span {
  color: var(--muted);
  overflow-wrap: anywhere;
  flex: 1;
}
.destination-note.inactive {
  background: var(--surface);
}
.destination-note button {
  margin-left: auto;
}
</style>
