<script setup lang="ts">
import { computed } from 'vue';
import VueApexCharts from 'vue3-apexcharts';
import { validPluginPieChart, pluginChartHTML } from '../features/plugin-forms';

const props = defineProps<{ height: number; options: Record<string, any>; series: number[] }>();
const valid = computed(() => validPluginPieChart(props));
const options = computed(() => ({
  ...props.options,
  chart: { type: 'pie', fontFamily: 'inherit', foreColor: '#3A3541', toolbar: { show: false } },
  legend: {
    ...props.options.legend,
    labels: { useSeriesColors: true },
    formatter: pluginChartHTML,
  },
  // Match the original frontend's global Apex defaults: actual GB values.
  dataLabels: {
    formatter: (_: number, context: any) => {
      const value = context.w.config.series[context.seriesIndex];
      return value.toFixed(value % 1 === 0 ? 0 : 1);
    },
  },
  tooltip: { theme: 'light', y: { title: { formatter: pluginChartHTML } } },
}));
</script>
<template>
  <VueApexCharts v-if="valid" type="pie" :height="height" :options="options" :series="series" />
  <p v-else role="alert">插件图表结构无效</p>
</template>
