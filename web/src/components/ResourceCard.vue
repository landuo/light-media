<script setup lang="ts">
import { computed } from 'vue';
import AppIcon from './AppIcon.vue';
import { bytes, date, friendly, label } from '../api';
import {
  candidateKey,
  promotionKind,
  promotionRemaining,
  resourceReviewReasons,
  type Candidate,
} from '../features/resources';
const props = defineProps<{
  showIdentityReview?: boolean;
  candidates: Candidate[];
  names: Record<string, string>;
  downloads: Record<string, string>;
  selected: string[];
}>();
const reviewReasons = computed(() =>
  resourceReviewReasons(props.candidates[0], props.showIdentityReview !== false),
);
defineEmits<{ download: [candidate: Candidate]; select: [candidate: Candidate] }>();
function promotion(r: Record<string, any>) {
  const kind = promotionKind(r);
  return kind === 'free'
    ? 'FREE'
    : kind === 'unknown'
      ? '促销未知'
      : r.download_factor < 1
        ? `${r.download_factor}×下载`
        : r.upload_factor > 1
          ? `${r.upload_factor}×上传`
          : '普通';
}
</script>
<template>
  <article
    class="release-card"
    :class="{ 'is-selected': selected.includes(candidateKey(candidates[0]!)) }"
    :tabindex="candidates[0]!.accepted || candidates[0]!.needs_confirmation ? 0 : -1"
    :aria-label="'下载资源 ' + candidates[0]!.resource.title"
    @click="
      (candidates[0]!.accepted || candidates[0]!.needs_confirmation) &&
      $emit('download', candidates[0]!)
    "
    @keydown.enter.self="
      (candidates[0]!.accepted || candidates[0]!.needs_confirmation) &&
      $emit('download', candidates[0]!)
    "
    @keydown.space.self.prevent="
      (candidates[0]!.accepted || candidates[0]!.needs_confirmation) &&
      $emit('download', candidates[0]!)
    "
  >
    <div class="release-top">
      <label class="check" @click.stop
        ><input
          type="checkbox"
          :checked="selected.includes(candidateKey(candidates[0]!))"
          :disabled="!candidates[0]!.accepted && !candidates[0]!.needs_confirmation"
          @change="$emit('select', candidates[0]!)"
        />{{ names[candidates[0]!.resource.site_id] || candidates[0]!.resource.site_id }}</label
      ><span :class="['promo', promotionKind(candidates[0]!.resource)]">{{
        promotion(candidates[0]!.resource)
      }}</span>
    </div>
    <h3 :title="candidates[0]!.resource.title">{{ candidates[0]!.resource.title }}</h3>
    <p v-if="candidates[0]!.resource.subtitle" class="release-description">
      {{ candidates[0]!.resource.subtitle }}
    </p>
    <div class="tags">
      <span v-if="candidates[0]!.season"
        >S{{ String(candidates[0]!.season).padStart(2, '0')
        }}{{
          candidates[0]!.episodes?.length
            ? ' · E' + candidates[0]!.episodes!.join(', E')
            : ' · 季包'
        }}</span
      ><span v-if="candidates[0]!.resource.resolution" class="quality-resolution">{{
        candidates[0]!.resource.resolution
      }}</span
      ><span v-if="candidates[0]!.resource.codec" class="quality-codec">{{
        candidates[0]!.resource.codec
      }}</span
      ><span v-if="candidates[0]!.resource.source" class="quality-source">{{
        candidates[0]!.resource.source
      }}</span
      ><span v-if="downloads[candidateKey(candidates[0]!)]" class="downloaded">{{
        label(downloads[candidateKey(candidates[0]!)])
      }}</span>
    </div>
    <div class="release-facts">
      <b>{{
        candidates[0]!.resource.size > 0 ? bytes(candidates[0]!.resource.size) : '体积未知'
      }}</b
      ><span class="green"
        >↑
        {{ candidates[0]!.resource.seeders >= 0 ? candidates[0]!.resource.seeders : '未知' }}</span
      ><span class="peers"
        >↓
        {{
          candidates[0]!.resource.leechers >= 0 ? candidates[0]!.resource.leechers : '未知'
        }}</span
      >
    </div>
    <p class="hint">
      <AppIcon name="clock" />{{ date(candidates[0]!.resource.published_at)
      }}<span v-if="candidates[0]!.resource.promotion_end">
        · {{ promotionRemaining(candidates[0]!.resource.promotion_end) }}</span
      >
    </p>
    <details v-if="reviewReasons.length" class="release-review" @click.stop @keydown.stop>
      <summary
        :class="
          showIdentityReview !== false && candidates[0]!.needs_confirmation ? 'warning' : 'hint'
        "
      >
        {{
          showIdentityReview === false
            ? candidates[0]!.accepted
              ? '资源信息'
              : '不符合搜索规则'
            : candidates[0]!.needs_confirmation
              ? '需要核对'
              : candidates[0]!.accepted
                ? '匹配详情'
                : '不符合当前规则'
        }}
      </summary>
      <p>{{ reviewReasons.map(friendly).join('；') }}</p>
    </details>
    <div class="release-actions" @click.stop @keydown.stop>
      <a
        v-if="candidates[0]!.resource.details_url?.startsWith('https://')"
        :href="candidates[0]!.resource.details_url"
        target="_blank"
        rel="noopener noreferrer"
        class="release-link"
        title="站点详情"
        aria-label="站点详情"
        ><AppIcon name="info" /></a
      ><button
        class="small primary"
        :disabled="!candidates[0]!.accepted && !candidates[0]!.needs_confirmation"
        @click="$emit('download', candidates[0]!)"
      >
        <AppIcon name="download" />下载
      </button>
    </div>
    <details v-if="candidates.length > 1" class="more-sources" @click.stop @keydown.stop>
      <summary>其他来源 · {{ candidates.length - 1 }}</summary>
      <div v-for="c in candidates.slice(1)" :key="candidateKey(c)" class="source-row">
        <span
          >{{ names[c.resource.site_id] || c.resource.site_id }} · {{ promotion(c.resource)
          }}<small> ↑ {{ c.resource.seeders >= 0 ? c.resource.seeders : '未知' }}</small></span
        ><button
          class="small"
          :disabled="!c.accepted && !c.needs_confirmation"
          @click="$emit('download', c)"
        >
          下载
        </button>
      </div>
    </details>
  </article>
