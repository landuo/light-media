<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { VCard } from 'vuetify/components/VCard';
import { VTable } from 'vuetify/components/VTable';
import { VChip } from 'vuetify/components/VChip';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { api, toast } from '../api';

type Service = {
  id: string;
  instance_id: string;
  service_id: string;
  provider: string;
  name: string;
  status: string;
  next_run: string | null;
  can_run: boolean;
};
const items = ref<Service[]>([]);
let mounted = true,
  fetching = false,
  background = false;
let timer: ReturnType<typeof setInterval> | undefined;
const refreshTimers = new Set<ReturnType<typeof setTimeout>>();

// ServiceView calls this directly after a manual submission, even when hidden
// or another read is pending. Only initial/poll reads use the single-flight gate.
async function load() {
  if (!mounted) return;
  try {
    const result = await api<Service[]>('/plugins/services');
    if (mounted) items.value = result;
  } catch {
    // Keep the source's existing/empty rows, without a new error or retry UI.
    // Never print an API error object, which could contain sensitive context.
    console.error('后台服务列表加载失败');
  }
}
async function refresh() {
  if (!mounted || fetching) return;
  fetching = true;
  try {
    await load();
  } finally {
    fetching = false;
  }
}
function run(item: Service) {
  if (!mounted || item.status === '正在运行') return;
  // Fixed ServiceView starts the synchronous host endpoint without awaiting it,
  // acknowledges submission, then refreshes after one second. Completion and
  // retained failures remain available through the ordinary progress API.
  try {
    void api('/v1/system/runscheduler?jobid=' + encodeURIComponent(item.id)).catch(() => {
      console.error('定时作业执行请求失败');
    });
    toast('定时作业执行请求提交成功！');
    const pending = setTimeout(() => {
      refreshTimers.delete(pending);
      void load();
    }, 1000);
    refreshTimers.add(pending);
  } catch {
    console.error('定时作业执行请求失败');
  }
}

function startTimer() {
  if (mounted && timer === undefined)
    timer = setInterval(() => {
      if (!background) void refresh();
    }, 5000);
}
function visible() {
  const previous = background;
  background = document.hidden;
  if (background && !previous) {
    clearInterval(timer);
    timer = undefined;
  } else if (!background && previous) startTimer();
}
onMounted(async () => {
  await refresh();
  if (!mounted) return;
  // The fixed useDataRefresh waits for its immediate read before adding the
  // timer. Initial hidden mounts still read and retain that first timer anchor.
  background = document.hidden;
  startTimer();
  document.addEventListener('visibilitychange', visible);
});
onUnmounted(() => {
  mounted = false;
  clearInterval(timer);
  for (const pending of refreshTimers) clearTimeout(pending);
  refreshTimers.clear();
  document.removeEventListener('visibilitychange', visible);
});
</script>
<template>
  <VCard class="plugin-services-card">
    <VTable class="service-table">
      <thead>
        <tr>
          <th scope="col">提供者</th>
          <th scope="col">任务名称</th>
          <th scope="col">任务状态</th>
          <th scope="col">下一次执行时间</th>
          <th scope="col"><span class="visually-hidden">操作</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in items" :key="item.id">
          <td>{{ item.provider }}</td>
          <td>{{ item.name }}</td>
          <td>
            <VChip
              :color="
                item.status === '正在运行' ? 'success' : item.status === '停止' ? 'error' : ''
              "
              >{{ item.status }}</VChip
            >
          </td>
          <td>{{ item.next_run }}</td>
          <td>
            <VBtn size="small" :disabled="item.status === '正在运行'" @click="run(item)"
              ><template #prepend><VIcon icon="mdi-play" /></template>执行</VBtn
            >
          </td>
        </tr>
        <tr v-if="!items.length">
          <td colspan="4" class="service-empty">没有后台服务</td>
        </tr>
      </tbody>
    </VTable>
  </VCard>
</template>
<style scoped>
.service-table {
  white-space: nowrap;
}
.service-table th {
  font-size: 14px;
  font-weight: 500;
}
.service-table td {
  font-size: 14px;
}
.service-empty {
  text-align: center;
  color: rgba(var(--v-theme-on-surface), 0.6);
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>
