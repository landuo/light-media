<script setup lang="ts">
import { VSelect } from 'vuetify/components/VSelect';
import { VTextField } from 'vuetify/components/VTextField';
import { action, api, date } from '../api';
import { ref } from 'vue';
const props = defineProps<{ model: any }>();
const preview = ref<any>(null);
const days = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
function day(i: number) {
  const p = props.model.weekdays.indexOf(i);
  if (p < 0) props.model.weekdays.push(i);
  else props.model.weekdays.splice(p, 1);
}
async function check() {
  preview.value = await action(() => api('/schedule/preview', { schedule: props.model }));
}
</script>
<template>
  <div class="form-grid">
    <VSelect
      v-model="model.source"
      label="更新时间来源"
      :items="[
        { title: '元数据日期', value: 'metadata' },
        { title: '自定义周期', value: 'custom' },
      ]"
    /><VSelect
      v-model="model.mode"
      label="调度模式"
      :items="[
        { title: '跟随更新窗口', value: 'follow' },
        { title: '固定间隔', value: 'fixed' },
        { title: '仅手动检查', value: 'manual' },
      ]"
    /><VSelect
      v-model="model.rule"
      label="周期规则"
      :items="[
        { title: '每周', value: 'weekly' },
        { title: '每天', value: 'daily' },
        { title: '每隔 N 天', value: 'every_n_days' },
      ]"
    /><VTextField v-model="model.time" type="time" label="更新时间" /><VTextField
      v-model="model.start_date"
      type="date"
      label="起始日期"
    /><VTextField v-model="model.timezone" placeholder="Asia/Shanghai" label="时区" /><VTextField
      v-if="model.rule === 'every_n_days'"
      v-model.number="model.every_days"
      type="number"
      min="1"
      label="间隔天数"
    />
  </div>
  <div v-if="model.rule === 'weekly'" class="weekdays">
    <button
      v-for="(d, i) in days"
      :key="d"
      :class="['small', { selected: model.weekdays.includes(i) }]"
      @click="day(i)"
    >
      {{ d }}
    </button>
  </div>
  <div class="form-grid">
    <VTextField
      v-model.number="model.interval_minutes"
      type="number"
      min="5"
      label="固定检查间隔（分钟）"
    /><VTextField
      v-model.number="model.wait_minutes"
      type="number"
      min="0"
      label="更新后等待（分钟）"
    /><VTextField
      v-model.number="model.window_hours"
      type="number"
      min="1"
      label="活跃窗口（小时）"
    /><VTextField
      v-model.number="model.active_minutes"
      type="number"
      min="5"
      label="窗口内检查（分钟）"
    /><VTextField
      v-model.number="model.fallback_minutes"
      type="number"
      min="30"
      label="窗口外兜底（分钟）"
    />
  </div>
  <p class="hint">
    跟随模式保留窗口外的低频兜底检查；到更新窗口后提高检查频率。日期预览来自服务端调度器。
  </p>
  <div class="section-heading">
    <h3>例外日期</h3>
    <button
      class="small"
      @click="model.exceptions.push({ date: '', action: 'skip', target_date: '', episode: 0 })"
    >
      ＋ 添加
    </button>
  </div>
  <div v-for="(ex, i) in model.exceptions" :key="i" class="inline-form">
    <VTextField v-model="ex.date" type="date" label="例外日期" density="compact" /><VSelect
      v-model="ex.action"
      density="compact"
      aria-label="例外安排"
      :items="[
        { title: '跳过更新', value: 'skip' },
        { title: '改期', value: 'postpone' },
        { title: '额外更新', value: 'extra' },
      ]"
    /><VTextField
      v-if="ex.action === 'postpone'"
      v-model="ex.target_date"
      type="date"
      label="改期至"
      density="compact"
    /><VTextField
      label="集数，0 表示全部"
      density="compact"
      v-model.number="ex.episode"
      type="number"
      min="0"
      placeholder="集数，0 表示全部"
    /><button class="small danger" @click="model.exceptions.splice(i, 1)">移除</button>
  </div>
  <button class="small" @click="check">预览接下来更新时间</button>
  <div v-if="preview" class="notice">
    <p>{{ preview.note }}</p>
    <strong>接下来的检查</strong>
    <div v-for="t in preview.next_checks" :key="t">{{ date(t) }}</div>
    <strong v-if="preview.expected_updates?.length">预计更新日期</strong>
    <div v-for="u in preview.expected_updates" :key="u.date">
      {{ u.date }} ·
      {{
        u.state === 'skipped'
          ? '本次跳过'
          : u.state === 'postponed'
            ? '已延期'
            : '周期预测，集号待确认'
      }}
    </div>
  </div>
</template>
