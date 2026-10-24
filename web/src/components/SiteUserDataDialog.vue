<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { VDivider } from 'vuetify/components/VDivider';
import { VAvatar } from 'vuetify/components/VAvatar';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
import VueApexCharts from 'vue3-apexcharts';
import type { ApexOptions } from 'apexcharts';
import { api, errorText, friendly } from '../api';
import { siteDataDays, siteDataDelta, siteDataBytes, type SiteStats } from '../features/site-data';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';

const props = defineProps<{ site: any; blocked?: boolean }>();
const emit = defineEmits<{ close: []; refreshed: [result: any] }>();
const display = useDisplay();
const row = ref(props.site),
  items = ref<any[]>([]);
const loading = ref(false),
  refreshing = ref(false),
  error = ref('');
let active = true;
const days = computed(() => siteDataDays(items.value, row.value.stats));
const stats = computed<SiteStats>(() => days.value.at(-1)?.stats || row.value.stats || {});
const metrics: { key: keyof SiteStats; label: string; icon: string }[] = [
  { key: 'level', label: '用户等级', icon: 'mdi-account' },
  { key: 'bonus', label: '积分', icon: 'mdi-scoreboard' },
  { key: 'ratio', label: '分享率', icon: 'mdi-percent' },
  { key: 'uploaded', label: '总上传量', icon: 'mdi-upload' },
  { key: 'downloaded', label: '总下载量', icon: 'mdi-download' },
  { key: 'seeding', label: '总做种数', icon: 'mdi-seed' },
  { key: 'seeding_bytes', label: '总做种体积', icon: 'mdi-database' },
  { key: 'joined_at', label: '加入时间', icon: 'mdi-calendar' },
];
const metricColors: Record<string, string> = {
  level: 'primary',
  bonus: 'warning',
  ratio: 'info',
  uploaded: 'success',
  downloaded: 'warning',
  seeding: 'primary',
  seeding_bytes: 'info',
  joined_at: 'secondary',
};
const byteKeys = ['uploaded', 'downloaded', 'seeding_bytes'];
function value(key: keyof SiteStats) {
  if (key === 'ratio' && stats.value.ratio_infinite) return '∞';
  const v = stats.value[key];
  if (v == null || v === '') return '无';
  if (key === 'joined_at') return String(v).split(/[T ]/)[0];
  if (typeof v === 'number')
    return byteKeys.includes(key) ? siteDataBytes(v) : v.toLocaleString('zh-CN');
  return String(v);
}
const cards = computed(() =>
  metrics.map((metric) => {
    const diff = siteDataDelta(days.value, metric.key);
    return {
      ...metric,
      value: value(metric.key),
      diff,
      change:
        diff == null
          ? ''
          : byteKeys.includes(metric.key)
            ? siteDataBytes(diff, true)
            : `${diff >= 0 ? '+' : ''}${Number(diff.toFixed(3)).toLocaleString('zh-CN')}`,
    };
  }),
);
const historySeries = computed(() =>
  ['uploaded', 'downloaded'].map((key, index) => ({
    name: ['上传量', '下载量'][index],
    data: days.value.map((day) => {
      const v = day.stats[key as 'uploaded' | 'downloaded'];
      return typeof v === 'number' ? Math.round(v / 1024 ** 3) : null;
    }),
  })),
);
const chartBase: ApexOptions = {
  chart: {
    parentHeightOffset: 0,
    toolbar: { show: false },
    background: 'transparent',
    foreColor: 'rgba(58,53,65,0.6)',
    zoom: { autoScaleYaxis: true },
  },
  theme: { mode: 'light' },
  noData: { text: '无' },
  legend: { show: false },
  dataLabels: { enabled: false },
  grid: { borderColor: 'rgba(58,53,65,0.1)', strokeDashArray: 4 },
};
const historyOptions = computed<ApexOptions>(() => ({
  ...chartBase,
  stroke: { width: 2.5, lineCap: 'butt', curve: 'smooth' },
  colors: ['#56CA00', '#FFB400'],
  markers: { size: 0 },
  grid: {
    borderColor: 'rgba(58,53,65,0.1)',
    strokeDashArray: 4,
    xaxis: { lines: { show: false } },
    yaxis: { lines: { show: true } },
  },
  xaxis: {
    type: 'category',
    categories: days.value.map((day) => day.day),
    labels: {
      style: { fontSize: '10px' },
      formatter: (val) => new Date(val).toLocaleDateString('zh-CN'),
    },
  },
  yaxis: {
    title: { text: 'GB' },
    labels: { style: { fontSize: '10px' }, formatter: (val) => val.toLocaleString() },
  },
  fill: {
    type: 'gradient',
    gradient: { shadeIntensity: 1, opacityFrom: 0.5, opacityTo: 0.7, stops: [0, 100] },
  },
}));
const seedingSeries = computed(() => [
  {
    name: '体积',
    data: (stats.value.seeding_info || [])
      .filter((point) => point.length === 2 && point.every((n) => Number.isFinite(n) && n >= 0))
      .map(([count, size]) => [count, Math.round(size / 1024 ** 3)]),
  },
]);
const seedingOptions: ApexOptions = {
  ...chartBase,
  colors: ['#9155FD'],
  tooltip: { x: { formatter: (val) => '数量：' + val.toLocaleString() } },
  grid: {
    borderColor: 'rgba(58,53,65,0.1)',
    strokeDashArray: 4,
    xaxis: { lines: { show: true } },
    yaxis: { lines: { show: true } },
  },
  xaxis: {
    type: 'numeric',
    title: { text: '数量：' },
    tickAmount: 10,
    labels: {
      style: { fontSize: '10px' },
      formatter: (val) => Math.round(Number(val)).toLocaleString(),
    },
  },
  yaxis: {
    title: { text: 'GB' },
    labels: { style: { fontSize: '10px' }, formatter: (val) => val.toLocaleString() + ' GB' },
  },
};
async function load() {
  if (loading.value) return;
  loading.value = true;
  try {
    const result = await api(
      '/sites/manage/' + encodeURIComponent(props.site.config.id) + '/history',
    );
    if (active) {
      items.value = result.items;
      error.value = '';
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
async function refresh() {
  if (refreshing.value || loading.value || props.blocked) return;
  refreshing.value = true;
  error.value = '';
  try {
    const result = await api(
      '/sites/manage/' + encodeURIComponent(props.site.config.id) + '/refresh',
      {},
    );
    if (!active) return;
    const updated = result.sites?.find((item: any) => item.config.id === props.site.config.id);
    if (!updated) throw new Error('站点刷新结果缺失，请重新打开');
    row.value = updated;
    emit('refreshed', result);
    await load();
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) refreshing.value = false;
  }
}
onMounted(load);
onUnmounted(() => {
  active = false;
});
</script>
<template>
  <VDialog
    :model-value="true"
    scrollable
    eager
    max-width="80rem"
    :fullscreen="!display.mdAndUp.value"
    content-class="plugin-dialog-surface site-user-data-surface"
    aria-label="站点用户数据"
    @update:model-value="emit('close')"
  >
    <VCard>
      <VCardItem>
        <VCardTitle
          >站点用户数据 - {{ site.config.name }}
          <VBtn
            variant="text"
            color="info"
            icon
            size="default"
            aria-label="刷新站点数据"
            :disabled="blocked || loading || refreshing"
            :loading="refreshing"
            @click="refresh"
            ><VIcon icon="mdi-refresh"
          /></VBtn>
        </VCardTitle>
        <PluginDialogCloseBtn aria-label="关闭站点数据" @click="emit('close')" />
      </VCardItem>
      <VDivider />
      <VCardText class="site-user-data-content">
        <p v-if="loading" role="status">正在加载…</p>
        <VAlert v-if="error" type="error" variant="tonal" class="mb-4"
          >{{ error }}<VBtn variant="text" @click="load">重试</VBtn></VAlert
        >
        <VAlert v-if="row.connection?.error" type="error" variant="tonal" class="mb-4"
          >{{ friendly(row.connection.error) }}{{ row.stats ? '；显示上次成功数据' : '' }}</VAlert
        >
        <VRow class="site-user-metrics site-data-summary-grid">
          <VCol v-for="card in cards" :key="card.key" cols="12" sm="6" md="3">
            <VCard
              class="site-data-summary-card"
              :class="`site-data-summary-card--${metricColors[card.key]}`"
              ><VCardText class="site-user-metric">
                <div>
                  <span class="text-base">{{ card.label }}</span>
                  <h5 class="text-h5">
                    {{ card.value }}
                    <span
                      v-if="card.change"
                      class="site-user-delta text-base"
                      :class="{ 'text-success': card.diff! > 0, 'text-error': card.diff! < 0 }"
                      >({{ card.change }})</span
                    >
                  </h5>
                </div>
                <VAvatar variant="tonal" size="42" rounded
                  ><VIcon :icon="card.icon"
                /></VAvatar> </VCardText
            ></VCard>
          </VCol>
        </VRow>
        <VRow class="match-height">
          <VCol cols="12" md="7"
            ><VCard class="site-data-chart-card">
              <VCardItem class="site-data-chart-header">
                <template #prepend
                  ><VAvatar color="success" variant="tonal" size="34" rounded="lg"
                    ><VIcon icon="mdi-chart-areaspline" size="19" /></VAvatar
                ></template>
                <VCardTitle>历史流量</VCardTitle
                ><template #append><span class="site-data-chart-caption">GB</span></template>
              </VCardItem>
              <VCardText class="site-data-chart-content"
                ><div class="site-data-chart-plot">
                  <VueApexCharts
                    type="line"
                    :options="historyOptions"
                    :series="historySeries"
                    height="100%"
                  />
                </div>
                <div class="site-data-chart-legend">
                  <span
                    ><i class="site-data-legend-dot site-data-legend-dot--upload"></i>上传量</span
                  ><span
                    ><i class="site-data-legend-dot site-data-legend-dot--download"></i>下载量</span
                  >
                </div></VCardText
              >
            </VCard></VCol
          >
          <VCol cols="12" md="5"
            ><VCard class="site-data-chart-card">
              <VCardItem class="site-data-chart-header">
                <template #prepend
                  ><VAvatar color="primary" variant="tonal" size="34" rounded="lg"
                    ><VIcon icon="mdi-chart-scatter-plot" size="19" /></VAvatar
                ></template>
                <VCardTitle>做种分布</VCardTitle
                ><template #append><span class="site-data-chart-caption">GB</span></template>
              </VCardItem>
              <VCardText class="site-data-chart-content"
                ><div class="site-data-chart-plot">
                  <VueApexCharts
                    type="scatter"
                    :options="seedingOptions"
                    :series="seedingSeries"
                    height="100%"
                  />
                </div>
                <div class="site-data-chart-legend">
                  <span><i class="site-data-legend-dot site-data-legend-dot--seed"></i>体积</span>
                </div></VCardText
              >
            </VCard></VCol
          >
        </VRow>
      </VCardText>
    </VCard>
  </VDialog>
  <VDialog
    :model-value="refreshing"
    persistent
    max-width="20rem"
    content-class="plugin-dialog-surface"
  >
    <VCard
      ><VCardText class="site-user-refresh" role="status"
        ><VIcon icon="mdi-loading" class="spinning" />正在刷新站点数据...</VCardText
      ></VCard
    >
  </VDialog>
</template>
<style scoped>
.site-user-metrics > .v-col {
  display: flex;
}
.site-user-metrics > .v-col > .v-card {
  width: 100%;
}
.site-user-metric {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  height: 100%;
}
.site-user-metric > div:first-child {
  min-width: 0;
}
.site-user-metric h5 {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: 4px 0 0;
  font-size: 24px;
  font-weight: 500;
  line-height: 1.334;
  overflow-wrap: anywhere;
}
.site-user-delta {
  font-size: 16px;
  font-weight: 400;
}
.site-user-metric .v-avatar {
  flex-shrink: 0;
}
.site-user-data-content {
  padding-top: 20px;
}
.site-user-refresh {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* Match the installed MoviePilot dialog, including its local theme overrides. */
.site-data-summary-card,
.site-data-chart-card {
  overflow: hidden;
  border: 0;
  border-radius: 12px;
  background: rgb(var(--v-theme-surface));
  box-shadow: none;
}
.site-data-summary-card {
  --site-data-accent: var(--v-theme-primary);
  block-size: 100%;
  transition:
    border-color 0.2s,
    transform 0.2s,
    box-shadow 0.2s;
}
.site-data-summary-card--success {
  --site-data-accent: var(--v-theme-success);
}
.site-data-summary-card--warning {
  --site-data-accent: var(--v-theme-warning);
}
.site-data-summary-card--info {
  --site-data-accent: var(--v-theme-info);
}
.site-data-summary-card--secondary {
  --site-data-accent: var(--v-theme-secondary);
}
.site-data-summary-card:hover {
  border-color: rgba(var(--site-data-accent), 0.34);
  box-shadow: 0 0.5rem 1.25rem rgba(var(--v-theme-on-surface), 0.09);
  transform: translateY(-2px);
}
.site-data-summary-card .v-card-text {
  position: relative;
  z-index: 1;
  min-block-size: 98px;
  padding: 1rem;
}
.site-data-summary-card .v-card-text > .d-flex {
  gap: 0.85rem;
}
.site-data-summary-card .text-base {
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  font-size: 0.72rem;
  font-weight: 500;
  letter-spacing: 0.01em;
}
.site-data-summary-card .text-h5 {
  color: rgba(var(--v-theme-on-surface), var(--v-high-emphasis-opacity));
  font-size: 24px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.35;
}
.site-data-summary-card .text-h5 > .text-base {
  display: inline-flex;
  align-items: center;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 999px;
  background: rgba(var(--v-theme-on-surface), 0.045);
  font-size: 0.66rem;
  font-weight: 600;
  line-height: 1;
  padding: 0.28rem 0.42rem;
}
.site-data-summary-card .text-h5 > .text-base.text-success {
  border-color: rgba(var(--v-theme-success), 0.22);
  background: rgba(var(--v-theme-success), 0.1);
  color: rgb(var(--v-theme-success)) !important;
}
.site-data-summary-card .text-h5 > .text-base.text-error {
  border-color: rgba(var(--v-theme-error), 0.22);
  background: rgba(var(--v-theme-error), 0.1);
  color: rgb(var(--v-theme-error)) !important;
}
.site-data-summary-card .v-avatar {
  flex: 0 0 auto;
  border: 1px solid rgba(var(--site-data-accent), 0.18);
  box-shadow: 0 0.45rem 1rem rgba(var(--site-data-accent), 0.12);
  background: rgba(var(--site-data-accent), 0.13) !important;
  color: rgb(var(--site-data-accent)) !important;
}
.site-data-chart-card {
  display: flex;
  flex-direction: column;
  block-size: 100%;
  min-block-size: 310px;
}
.site-data-chart-header {
  min-block-size: 64px;
}
.site-data-chart-header .v-card-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.site-data-chart-caption {
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.site-data-chart-content {
  display: flex;
  flex: 1 1 auto;
  min-block-size: 0px;
  flex-direction: column;
  overflow: hidden;
}
.site-data-chart-plot {
  flex: 1 1 auto;
  min-block-size: 230px;
  block-size: clamp(15rem, 25vw, 20rem);
  min-inline-size: 0px;
}
.site-data-chart-legend {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 1rem;
  border-block-start: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  font-size: 0.72rem;
  padding-block-start: 0.65rem;
}
.site-data-chart-legend > span {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  min-inline-size: 0px;
}
.site-data-legend-dot {
  display: inline-block;
  border-radius: 50%;
  block-size: 0.48rem;
  inline-size: 0.48rem;
  box-shadow: 0 0 0 3px rgba(var(--v-theme-on-surface), 0.06);
}
.site-data-legend-dot--upload {
  background: rgb(var(--v-theme-success));
}
.site-data-legend-dot--download {
  background: rgb(var(--v-theme-warning));
}
.site-data-legend-dot--seed {
  background: rgb(var(--v-theme-primary));
}
@media (max-width: 600px) {
  .site-data-chart-card {
    min-block-size: 290px;
  }
  .site-data-chart-plot {
    min-block-size: 210px;
    block-size: 14rem;
  }
  .site-data-chart-legend {
    gap: 0.75rem;
  }
}
.site-data-summary-card .text-h5 {
  margin: 4px 0 0;
}
.site-data-summary-card .site-user-metric {
  gap: 0.85rem;
}
.site-data-chart-card > .v-card-text {
  padding-top: 0;
}
.site-user-data-content :deep(.v-card),
:global(.site-user-data-surface > .v-card) {
  background: rgba(255, 255, 255, 0.95);
}
.site-data-chart-plot :deep(.vue-apexcharts) {
  height: 100%;
}
</style>
