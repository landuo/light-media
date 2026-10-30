<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { VCard, VCardItem, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { VAvatar } from 'vuetify/components/VAvatar';
import { api } from '../api';

const props = withDefaults(defineProps<{ allowRefresh?: boolean }>(), { allowRefresh: true });
const emit = defineEmits<{ loaded: [] }>();
type Schedule = { id: string; name: string; next_run: string | null; status: string };
const schedulerList = ref<Schedule[]>([]);
let disposed = false, loading = false, background = false;
let timer: ReturnType<typeof setInterval> | undefined;

// AnalyticsScheduler/useDataRefresh: one immediate read, then 60-second reads.
// Failed reads retain the previous list; this card is not a manual-run control.
async function loadSchedulerList() {
  if (disposed || loading || !props.allowRefresh) return;
  loading = true;
  try {
    const result = await api<Schedule[]>('/plugins/services');
    if (!disposed) schedulerList.value = result;
  } catch (e) {
    console.error(e);
  } finally {
    loading = false;
    if (!disposed) emit('loaded');
  }
}
function startTimer() {
  if (!disposed && timer === undefined)
    timer = setInterval(() => { if (!background) void loadSchedulerList(); }, 60000);
}
function visibilityChanged() {
  const previous = background;
  background = document.hidden;
  if (background && !previous) {
    clearInterval(timer);
    timer = undefined;
  } else if (!background && previous) startTimer();
}
onMounted(async () => {
  await loadSchedulerList();
  if (disposed) return;
  // BackgroundManager creates its initial interval even when mounted hidden.
  // Its callbacks are gated, so the first visible event preserves that anchor.
  background = document.hidden;
  startTimer();
  document.addEventListener('visibilitychange', visibilityChanged);
});
onUnmounted(() => {
  disposed = true;
  clearInterval(timer);
  document.removeEventListener('visibilitychange', visibilityChanged);
});
</script>

<template>
  <VCard class="dashboard-work-card plugin-scheduler-dashboard">
    <VCardItem><VCardTitle>后台任务</VCardTitle></VCardItem>
    <VCardText class="dashboard-work-content">
      <VList class="card-list">
        <VListItem v-for="item in schedulerList" :key="item.id">
          <template #prepend>
            <VAvatar size="40" variant="tonal" color="" class="me-3">{{ item.name[0] }}</VAvatar>
          </template>
          <VListItemTitle class="mb-1"><span class="text-sm font-weight-medium">{{ item.name }}</span></VListItemTitle>
          <VListItemSubtitle class="text-xs">{{ item.next_run }}</VListItemSubtitle>
          <template #append><div><h4 class="font-weight-medium">{{ item.status }}</h4></div></template>
        </VListItem>
        <VListItem v-if="schedulerList.length === 0">
          <VListItemTitle class="text-center">没有后台服务</VListItemTitle>
        </VListItem>
      </VList>
    </VCardText>
  </VCard>
</template>

<style scoped>
.plugin-scheduler-dashboard, .dashboard-work-content {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-block-size: 0;
}
.card-list {
  --v-card-list-gap: 1.5rem;
  flex: 1 1 auto;
  min-block-size: 0;
  overflow: auto;
}
.card-list::-webkit-scrollbar { display: none; }
</style>
