<script setup lang="ts">
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
    <label>包含<input v-model="model.include" placeholder="留空不限制" /></label>
    <label>排除<input v-model="model.exclude" placeholder="留空不限制" /></label>
  </div>
  <div class="form-grid">
    <label>最低做种数<input v-model.number="model.min_seeders" type="number" min="0" /></label
    ><label v-if="!advanced"
      >分辨率（硬性要求）<select v-model="model.resolution">
        <option value="">不限</option>
        <option>2160p</option>
        <option>1080p</option>
        <option>720p</option>
      </select></label
    ><label
      >编码<select v-model="model.codec">
        <option value="">不限</option>
        <option>HEVC</option>
        <option>AVC</option>
        <option>AV1</option>
      </select></label
    ><label v-if="!advanced"
      >资源来源<select v-model="model.source">
        <option value="">不限</option>
        <option value="WEB-DL">WEB-DL</option>
        <option value="WEBRip">WEBRip</option>
        <option value="BluRay">BluRay</option>
        <option value="BDRip">BDRip</option>
        <option value="HDTV">HDTV</option>
        <option value="REMUX">REMUX</option>
        <option value="DVD">DVD</option>
      </select></label
    ><label v-if="!advanced"
      >动态范围<select v-model="model.hdr">
        <option value="">不限</option>
        <option value="sdr">SDR</option>
        <option value="hdr10">HDR10</option>
        <option value="hdr10plus">HDR10+</option>
        <option value="dolby_vision">Dolby Vision</option>
      </select></label
    ><label class="check"><input v-model="model.free_only" type="checkbox" />仅免费资源</label
    ><label
      >音轨语言（逗号分隔）<input
        :value="(model.languages || []).join(', ')"
        @input="csv('languages', $event)"
        placeholder="如 zh, en" /></label
    ><label
      >字幕（逗号分隔）<input
        :value="(model.subtitles || []).join(', ')"
        @input="csv('subtitles', $event)"
        placeholder="如 zh-Hans, en" /></label
    ><label
      >最小体积（GiB）<input
        :value="model.min_bytes ? model.min_bytes / 1073741824 : ''"
        @input="size('min_bytes', $event)"
        type="number"
        min="0"
        step="0.1"
        placeholder="不限制" /></label
    ><label
      >最大体积（GiB）<input
        :value="model.max_bytes ? model.max_bytes / 1073741824 : ''"
        @input="size('max_bytes', $event)"
        type="number"
        min="0"
        step="0.1"
        placeholder="不限制"
    /></label>
  </div>
  <p class="hint">硬性要求只有明确识别后才满足；资源缺少对应信息时会显示筛除原因。</p>
  <div class="form-grid">
    <label
      >优先发布组（按顺序，逗号分隔）<input
        :value="(model.preferred_groups || []).join(', ')"
        @input="csv('preferred_groups', $event)"
        placeholder="如 HHWEB, ANi" /></label
    ><label
      >优先分辨率（按顺序，逗号分隔）<input
        :value="(model.preferred_resolutions || []).join(', ')"
        @input="csv('preferred_resolutions', $event)"
        placeholder="如 2160p, 1080p" /></label
    ><label v-if="!advanced"
      >必须匹配的标题正则<input v-model="model.include" placeholder="留空不限制" /></label
    ><label v-if="!advanced"
      >排除的标题正则<input v-model="model.exclude" placeholder="留空不限制"
    /></label>
  </div>
  <p class="hint">偏好影响合格资源的排序，不会触发已入库作品自动升级。</p>
</template>
