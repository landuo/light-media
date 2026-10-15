<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, action, bytes, errorText, image, label, ui, go } from '../api';
const clients = computed(() =>
  (ui.settings?.downloaders || []).filter((d: any) => d.enabled !== false),
);
const selected = ref(clients.value[0]?.id || ''),
  data = ref<any>(null),
  loading = ref(false),
  error = ref(''),
  busy = ref<string[]>([]);
const instance = computed(() => data.value?.instances?.find((d: any) => d.id === selected.value));
const pending = computed(() =>
  (data.value?.native_downloads || []).filter(
    (d: any) =>
      d.downloader_id === selected.value &&
      ['planned', 'submitting', 'waiting', 'failed'].includes(d.state),
  ),
);
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
  return data.value?.intents?.find(
    (i: any) =>
      i.downloader_id === selected.value && i.hash?.toLowerCase() === task.hash?.toLowerCase(),
  )?.media;
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
  <div class="page-heading">
    <h1>下载管理</h1>
    <button :disabled="loading" @click="load()">刷新</button>
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
  <section v-if="pending.length" class="pending-downloads">
    <p v-for="item in pending" :key="item.id">
      <span class="badge">{{
        item.state === 'failed'
          ? '添加失败'
          : item.state === 'waiting'
            ? '等待下载器启用'
            : '正在添加'
      }}</span>
      {{ item.resource_title }}<small v-if="item.error" class="error-text">{{ item.error }}</small>
    </p>
  </section>
  <div v-if="instance?.tasks?.length" class="download-grid">
    <article v-for="task in instance.tasks" :key="task.hash" class="download-card">
      <img
        v-if="identity(task)?.backdrop_path || identity(task)?.poster_path"
        class="download-backdrop"
        :src="image(identity(task).backdrop_path || identity(task).poster_path)"
        alt=""
      />
      <div class="download-info">
        <h3>{{ identity(task)?.title || task.name }}</h3>
        <p v-if="identity(task)" class="download-filename">{{ task.name }}</p>
        <p class="download-state">{{ label(task.status) }}</p>
        <p class="download-speed">
          {{ bytes(task.total_size) }}　↑ {{ bytes(task.upload_speed) }}/s　↓
          {{ bytes(task.download_speed) }}/s
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
                task.status === 'downloading' || task.status === 'seeding'
                  ? '暂停 ' + task.name
                  : '开始 ' + task.name
              "
              :disabled="busy.includes(task.hash)"
              @click="
                control(task, ['downloading', 'seeding'].includes(task.status) ? 'pause' : 'start')
              "
            >
              {{ ['downloading', 'seeding'].includes(task.status) ? 'Ⅱ' : '▶' }}</button
            ><button
              class="icon-button danger"
              :aria-label="'删除下载任务 ' + task.name"
              title="删除下载任务，保留文件"
              :disabled="busy.includes(task.hash)"
              @click="control(task, 'remove_keep_data')"
            >
              ×
            </button>
          </div>
        </div>
      </div>
    </article>
  </div>
  <div v-else-if="!loading && !error && !instance?.error" class="empty-state">
    <h3>{{ clients.length ? '暂无下载任务' : '未配置下载器' }}</h3>
    <button v-if="!clients.length" @click="go('settings?tab=system')">配置下载器</button>
  </div>
</template>
<style scoped>
.download-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 18px;
}
.download-card {
  position: relative;
  overflow: hidden;
  border-radius: 12px;
  background: #263348;
  color: #fff;
  min-height: 220px;
}
.download-backdrop {
  position: absolute;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0.25;
}
.download-info {
  position: relative;
  padding: 22px;
  background: linear-gradient(180deg, transparent, #172235bb);
  height: 100%;
  display: flex;
  flex-direction: column;
}
.download-info h3 {
  margin: 0 0 10px;
  overflow-wrap: anywhere;
  font-size: 17px;
  line-height: 1.5;
}
.download-filename,
.download-speed,
.download-state {
  font-size: 12px;
  opacity: 0.85;
  overflow-wrap: anywhere;
  margin: 4px 0;
}
.download-info .progress {
  margin-top: auto;
  margin-bottom: 8px;
  background: #ffffff30;
}
.download-info .progress span {
  background: #9a8aff;
}
.download-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
}
.download-footer button {
  color: #fff;
  background: transparent;
  border: 0;
}
.pending-downloads small {
  display: block;
  margin: 4px 0;
}
</style>
