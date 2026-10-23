<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { VSelect } from 'vuetify/components/VSelect';
const props = defineProps<{ model: any; advanced?: boolean }>();
function csv(field: string, e: Event) {
  props.model[field] = (e.target as HTMLInputElement).value
    .split(/[,，]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
function size(field: string, e: Event) {
  const n = Number((e.target as HTMLInputElement).value);
  props.model[field] = Number.isFinite(n) && n > 0 ? Math.round(n * 1073741824) : 0;
}
</script>
<template>
  <div v-if="advanced" class="form-grid">
    <VTextField v-model="model.include" placeholder="留空不限制" label="包含" />
    <VTextField v-model="model.exclude" placeholder="留空不限制" label="排除" />
  </div>
  <div class="form-grid">
    <VTextField
      v-model.number="model.min_seeders"
      type="number"
      min="0"
      label="最低做种数"
    /><VSelect
      v-if="!advanced"
      v-model="model.resolution"
      label="分辨率（硬性要求）"
      :items="[
        { title: '不限', value: '' },
        { title: '2160p', value: '2160p' },
        { title: '1080p', value: '1080p' },
        { title: '720p', value: '720p' },
      ]"
    /><VSelect
      v-model="model.codec"
      label="编码"
      :items="[
        { title: '不限', value: '' },
        { title: 'HEVC', value: 'HEVC' },
        { title: 'AVC', value: 'AVC' },
        { title: 'AV1', value: 'AV1' },
      ]"
    /><VSelect
      v-if="!advanced"
      v-model="model.source"
      label="资源来源"
      :items="[
        { title: '不限', value: '' },
        { title: 'WEB-DL', value: 'WEB-DL' },
        { title: 'WEBRip', value: 'WEBRip' },
        { title: 'BluRay', value: 'BluRay' },
        { title: 'BDRip', value: 'BDRip' },
        { title: 'HDTV', value: 'HDTV' },
        { title: 'REMUX', value: 'REMUX' },
        { title: 'DVD', value: 'DVD' },
      ]"
    /><VSelect
      v-if="!advanced"
      v-model="model.hdr"
      label="动态范围"
      :items="[
        { title: '不限', value: '' },
        { title: 'SDR', value: 'sdr' },
        { title: 'HDR10', value: 'hdr10' },
        { title: 'HDR10+', value: 'hdr10plus' },
        { title: 'Dolby Vision', value: 'dolby_vision' },
      ]"
    /><label class="check"><input v-model="model.free_only" type="checkbox" />仅免费资源</label
    ><VTextField
      :model-value="(model.languages || []).join(', ')"
      @input="csv('languages', $event)"
      placeholder="如 zh, en"
      label="音轨语言（逗号分隔）"
    /><VTextField
      :model-value="(model.subtitles || []).join(', ')"
      @input="csv('subtitles', $event)"
      placeholder="如 zh-Hans, en"
      label="字幕（逗号分隔）"
    /><VTextField
      :model-value="model.min_bytes ? model.min_bytes / 1073741824 : ''"
      @input="size('min_bytes', $event)"
      type="number"
      min="0"
      step="0.1"
      placeholder="不限制"
      label="最小体积（GiB）"
    /><VTextField
      :model-value="model.max_bytes ? model.max_bytes / 1073741824 : ''"
      @input="size('max_bytes', $event)"
      type="number"
      min="0"
      step="0.1"
      placeholder="不限制"
      label="最大体积（GiB）"
    />
  </div>
  <p class="hint">硬性要求只有明确识别后才满足；资源缺少对应信息时会显示筛除原因。</p>
  <div class="form-grid">
    <VTextField
      :model-value="(model.preferred_groups || []).join(', ')"
      @input="csv('preferred_groups', $event)"
      placeholder="如 HHWEB, ANi"
      label="优先发布组（按顺序，逗号分隔）"
    /><VTextField
      :model-value="(model.preferred_resolutions || []).join(', ')"
      @input="csv('preferred_resolutions', $event)"
      placeholder="如 2160p, 1080p"
      label="优先分辨率（按顺序，逗号分隔）"
    /><VTextField
      v-if="!advanced"
      v-model="model.include"
      placeholder="留空不限制"
      label="必须匹配的标题正则"
    /><VTextField
      v-if="!advanced"
      v-model="model.exclude"
      placeholder="留空不限制"
      label="排除的标题正则"
    />
  </div>
  <p class="hint">偏好影响合格资源的排序，不会触发已入库作品自动升级。</p>
</template>
