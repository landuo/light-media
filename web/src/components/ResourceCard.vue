<script setup lang="ts">
import { bytes, date, friendly, label } from '../api';
import {
  candidateKey,
  promotionKind,
  promotionRemaining,
  type Candidate,
} from '../features/resources';
defineProps<{
  candidates: Candidate[];
  names: Record<string, string>;
  downloads: Record<string, string>;
  selected: string[];
}>();
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
  <article class="release-card">
    <div class="release-top">
      <label class="check"
        ><input
          type="checkbox"
          :checked="selected.includes(candidateKey(candidates[0]!))"
          :disabled="!candidates[0]!.accepted && !candidates[0]!.needs_confirmation"
          @change="$emit('select', candidates[0]!)"
        />{{ names[candidates[0]!.resource.site_id] || candidates[0]!.resource.site_id }}</label
      ><span :class="['promo', { free: promotionKind(candidates[0]!.resource) === 'free' }]">{{
        promotion(candidates[0]!.resource)
      }}</span>
    </div>
    <h3 :title="candidates[0]!.resource.title">{{ candidates[0]!.resource.title }}</h3>
    <p class="release-description">{{ candidates[0]!.resource.subtitle || '暂无副标题' }}</p>
    <div class="tags">
      <span v-if="candidates[0]!.season"
        >S{{ String(candidates[0]!.season).padStart(2, '0')
        }}{{
          candidates[0]!.episodes?.length
            ? ' · E' + candidates[0]!.episodes!.join(', E')
            : ' · 季包'
        }}</span
      ><span
        v-for="v in [
          candidates[0]!.resource.resolution,
          candidates[0]!.resource.codec,
          candidates[0]!.resource.source,
        ].filter(Boolean)"
        :key="v"
        >{{ v }}</span
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
      ><span
        >↓
        {{
          candidates[0]!.resource.leechers >= 0 ? candidates[0]!.resource.leechers : '未知'
        }}</span
      >
    </div>
    <p class="hint">
      {{ date(candidates[0]!.resource.published_at)
      }}<span v-if="candidates[0]!.resource.promotion_end">
        · {{ promotionRemaining(candidates[0]!.resource.promotion_end) }}</span
      >
    </p>
    <details v-if="candidates[0]!.reasons?.length" class="release-review">
      <summary :class="candidates[0]!.needs_confirmation ? 'warning' : 'hint'">
        {{
          candidates[0]!.needs_confirmation
            ? '需要核对'
            : candidates[0]!.accepted
              ? '匹配详情'
              : '不符合当前规则'
        }}
      </summary>
      <p>{{ candidates[0]!.reasons!.map(friendly).join('；') }}</p>
    </details>
    <div class="release-actions">
      <a
        v-if="candidates[0]!.resource.details_url?.startsWith('https://')"
        :href="candidates[0]!.resource.details_url"
        target="_blank"
        rel="noopener noreferrer"
        class="release-link"
        >站点详情 ↗</a
      ><button
        class="small primary"
        :disabled="!candidates[0]!.accepted && !candidates[0]!.needs_confirmation"
        @click="$emit('download', candidates[0]!)"
      >
        ↓ 下载
      </button>
    </div>
    <details v-if="candidates.length > 1" class="more-sources">
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
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border, #2a354a);
  border-radius: 12px;
  padding: 20px;
  background: var(--surface, #171f2e);
  min-width: 0;
  box-shadow: 0 2px 6px #18233804;
  transition:
    border-color 0.15s,
    box-shadow 0.15s;
}
.release-card:hover {
  border-color: #c7cce0;
  box-shadow: 0 5px 16px #18233808;
}
.release-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
  font-weight: 600;
  font-size: 13px;
}
.promo {
  background: #fff3df;
  color: #8c5c15;
  padding: 3px 8px;
  border-radius: 5px;
  font-size: 11px;
  white-space: nowrap;
}
.promo.free {
  background: #e7f7ee;
  color: #24794e;
}
h3 {
  font-size: 14px;
  line-height: 1.65;
  overflow-wrap: anywhere;
  margin: 0;
  color: #26344b;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.release-description {
  font-size: 12px;
  color: var(--muted, #9aa8be);
  line-height: 1.7;
  margin: 8px 0 12px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.release-facts {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 12px;
  margin-top: 14px;
}
.release-facts b {
  margin-right: auto;
  font-size: 16px;
  color: #26344b;
}
.release-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid var(--line);
}
.release-link {
  font-size: 12px;
  color: var(--muted, #a8b8d3);
}
.more-sources {
  margin-top: 12px;
  border-top: 1px solid var(--border, #2a354a);
  padding-top: 10px;
  font-size: 12px;
}
.source-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  margin-top: 8px;
}
.source-row small {
  color: var(--muted);
}
.release-review {
  font-size: 12px;
  margin: 4px 0 14px;
  padding: 0;
  border: 0;
}
.release-review summary {
  font-size: 12px;
  padding: 5px 0;
  background: none;
}
.release-review p {
  margin: 8px 0 0;
  color: var(--muted);
}
.more-sources {
  margin-bottom: 0;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  padding: 12px 0 0;
}
.more-sources summary {
  font-size: 12px;
}
.release-card .hint {
  font-size: 11px;
  margin: 10px 0;
}
.release-card .tags {
  margin: 0;
  gap: 5px;
}
.release-top label {
  color: #536078;
}
.downloaded {
  color: #167448;
}
</style>
