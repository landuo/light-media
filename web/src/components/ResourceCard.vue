<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VDialog } from 'vuetify/components/VDialog';
import { VIcon } from 'vuetify/components/VIcon';
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
  mediaTitle?: string;
  showIdentityReview?: boolean;
  candidates: Candidate[];
  names: Record<string, string>;
  downloads: Record<string, string>;
  selected: string[];
}>();
const first = computed(() => props.candidates[0]!);
const title = computed(() => props.mediaTitle || resourceTitleQuery(first.value.resource.title));
const siteName = computed(() => props.names[first.value.resource.site_id] || first.value.resource.site_id);
const siteIconFailed = ref(false);
watch(() => first.value.resource.site_id, () => { siteIconFailed.value = false; });
const moreSources = ref(false);
const reviewReasons = computed(() =>
  resourceReviewReasons(props.candidates[0], props.showIdentityReview !== false),
);
const emit = defineEmits<{ download: [candidate: Candidate]; select: [candidate: Candidate] }>();
function downloadSource(candidate: Candidate) {
  moreSources.value = false;
  emit('download', candidate);
}
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
      'is-downloaded': downloads[candidateKey(first)],
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
      <span class="site-name" :title="siteName">
        <img
          v-if="!siteIconFailed"
          class="site-avatar"
          :src="`/api/sites/manage/${encodeURIComponent(first.resource.site_id)}/icon`"
          alt=""
          loading="lazy"
          @error="siteIconFailed = true"
        />
        <span v-else class="site-avatar" aria-hidden="true">{{ siteName.slice(0, 1) }}</span>
        {{ siteName }}
      </span>
      <span class="release-counts">
        <span class="green" title="做种人数"><VIcon icon="mdi-arrow-up" size="16" />{{ first.resource.seeders >= 0 ? first.resource.seeders : '未知' }}</span>
        <span v-if="first.resource.leechers !== 0" class="peers" title="下载人数"><VIcon icon="mdi-arrow-down" size="16" />{{ first.resource.leechers >= 0 ? first.resource.leechers : '未知' }}</span>
      </span>
    </div>
    <p class="release-title" :title="first.resource.title">{{ first.resource.title }}</p>
    <p v-if="first.resource.subtitle" class="release-description" :title="first.resource.subtitle">{{ first.resource.subtitle }}</p>
    <p v-if="first.resource.published_at" class="hint"><AppIcon name="clock" />{{ relativeDate(first.resource.published_at) }}</p>
    <div class="tags">
      <span v-if="first.resource.source" class="quality-source">{{ first.resource.source }}</span>
      <span v-if="first.resource.resolution" class="quality-resolution">{{
        first.resource.resolution
      }}</span>
      <span v-if="first.resource.codec" class="quality-codec">{{ first.resource.codec }}</span>
      <span v-if="resourceGroup(first)" class="quality-group">{{ resourceGroup(first) }}</span>
      <span v-for="tag in first.resource.labels || []" :key="tag">{{ tag }}</span>
      <span v-if="first.resource.hit_and_run" class="quality-hr">H&amp;R</span>
      <span v-if="first.resource.promotion_end" class="quality-expiry">{{
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
      <VBtn
        v-if="candidates.length > 1"
        class="more-sources-button"
        prepend-icon="mdi-chevron-down"
        color="primary"
        size="small"
        variant="text"
        @click="moreSources = true"
      >更多来源 ({{ candidates.length - 1 }})</VBtn>
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
  </article>
  <VDialog v-if="moreSources" v-model="moreSources" max-width="25rem" aria-label="其他来源">
    <VCard>
      <VCardTitle class="source-heading">
        <span>其他来源</span>
        <VBtn icon="mdi-close" size="small" variant="text" aria-label="关闭其他来源" @click="moreSources = false" />
      </VCardTitle>
      <VCardText class="source-list">
        <div v-for="candidate in candidates.slice(1)" :key="candidateKey(candidate)" class="source-row">
          <span class="source-summary">
            <strong>{{ names[candidate.resource.site_id] || candidate.resource.site_id }}</strong>
            <small>
              {{ promotion(candidate.resource) }} · {{ candidate.resource.size > 0 ? bytes(candidate.resource.size) : '体积未知' }}
              · <VIcon icon="mdi-arrow-up" size="14" color="success" />{{ candidate.resource.seeders >= 0 ? candidate.resource.seeders : '未知' }}
            </small>
          </span>
          <a
            v-if="candidate.resource.details_url?.startsWith('https://')"
            :href="candidate.resource.details_url"
            target="_blank"
            rel="noopener noreferrer"
            class="release-link"
            aria-label="站点详情"
            title="站点详情"
          ><VIcon icon="mdi-arrow-top-right" size="18" /></a>
          <VBtn
            color="primary"
            size="small"
            variant="text"
            :disabled="!candidate.accepted && !candidate.needs_confirmation"
            @click="downloadSource(candidate)"
          >下载</VBtn>
        </div>
      </VCardText>
    </VCard>
  </VDialog>
</template>
<style scoped>
.site-avatar {
  display: inline-grid;
  place-items: center;
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
  border-radius: 4px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12px;
}
.list-layout.release-card {
  display: grid;
  grid-template-columns: 56px minmax(0, 1fr) auto;
  gap: 4px 12px;
  padding: 12px;
  background: transparent;
  border-radius: 8px;
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
  grid-row: 1 / span 4;
  flex-direction: column;
  justify-content: center;
  width: 56px;
  margin: 0;
  text-align: center;
  overflow-wrap: anywhere;
}
.list-layout .site-avatar {
  width: 32px;
  height: 32px;
  font-size: 14px;
}
.list-layout .release-counts {
  grid-column: 3;
  grid-row: 1;
  justify-self: end;
  margin-top: 22px;
}
.list-layout .release-actions {
  grid-column: 3;
  grid-row: 2 / span 4;
  align-self: start;
  min-height: 0;
  border: 0;
  margin: 0;
  padding: 0;
}
.list-layout .selection {
  position: absolute;
  left: 12px;
  top: 8px;
}
.list-layout:hover {
  transform: none;
  border-color: var(--accent-line);
}
@media (max-width: 600px) {
  .list-layout.release-card {
    grid-template-columns: 48px minmax(0, 1fr);
  }
  .list-layout .site-name {
    width: 48px;
  }
  .list-layout .promo:not(.normal, .unknown) ~ .release-heading {
    padding-inline-end: 48px;
  }
  .list-layout .release-actions,
  .list-layout .release-counts {
    grid-column: 2;
    grid-row: auto;
    margin-top: 0;
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
  border-color: var(--accent-line);
}
.release-card.is-selected {
  border-color: var(--accent);
}
.release-card.is-downloaded:not(.is-selected) {
  border-color: var(--success);
}
.list-layout.is-downloaded:not(.is-selected) {
  border-inline-start-width: 3px;
  padding-inline-start: 10px;
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
  background: #4caf50;
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
  flex-wrap: wrap;
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
.release-counts,
.release-counts > span {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}
.release-counts { gap: 8px; }
.release-counts > span { gap: 2px; font-weight: 600; }
.site-name {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-right: auto;
  font-size: 14px;
  font-weight: 600;
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
  background: #5c6bc0;
  color: #fff;
  font-size: 10px;
  line-height: 16px;
}
.release-card .tags .quality-source {
  background: #f44336;
}
.release-card .tags .quality-resolution {
  background: #7b1fa2;
}
.release-card .tags .quality-codec {
  background: #ff9800;
}
.release-card .tags .quality-group {
  background: #00897b;
}
.release-card .tags .quality-hr { background: #212121; }
.release-card .tags .quality-expiry { background: #7e57c2; }
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
.more-sources-button { margin-inline-end: auto; letter-spacing: 0; }
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
.source-heading { display: flex; align-items: center; justify-content: space-between; }
.source-list { max-height: 60vh; overflow-y: auto; padding-block: 0; }
.source-row {
  display: flex;
  align-items: center;
  gap: 4px;
  padding-block: 8px;
}
.source-row + .source-row { border-top: 1px solid var(--line); }
.source-summary { display: grid; min-width: 0; flex: 1; overflow-wrap: anywhere; }
.source-summary small { color: var(--muted); }
@media (hover: none) {
  .selection {
    display: block;
  }
}
</style>
