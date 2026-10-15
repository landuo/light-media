<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import AppIcon from './AppIcon.vue';
import { api, action, bytes, errorText, image, ui, go } from '../api';
import {
  downloadTaskInfo,
  downloadTimeLeft,
  downloadingTaskIsRunning,
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
let generation = 0,
  timer: number;
async function load(silent = false) {
  if (!selected.value) return;
  const seq = ++generation;
  if (!silent) loading.value = true;
  error.value = '';
  try {
    const result = await api('/downloads?downloader_id=' + encodeURIComponent(selected.value));
    if (seq === generation) data.value = result;
  } catch (e) {
    if (seq === generation) error.value = errorText(e);
  } finally {
    if (seq === generation) loading.value = false;
  }
}
function pick(id: string) {
  selected.value = id;
  data.value = null;
  void load();
}
function identity(task: any) {
  return downloadTaskInfo(data.value, selected.value, task);
}
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
  timer = window.setInterval(() => {
    if (!document.hidden && !loading.value && !busy.value.length) void load(true);
  }, 3000);
});
onUnmounted(() => {
  ++generation;
  clearInterval(timer);
});
</script>
<template>
  <div class="page-actions">
    <button :disabled="loading" @click="load()"><AppIcon name="refresh" />刷新</button>
  </div>
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
  <p v-if="error || instance?.error" class="error-box" role="alert">
    {{ error || instance.error }}
  </p>
  <p v-if="loading && !instance" class="loading-state">正在读取下载任务…</p>
  <div v-if="instance?.tasks?.length" class="download-grid">
    <article v-for="task in instance.tasks" :key="task.hash" class="download-card">
      <img
        v-if="identity(task).media?.poster_path || identity(task).media?.backdrop_path"
        class="download-backdrop"
        :src="image(identity(task).media.poster_path || identity(task).media.backdrop_path)"
        alt=""
      />
      <div class="download-info">
        <h3>{{ identity(task).title }} {{ identity(task).seasonEpisode }}</h3>
        <p class="download-filename">{{ identity(task).subtitle }}</p>
        <p class="download-speed">
          {{ bytes(task.total_size) }}　↑ {{ bytes(task.upload_speed) }}/s　↓
          {{ bytes(task.download_speed) }}/s {{ downloadTimeLeft(task) }}
        </p>
        <div class="progress">
          <span :style="{ width: Math.min(100, task.progress * 100) + '%' }"></span>
        </div>
        <div class="download-footer">
          <span>{{ (task.progress * 100).toFixed(1) }}%</span>
          <div class="button-row">
            <button
              class="icon-button"
              :aria-label="
                downloadingTaskIsRunning(task) ? '暂停 ' + task.name : '开始 ' + task.name
              "
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
      </div>
    </article>
  </div>
  <div v-else-if="!loading && !error && !instance?.error" class="empty-state">
    <h3>{{ clients.length ? '暂无正在下载的任务' : '未配置下载器' }}</h3>
    <button v-if="!clients.length" @click="go('settings?tab=system')">配置下载器</button>
  </div>
</template>
<style scoped>
.download-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 330px), 1fr));
  gap: 18px;
}
.download-card {
  position: relative;
  overflow: hidden;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: var(--surface);
  color: var(--text, #172b3a);
  min-height: 242px;
  box-shadow: var(--shadow);
  transition:
    border-color 0.18s,
    box-shadow 0.18s;
}
.download-card:hover {
  border-color: #bdcfd2;
  box-shadow: 0 7px 24px #172b3a0c;
}
.download-backdrop {
  position: absolute;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0.1;
  filter: saturate(0.65);
}
.download-info {
  position: relative;
  padding: 24px;
  background: linear-gradient(145deg, #ffffffa6, #fffffff2 70%);
  height: 100%;
  display: flex;
  flex-direction: column;
}
.download-info h3 {
  margin: 0 0 8px;
  overflow-wrap: anywhere;
  font-size: 17px;
  font-weight: 650;
  line-height: 1.5;
}
.download-filename,
.download-speed {
  font-size: 12px;
  color: var(--muted);
  overflow-wrap: anywhere;
  margin: 4px 0 8px;
  line-height: 1.7;
}
.download-info .progress {
  margin-top: auto;
  margin-bottom: 14px;
  background: #e3eceb;
  height: 5px;
}
.download-info .progress span {
  background: var(--accent);
}
.download-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: var(--muted);
}
.download-footer > span {
  font-variant-numeric: tabular-nums;
  font-weight: 650;
  color: var(--accent);
}
.download-footer button {
  color: var(--muted);
  background: transparent;
  border: 0;
  width: 36px;
  height: 36px;
  padding: 8px;
  border-radius: 9px;
}
.download-footer button:hover {
  background: var(--accent-soft);
  color: var(--accent);
}
.download-footer button.danger:hover {
  background: #fceff0;
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
