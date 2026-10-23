<script setup lang="ts">
import { computed } from 'vue';
import AppIcon from './AppIcon.vue';
import { resourceTitleQuery } from '../features/keyword-search';
import { bytes, date, friendly, label } from '../api';
import {
  candidateKey,
  promotionKind,
  promotionRemaining,
  resourceReviewReasons,
  resourceGroup,
  type Candidate,
} from '../features/resources';
const props = defineProps<{
  layout?: string;
  showIdentityReview?: boolean;
  candidates: Candidate[];
  names: Record<string, string>;
  downloads: Record<string, string>;
  selected: string[];
}>();
const first = computed(() => props.candidates[0]!);
const title = computed(() => resourceTitleQuery(first.value.resource.title));
const reviewReasons = computed(() =>
  resourceReviewReasons(props.candidates[0], props.showIdentityReview !== false),
);
defineEmits<{ download: [candidate: Candidate]; select: [candidate: Candidate] }>();
function promotion(r: Record<string, any>) {
  const kind = promotionKind(r);
  return kind === 'free'
    ? '免费'
    : kind === 'unknown'
      ? '促销未知'
      : r.download_factor < 1
        ? `${Math.round(r.download_factor * 100)}%`
        : r.upload_factor > 1
          ? `${r.upload_factor}×上传`
          : '普通';
}
function relativeDate(value: string) {
  const seconds = (Date.now() - Date.parse(value)) / 1000;
  if (!Number.isFinite(seconds) || seconds < 0) return date(value);
  if (seconds < 60) return '刚刚';
  if (seconds < 3600) return Math.floor(seconds / 60) + ' 分钟前';
  if (seconds < 86400) return Math.floor(seconds / 3600) + ' 小时前';
  if (seconds < 2592000) return Math.floor(seconds / 86400) + ' 天前';
  return Math.floor(seconds / 2592000) + ' 个月前';
}
</script>
<template>
  <article
    class="release-card"
    :class="{
      'is-selected': selected.includes(candidateKey(candidates[0]!)),
      'list-layout': layout === 'list',
    }"
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
    <span :class="['promo', promotionKind(first.resource)]">{{ promotion(first.resource) }}</span>
    <div class="release-heading">
      <h3 :title="title">{{ title }}</h3>
      <span v-if="first.season" class="season-tag"
        >S{{ String(first.season).padStart(2, '0')
        }}<template v-if="first.episodes?.length">
          E{{ first.episodes.map((e) => String(e).padStart(2, '0')).join(',') }}</template
        ></span
      >
    </div>
    <div class="release-site-line">
      <label class="selection" @click.stop
        ><input
          type="checkbox"
          :aria-label="'选择 ' + first.resource.title"
          :checked="selected.includes(candidateKey(first))"
          :disabled="!first.accepted && !first.needs_confirmation"
          @change="$emit('select', first)"
      /></label>
      <span class="site-name"
        ><span class="site-avatar">{{
          (names[first.resource.site_id] || first.resource.site_id).slice(0, 1)
        }}</span
        >{{ names[first.resource.site_id] || first.resource.site_id }}</span
      >
      <span class="green"
        >↑ {{ first.resource.seeders >= 0 ? first.resource.seeders : '未知' }}</span
      >
      <span v-if="first.resource.leechers !== 0" class="peers"
        >↓ {{ first.resource.leechers >= 0 ? first.resource.leechers : '未知' }}</span
      >
    </div>
    <p class="release-title">{{ first.resource.title }}</p>
    <p v-if="first.resource.subtitle" class="release-description">{{ first.resource.subtitle }}</p>
    <p class="hint"><AppIcon name="clock" />{{ relativeDate(first.resource.published_at) }}</p>
    <div class="tags">
      <span v-if="first.resource.source" class="quality-source">{{ first.resource.source }}</span>
      <span v-if="first.resource.resolution" class="quality-resolution">{{
        first.resource.resolution
      }}</span>
      <span v-if="first.resource.codec" class="quality-codec">{{ first.resource.codec }}</span>
      <span v-if="resourceGroup(first)" class="quality-group">{{ resourceGroup(first) }}</span>
      <span v-for="tag in first.resource.labels || []" :key="tag">{{ tag }}</span>
      <span v-if="first.resource.promotion_end">{{
        promotionRemaining(first.resource.promotion_end)
      }}</span>
      <span v-if="downloads[candidateKey(first)]" class="downloaded">{{
        label(downloads[candidateKey(first)])
      }}</span>
    </div>
    <details v-if="reviewReasons.length" class="release-review" @click.stop @keydown.stop>
      <summary :class="first.needs_confirmation ? 'warning' : 'hint'">
        {{ first.accepted ? '匹配详情' : first.needs_confirmation ? '需要核对' : '不符合当前规则' }}
      </summary>
      <p>{{ reviewReasons.map(friendly).join('；') }}</p>
    </details>
    <div class="release-actions" @click.stop @keydown.stop>
      <span class="release-size">{{
        first.resource.size > 0 ? bytes(first.resource.size) : '体积未知'
      }}</span>
      <a
        v-if="first.resource.details_url?.startsWith('https://')"
        :href="first.resource.details_url"
        target="_blank"
        rel="noopener noreferrer"
        class="release-link"
        title="站点详情"
        aria-label="站点详情"
        ><AppIcon name="info"
      /></a>
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
.site-avatar {
  display: inline-grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 4px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12px;
}
.list-layout.release-card {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr) 150px;
  gap: 4px 12px;
  padding: 16px;
}
.list-layout .release-heading {
  grid-column: 2;
  margin: 0;
}
.list-layout .release-title,
.list-layout .release-description,
.list-layout .hint,
.list-layout .tags,
.list-layout .release-review {
  grid-column: 2;
  margin: 0;
}
.list-layout .release-site-line {
  display: contents;
}
.list-layout .site-name {
  grid-column: 1;
  grid-row: 1 / 6;
  flex-direction: column;
  justify-content: center;
  width: 72px;
}
.list-layout .site-avatar {
  width: 40px;
  height: 40px;
  font-size: 20px;
}
.list-layout .green {
  position: absolute;
  right: 80px;
  top: 42px;
}
.list-layout .peers {
  position: absolute;
  right: 20px;
  top: 42px;
}
.list-layout .release-actions {
  grid-column: 3;
  grid-row: 2 / 6;
  align-self: end;
  border: 0;
  margin: 0;
  padding: 0;
}
.list-layout .selection {
  position: absolute;
  left: 12px;
  top: 8px;
}
.list-layout .release-heading h3 {
  font-size: 16px;
}
.list-layout:hover {
  transform: none;
  border-color: var(--accent-line);
}
@media (max-width: 650px) {
  .list-layout.release-card {
    grid-template-columns: 48px minmax(0, 1fr);
  }
  .list-layout .site-name {
    width: 48px;
  }
  .list-layout .release-actions {
    grid-column: 2;
    grid-row: auto;
  }
  .list-layout .green,
  .list-layout .peers {
    display: none;
  }
}

