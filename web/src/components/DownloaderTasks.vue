<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import AppIcon from './AppIcon.vue';
import { createSingleFlightRequest } from '../features/single-flight-request';
import { api, action, bytes, errorText, image, ui, go } from '../api';
import {
  downloadTaskInfo,
  createDownloadTaskIndex,
  downloadTimeLeft,
  downloadingTaskIsRunning,
  downloadSourceFileSize,
} from '../features/downloader-tasks';
const clients = computed(() =>
  (ui.settings?.downloaders || []).filter((d: any) => d.enabled !== false),
);
const selected = ref(clients.value[0]?.id || ''),
  data = ref<any>(null),
  loading = ref(false),
  error = ref(''),
  busy = ref<string[]>([]);
const instance = computed(() => data.value?.instances?.find((d: any) => d.id === selected.value));
let disposed = false;
const requests = createSingleFlightRequest({
  request: (id, signal) =>
    api('/downloads?downloader_id=' + encodeURIComponent(id), undefined, undefined, signal),
  result: (result) => {
    data.value = result;
  },
  error: (e) => {
    error.value = errorText(e);
  },
  settled: () => {
    loading.value = false;
  },
});
function load(silent = false) {
  if (disposed || !selected.value) return Promise.resolve();
  if (!silent) loading.value = true;
  if (!requests.pending) error.value = '';
  return requests.run(selected.value);
}
function pick(id: string) {
  if (id === selected.value) return;
  requests.cancel();
  selected.value = id;
  data.value = null;
  void load();
}
const taskIndex = computed(() => createDownloadTaskIndex(data.value));
const taskRows = computed(() =>
  (instance.value?.tasks || []).map((task: any) => ({
    task,
    info: downloadTaskInfo(data.value, selected.value, task, taskIndex.value),
  })),
);
async function control(task: any, operation: string) {
  if (busy.value.includes(task.hash)) return;
  busy.value.push(task.hash);
  try {
    const result = await action(() =>
      api('/downloads/task-action', {
        downloader_id: selected.value,
        hash: task.hash,
        action: operation,
      }),
    );
    if (result) await load(true);
  } finally {
    busy.value = busy.value.filter((hash) => hash !== task.hash);
  }
}
onMounted(() => {
  void load();
});
onUnmounted(() => {
  disposed = true;
  requests.cancel();
});
</script>
<template>
  <div class="download-toolbar">
    <div v-if="clients.length" class="tabs" role="tablist" aria-label="下载器">
      <button
        v-for="client in clients"
        :key="client.id"
        role="tab"
        :aria-selected="selected === client.id"
        :class="{ selected: selected === client.id }"
        @click="pick(client.id)"
      >
        {{ client.name || client.id }}
      </button>
    </div>
    <button
      class="download-refresh"
      aria-label="刷新下载任务"
      title="刷新"
      :disabled="loading"
      @click="load()"
    >
      <AppIcon name="refresh" />
    </button>
  </div>
  <p v-if="error || instance?.error" class="error-box" role="alert">
    {{ error || instance.error }}
  </p>
  <p v-if="loading && !instance" class="loading-state">正在读取下载任务…</p>
  <div v-if="instance?.tasks?.length" class="download-grid">
    <article v-for="{ task, info } in taskRows" :key="task.hash || task.name" class="download-card">
      <img
        v-if="info.media?.poster_path || info.media?.backdrop_path"
        class="download-backdrop"
        :src="image(info.media.poster_path || info.media.backdrop_path)"
        alt=""
      />
      <div class="download-info">
        <h3>{{ info.title }} {{ info.seasonEpisode }}</h3>
        <p class="download-filename">{{ info.subtitle }}</p>
        <p class="download-speed">
          {{
            task.source_torrent
              ? downloadSourceFileSize(task.source_torrent.size)
              : bytes(task.total_size)
          }}　↑
          {{
            task.source_torrent ? task.source_torrent.upspeed || '' : bytes(task.upload_speed)
          }}/s　↓
          {{
            task.source_torrent ? task.source_torrent.dlspeed || '' : bytes(task.download_speed)
          }}/s {{ downloadTimeLeft(task) }}
        </p>
        <div
          class="progress"
          role="progressbar"
          :aria-valuenow="Math.round(task.progress * 100)"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-label="'下载进度 ' + task.name"
        >
          <span :style="{ width: Math.min(100, task.progress * 100) + '%' }"></span>
        </div>
        <div class="download-footer">
          <button
            class="icon-button"
            :aria-label="downloadingTaskIsRunning(task) ? '暂停 ' + task.name : '开始 ' + task.name"
            :disabled="busy.includes(task.hash)"
            @click="control(task, downloadingTaskIsRunning(task) ? 'pause' : 'start')"
          >
            <AppIcon :name="downloadingTaskIsRunning(task) ? 'pause' : 'play'" /></button
          ><button
            class="icon-button danger"
            :aria-label="'删除下载任务和文件 ' + task.name"
            title="删除下载任务和文件"
            :disabled="busy.includes(task.hash)"
            @click="control(task, 'remove_with_data')"
          >
            <AppIcon name="trash" />
          </button>
        </div>
      </div>
    </article>
  </div>
  <div v-else-if="!loading && !error && !instance?.error" class="empty-state">
    <h3>{{ clients.length ? '暂无正在下载的任务' : '未配置下载器' }}</h3>
    <button v-if="!clients.length" @click="go('settings?tab=system')">配置下载器</button>
  </div>
</template>
<style scoped>
.download-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}
.download-toolbar .tabs {
  flex: 1;
  min-width: 0;
  margin: 0;
}
.download-refresh {
  border: 0;
  background: transparent;
  color: var(--muted);
  width: 40px;
  height: 40px;
  padding: 9px;
  border-radius: 50%;
}
.download-refresh:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
}
.download-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 330px), 1fr));
  gap: 24px;
}
.download-card {
  position: relative;
  overflow: hidden;
  border-radius: 6px;
  background: #1f2937;
  color: white;
  min-height: 230px;
  box-shadow: var(--shadow);
  transition: transform 0.3s;
}
.download-card:hover {
  transform: translateY(-4px);
}
.download-backdrop {
  position: absolute;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: top;
}
.download-info {
  position: relative;
  padding: 16px;
  background: linear-gradient(180deg, #1f293778 0%, #1f2937 100%);
  height: 100%;
  display: flex;
  flex-direction: column;
}
.download-info h3 {
  margin: 0 0 8px;
  overflow-wrap: anywhere;
  font-size: 20px;
  font-weight: 500;
  line-height: 1.5;
}
.download-filename,
.download-speed {
  font-size: 14px;
  color: #ffffffc7;
  overflow-wrap: anywhere;
  margin: 4px 0 8px;
  line-height: 1.7;
}
.download-speed {
  padding-top: 8px;
  font-size: 15px;
  color: white;
}
.download-info .progress {
  width: 100%;
  margin-top: auto;
  margin-bottom: 14px;
  background: #56ca0040;
  height: 4px;
  border-radius: 0;
}
.download-info .progress span {
  background: var(--success, #56ca00);
}
.download-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: white;
}
.download-footer button {
  color: white;
  background: transparent;
  border: 0;
  width: 40px;
  height: 40px;
  padding: 9px;
  border-radius: 50%;
}
.download-footer button:hover {
  background: #ffffff14;
}
.download-footer button.danger {
  color: var(--danger);
}
.download-footer button.danger:hover {
  background: #ff4c511f;
  color: var(--danger);
}
@media (max-width: 760px) {
  .download-info {
    padding: 20px;
  }
  .download-footer button {
    width: 42px;
    height: 42px;
  }
}
</style>