</template>
<style scoped>
.release-card {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 12px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: var(--surface);
  box-shadow: var(--shadow);
  transition:
    border-color 0.3s,
    transform 0.3s;
  cursor: pointer;
}
.release-card:hover {
  border-color: var(--accent-line);
  transform: translateY(-4px);
}
.release-card.is-selected {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.release-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
  font-weight: 600;
  font-size: 14px;
  padding-right: 66px;
}
.release-top .check {
  min-width: 0;
  min-height: 28px;
  padding: 2px 0;
  color: var(--text);
}
.promo {
  position: absolute;
  top: 0;
  right: 0;
  flex: none;
  padding: 4px 8px;
  border-radius: 0 5px 0 6px;
  background: var(--surface-subtle);
  color: var(--muted);
  font-size: 12px;
  font-weight: 650;
  line-height: 1.4;
  white-space: nowrap;
}
.promo.free {
  background: #4caf50;
  color: white;
}
.promo.discount {
  background: #ff5722;
  color: white;
}
.promo.upload {
  background: #9c27b0;
  color: white;
}
.promo.normal {
  display: none;
}
h3 {
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 500;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.release-description {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 6px 0 8px;
  color: var(--muted);
  font-size: 14px;
  line-height: 1.5;
}
.release-card .tags {
  gap: 4px;
  margin: 0;
}
.release-card .tags span {
  padding: 2px 6px;
  border: 0;
  border-radius: 3px;
  background: #3f51b5;
  color: white;
  font-size: 11px;
  font-weight: 500;
}
.release-card .tags .quality-resolution {
  background: #7b1fa2;
}
.release-card .tags .quality-codec {
  background: #ff9800;
}
.release-card .tags .quality-source {
  background: #8000ff;
}
.release-card .tags .downloaded {
  background: var(--accent-soft);
  color: var(--accent);
}
.release-facts {
  display: flex;
  align-items: baseline;
  gap: 14px;
  margin-top: 12px;
  color: var(--muted);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}
.release-facts b {
  margin-right: auto;
  color: white;
  background: var(--accent);
  font-size: 12px;
  font-weight: 500;
  border-radius: 3px;
  padding: 3px 6px;
}
.release-facts .green {
  color: var(--success);
}
.release-facts .peers {
  color: var(--warning);
}
.release-card .hint {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 8px 0;
  font-size: 12px;
}
.release-card .hint svg {
  width: 16px;
  height: 16px;
}
.release-review {
  margin: 0 0 8px;
  padding: 0;
  border: 0;
  font-size: 12px;
}
.release-review summary {
  padding: 4px 0;
  background: none;
  font-size: 12px;
}
.release-review p {
  margin: 8px 0 0;
  color: var(--muted);
  line-height: 1.7;
}
.release-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid var(--line);
}
.release-link {
  color: var(--accent);
  font-size: 12px;
  text-decoration: none;
  display: inline-flex;
  justify-content: center;
  align-items: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
}
.release-link:hover {
  color: var(--accent);
}
.release-actions .primary {
  min-width: 78px;
  min-height: 33px;
  border-radius: 4px;
}
.more-sources {
  margin: 13px 0 0;
  padding: 12px 0 0;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  font-size: 12px;
}
.more-sources summary {
  color: var(--accent);
  font-size: 12px;
}
.source-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: 10px;
}
.source-row small {
  color: var(--muted);
}
@media (max-width: 520px) {
  .release-card {
    padding: 12px;
  }
}
</style>