.release-card {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 16px 12px 0;
  border: 1px solid transparent;
  border-radius: 12px;
  background: var(--surface);
  cursor: pointer;
  transition: transform 0.3s;
  overflow: hidden;
}
.release-card:hover {
  transform: translateY(-4px);
}
.release-card.is-selected {
  border-color: var(--accent);
}
.promo {
  position: absolute;
  top: 0;
  right: 0;
  padding: 4px 8px;
  border-radius: 0 0 0 6px;
  color: white;
  font-weight: 600;
  font-size: 14px;
}
.promo.free {
  background: #8bc34a;
}
.promo.discount {
  background: #ff5722;
}
.promo.upload {
  background: #9c27b0;
}
.promo.normal,
.promo.unknown {
  display: none;
}
.release-heading {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 56px 12px 8px;
  min-height: 28px;
}
h3 {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
  line-height: 28px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.season-tag {
  flex: none;
  padding: 4px 8px;
  border-radius: 2px;
  background: #3f51b5;
  color: white;
  font-size: 12px;
  font-weight: 600;
}
.release-site-line {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--muted);
}
.site-name {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-right: auto;
  font-size: 14px;
}
.site-name svg {
  width: 20px;
  height: 20px;
  color: var(--accent);
}
.green {
  color: var(--success);
}
.peers {
  color: var(--warning);
}
.selection {
  display: none;
}
.release-card:hover .selection,
.release-card:focus-within .selection,
.is-selected .selection {
  display: block;
}
.selection input {
  width: 16px;
  height: 16px;
}
.release-title {
  margin: 4px 0;
  font-size: 14px;
  line-height: 21px;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.release-description {
  margin: 4px 0;
  color: var(--muted);
  font-size: 14px;
  line-height: 20px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.release-card .hint {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 6px 0;
  color: var(--muted);
  font-size: 14px;
}
.hint svg {
  width: 16px;
  height: 16px;
}
.release-card .tags {
  gap: 4px;
  margin: 2px 0 20px;
}
.release-card .tags span {
  padding: 2px 6px;
  border: 0;
  border-radius: 2px;
  background: #7360b5;
  color: #fff;
  font-size: 10px;
  line-height: 16px;
}
.release-card .tags .quality-source {
  background: #e95150;
}
.release-card .tags .quality-resolution {
  background: #7b1fa2;
}
.release-card .tags .quality-codec {
  background: #f5ab49;
}
.release-card .tags .quality-group {
  background: #4c9693;
}
.release-review {
  margin: -8px 0 12px;
  padding: 0;
  border: 0;
  font-size: 12px;
}
.release-review summary {
  padding: 4px 0;
  background: none;
}
.release-review p {
  margin: 8px 0 0;
  color: var(--muted);
}
.release-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin: auto -12px 0;
  padding: 10px 12px;
  min-height: 48px;
  border-top: 1px solid var(--line);
}
.release-size {
  color: white;
  background: var(--accent);
  border-radius: 2px;
  padding: 2px 8px;
  font-size: 10px;
}
.release-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: var(--accent);
}
.release-link svg {
  width: 18px;
  height: 18px;
}
.more-sources {
  margin: 0 -12px;
  padding: 12px;
  border: 0;
  border-top: 1px solid var(--line);
  font-size: 12px;
}
.source-row {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  margin-top: 10px;
}
@media (hover: none) {
  .selection {
    display: block;
  }
}
</style>
