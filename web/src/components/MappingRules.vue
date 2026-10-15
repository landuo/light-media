<script setup lang="ts">
const props = defineProps<{ model: any[]; mediaId: string; season: number }>();
function add() {
  props.model.push({
    id: 'map-' + Date.now(),
    revision: 1,
    media_id: props.mediaId,
    target_season: props.season,
    from: 1,
    to: 1,
    offset: 0,
    source: { site: '', group: '', form: 'absolute_episode' },
    confirmed: false,
  });
}
</script>
<template>
  <div class="section-heading">
    <h3>原始集号 → 标准季集</h3>
    <button class="small" @click="add">＋ 添加偏移映射</button>
  </div>
  <article v-for="(m, i) in model" :key="m.id" class="mapping-rule">
    <div class="form-grid">
      <label>原始起始集<input v-model.number="m.from" type="number" min="1" /></label
      ><label>原始结束集<input v-model.number="m.to" type="number" min="1" /></label
      ><label>标准目标季<input v-model.number="m.target_season" type="number" min="1" /></label
      ><label v-if="m.offset !== undefined && m.offset !== null"
        >集号偏移（标准 = 原始 + 偏移）<input v-model.number="m.offset" type="number"
      /></label>
    </div>
    <div class="form-grid">
      <label>限定来源站点（留空不限）<input v-model="m.source.site" /></label
      ><label>限定发布组（留空不限）<input v-model="m.source.group" /></label
      ><label
        >原始编号形式<select v-model="m.source.form">
          <option value="absolute_episode">绝对集号 EPxxx / 第xxx集</option>
          <option value="season_episode">标准形式 SxxExx</option>
          <option value="">不限</option>
        </select></label
      >
    </div>
    <p class="hint" v-if="m.offset !== undefined && m.offset !== null">
      示例：原始 {{ m.from }} → S{{ m.target_season }}E{{ Number(m.from) + Number(m.offset) }}；原始
      {{ m.to }} → S{{ m.target_season }}E{{ Number(m.to) + Number(m.offset) }}。
    </p>
    <div class="button-row">
      <label class="check"
        ><input v-model="m.confirmed" type="checkbox" />我已核对这条映射与标准元数据一致</label
      ><button class="small danger" @click="model.splice(i, 1)">移除规则</button>
    </div>
  </article>
  <p v-if="!model.length" class="hint">
    没有映射规则。标准 SxxExx 可直接识别；绝对集号保持待确认，直到有明确映射。
  </p>
</template>
