<script setup lang="ts">
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
    <label
      >更新时间来源<select v-model="model.source">
        <option value="metadata">元数据日期</option>
        <option value="custom">自定义周期</option>
      </select></label
    ><label
      >调度模式<select v-model="model.mode">
        <option value="follow">跟随更新窗口</option>
        <option value="fixed">固定间隔</option>
        <option value="manual">仅手动检查</option>
      </select></label
    ><label
      >周期规则<select v-model="model.rule">
        <option value="weekly">每周</option>
        <option value="daily">每天</option>
        <option value="every_n_days">每隔 N 天</option>
      </select></label
    ><label>更新时间<input v-model="model.time" type="time" /></label
    ><label>起始日期<input v-model="model.start_date" type="date" /></label
    ><label>时区<input v-model="model.timezone" placeholder="Asia/Shanghai" /></label
    ><label v-if="model.rule === 'every_n_days'"
      >间隔天数<input v-model.number="model.every_days" type="number" min="1"
    /></label>
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
    <label
      >固定检查间隔（分钟）<input
        v-model.number="model.interval_minutes"
        type="number"
        min="5" /></label
    ><label
      >更新后等待（分钟）<input v-model.number="model.wait_minutes" type="number" min="0" /></label
    ><label
      >活跃窗口（小时）<input v-model.number="model.window_hours" type="number" min="1" /></label
    ><label
      >窗口内检查（分钟）<input
        v-model.number="model.active_minutes"
        type="number"
        min="5" /></label
    ><label
      >窗口外兜底（分钟）<input v-model.number="model.fallback_minutes" type="number" min="30"
    /></label>
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
    <input v-model="ex.date" type="date" /><select v-model="ex.action">
      <option value="skip">跳过更新</option>
      <option value="postpone">改期</option>
      <option value="extra">额外更新</option></select
    ><input v-if="ex.action === 'postpone'" v-model="ex.target_date" type="date" /><input
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
