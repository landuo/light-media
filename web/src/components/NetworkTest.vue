<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { VAlert } from 'vuetify/components/VAlert';
import { VAvatar } from 'vuetify/components/VAvatar';
import { VBtn } from 'vuetify/components/VBtn';
import { VDivider } from 'vuetify/components/VDivider';
import { VList, VListItem, VListItemTitle } from 'vuetify/components/VList';
import { api, errorText } from '../api';
import tmdb from '../assets/tmdb.png';
import tvdb from '../assets/thetvdb.jpeg';
import fanart from '../assets/fanart.webp';
import telegram from '../assets/telegram.webp';
import wechat from '../assets/wechat.png';
import douban from '../assets/douban.png';
import slack from '../assets/slack.webp';
import python from '../assets/python.png';
import github from '../assets/github.png';

type Target = { id: string; name: string; icon: string };
type TestRow = Target & {
  status: 'waiting' | 'testing' | 'success' | 'failed';
  message: string;
  time?: number;
};
type Result = { success: boolean; message: string; time_ms: number };
const logos: Record<string, string> = {
  tmdb,
  tvdb,
  fanart,
  telegram,
  wechat,
  douban,
  slack,
  python,
  github,
};
const rows = ref<TestRow[]>([]);
const loading = ref(false);
const error = ref('');
const controller = new AbortController();
const signal = controller.signal;
let queue: TestRow[] = [];
let running = false;

async function runQueue() {
  if (running) return;
  running = true;
  try {
    while (!signal.aborted && queue.length) {
      const row = queue.shift()!;
      row.status = 'testing';
      row.message = '测试中…';
      row.time = undefined;
      try {
        const result = await api<Result>(
          '/system/nettest?target_id=' + encodeURIComponent(row.id),
          undefined,
          'GET',
          signal,
          true,
        );
        if (signal.aborted) return;
        row.status = result.success ? 'success' : 'failed';
        row.message = result.message || (result.success ? '正常' : '测试失败');
        row.time = result.time_ms;
      } catch (e) {
        if (signal.aborted) return;
        row.status = 'failed';
        row.message = errorText(e);
      }
    }
  } finally {
    running = false;
  }
}
function retest(row: TestRow) {
  if (row.status === 'testing') return;
  row.status = 'waiting';
  row.message = '等待测试';
  row.time = undefined;
  queue = [row, ...queue.filter((item) => item.id !== row.id)];
  void runQueue();
}
async function load() {
  loading.value = true;
  error.value = '';
  try {
    const { targets } = await api<{ targets: Target[] }>(
      '/system/nettest/targets',
      undefined,
      'GET',
      signal,
      true,
    );
    if (signal.aborted) return;
    rows.value = targets.map((item) => ({ ...item, status: 'waiting', message: '未测试' }));
    queue = [...rows.value];
    void runQueue();
  } catch (e) {
    if (!signal.aborted) error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
onMounted(load);
onBeforeUnmount(() => controller.abort());
</script>
<template>
  <p v-if="loading" class="net-placeholder" role="status">正在读取测试目标…</p>
  <VAlert v-else-if="error" type="error" variant="tonal" class="ma-3">
    {{ error }}
    <VBtn variant="text" @click="load">重试</VBtn>
  </VAlert>
  <p v-else-if="!rows.length" class="net-placeholder">暂无测试目标</p>
  <VList v-else lines="two" aria-label="网络测试结果">
    <template v-for="(row, index) in rows" :key="row.id">
      <VListItem>
        <template #prepend><VAvatar :image="logos[row.icon]" /></template>
        <VListItemTitle>{{ row.name }}</VListItemTitle>
        <div class="net-result" :class="row.status" role="status">
          {{ row.message }}
          <span v-if="row.time != null" class="net-time">{{ row.time }} ms</span>
        </div>
        <template #append>
          <VBtn
            size="small"
            variant="tonal"
            icon="mdi-connection"
            :disabled="row.status === 'testing'"
            :title="'重新测试 ' + row.name"
            :aria-label="'重新测试 ' + row.name"
            @click="retest(row)"
          />
        </template>
      </VListItem>
      <VDivider v-if="index + 1 < rows.length" inset />
    </template>
  </VList>
</template>
<style scoped>
.net-placeholder {
  padding: 16px 20px;
}
.net-result {
  margin-top: 4px;
  color: rgba(var(--v-theme-on-surface), 0.72);
  font-size: 0.875rem;
  overflow-wrap: anywhere;
}
.net-result::before {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-inline-end: 6px;
  border-radius: 50%;
  background: currentColor;
  content: '';
}
.net-result.testing::before {
  background: rgb(var(--v-theme-warning));
}
.net-result.success::before {
  background: rgb(var(--v-theme-success));
}
.net-result.failed::before {
  background: rgb(var(--v-theme-error));
}
.net-time {
  margin-left: 6px;
  white-space: nowrap;
  font-size: 0.75rem;
}
</style>
