<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { VCard } from 'vuetify/components/VCard';
import { VTable } from 'vuetify/components/VTable';
import { VChip } from 'vuetify/components/VChip';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText, toast } from '../api';

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
const items = ref<Service[]>([]),
  loading = ref(true),
  error = ref('');
let mounted = true,
  fetching = false,
  timer: ReturnType<typeof setInterval>;
const refreshTimers = new Set<ReturnType<typeof setTimeout>>();
async function load() {
  if (!mounted || fetching || document.hidden) return;
  fetching = true;
  try {
    const result = await api<Service[]>('/plugins/services');
    if (mounted) {
      items.value = result;
      error.value = '';
    }
  } catch (e) {
    if (mounted) error.value = errorText(e);
  } finally {
    fetching = false;
    if (mounted) loading.value = false;
  }
}
function run(item: Service) {
  if (item.status === '正在运行') return;
  // Fixed ServiceView starts the synchronous host endpoint without awaiting it,
  // acknowledges submission, then refreshes after one second. Completion and
  // retained failures remain available through the ordinary progress API.
  void api('/v1/system/runscheduler?jobid=' + encodeURIComponent(item.id)).catch((e) => {
    if (mounted) {
      error.value = errorText(e);
      toast(error.value, true);
    }
  });
  toast('定时作业执行请求提交成功！');
  const pending = setTimeout(() => {
    refreshTimers.delete(pending);
    void load();
  }, 1000);
  refreshTimers.add(pending);
}

function visible() {
  if (!document.hidden) void load();
}
onMounted(() => {
  void load();
  timer = setInterval(() => void load(), 5000);
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
    <VAlert v-if="error" type="error" variant="tonal" class="service-error" role="alert">
      {{ error }} <VBtn variant="text" size="small" @click="load">重试</VBtn>
    </VAlert>
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
        <tr v-for="item in items" :key="item.instance_id + ':' + item.service_id">
          <td>{{ item.provider }}</td>
          <td>{{ item.name }}</td>
          <td>
            <VChip :color="item.status === '正在运行' ? 'success' : item.status === '停止' ? 'error' : undefined">{{ item.status }}</VChip>
          </td>
          <td>{{ item.next_run }}</td>
          <td>
            <VBtn
              size="small"
              :disabled="item.status === '正在运行'"
              @click="run(item)"
              ><template #prepend><VIcon icon="mdi-play" /></template>执行</VBtn
            >
          </td>
        </tr>
        <tr v-if="!items.length">
          <td colspan="5" class="service-empty">
            {{ loading ? '正在加载…' : error ? '服务列表加载失败' : '没有后台服务' }}
          </td>
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
.service-error {
  margin: 16px;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>
