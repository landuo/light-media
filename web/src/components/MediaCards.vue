<script setup lang="ts">
import { go, image, label } from '../api';
defineProps<{ items: any[]; quickActions?: boolean }>();
function detail(m: any) {
  go('detail/' + m.media_type + '/' + m.id);
}
</script>
<template>
  <div class="media-grid">
    <article v-for="m in items" :key="m.media_type + ':' + m.id" class="media-card">
      <button class="poster poster-action" @click="detail(m)" :aria-label="'查看' + m.title">
        <img
          v-if="image(m.poster_path)"
          :src="image(m.poster_path)"
          :alt="m.title"
          loading="lazy"
        />
        <div v-else class="poster-empty">◈<small>暂无海报</small></div>
        <span v-if="m.vote_average > 0" class="rating">★ {{ m.vote_average.toFixed(1) }}</span>
        <span class="poster-kind">{{ label(m.media_type) }}</span>
        <div class="media-state">
          <span v-if="m.library_state === 'present' || m.library_state === 'managed'">已入库</span>
          <span v-else-if="m.library_state">{{ label(m.library_state) }}</span>
          <span v-if="m.subscription_id">已订阅</span>
          <span v-if="m.download_state">{{ label(m.download_state) }}</span>
        </div>
      </button>
      <button class="media-name" @click="detail(m)">
        <strong>{{ m.title }}</strong>
      </button>
      <p>
        {{ m.year || '日期待定' }} · {{ label(m.media_type)
        }}<span v-if="m.character"> · {{ m.character }}</span>
      </p>
      <div v-if="quickActions" class="media-actions">
        <button class="small" @click="go('resources/' + m.media_type + '/' + m.id)">
          <svg
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            aria-hidden="true"
          >
            <circle cx="8.5" cy="8.5" r="5.5" />
            <path d="m13 13 4 4" /></svg
          >资源
        </button>
        <button
          class="small"
          @click="
            go(
              m.subscription_id
                ? 'subscriptions'
                : 'subscriptions/new/' + m.media_type + '/' + m.id,
            )
          "
        >
          {{ m.subscription_id ? '查看订阅' : '＋ 订阅' }}
        </button>
      </div>
    </article>
  </div>
</template>
<style scoped>
.media-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 28px 20px;
}
.media-card {
  min-width: 0;
  text-align: left;
}
.poster-action {
  padding: 0;
  border: 0;
  width: 100%;
  display: block;
  aspect-ratio: 2 / 3;
  border-radius: 12px;
  overflow: hidden;
  background: #e8edf0;
  box-shadow: 0 4px 10px #172b3a0c;
  transition:
    box-shadow 0.2s,
    transform 0.2s;
}
.poster-action:hover {
  transform: translateY(-3px);
  box-shadow: 0 10px 22px #172b3a20;
}
.poster-action img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.poster-action:after {
  inset: 65% 0 0;
  background: linear-gradient(transparent, #10212d99);
}
.poster-empty {
  display: flex;
  height: 100%;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 12px;
  background: linear-gradient(145deg, #edf2f5, #dce6e9);
  color: #8c9da8;
  font-size: 30px;
}
.poster-empty small {
  font-size: 12px;
  font-weight: 400;
}
.rating {
  top: 9px;
  right: 9px;
  padding: 4px 7px;
  border: 1px solid #ffffff24;
  border-radius: 6px;
  background: #122632d9;
  font-size: 11px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.poster-kind {
  bottom: 10px;
  left: 11px;
  font-size: 11px;
  font-weight: 550;
  letter-spacing: 0.5px;
  animation: none;
}
.media-state {
  top: 10px;
  left: 9px;
  right: 50px;
  bottom: auto;
  align-items: flex-start;
}
.media-state > span {
  padding: 3px 6px;
  border: 1px solid #ffffff30;
  border-radius: 5px;
  background: #174e48e8;
  color: #fff;
  font-size: 10px;
}
.media-name {
  display: block;
  height: 55px;
  padding: 11px 0 0;
  border: 0;
  background: none;
  width: 100%;
  text-align: left;
  white-space: normal;
}
.media-name strong {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font-size: 14px;
  font-weight: 650;
  line-height: 1.55;
  color: #172b3a;
  white-space: normal;
}
.media-name:hover strong {
  color: var(--accent);
}
.media-card > p {
  margin: 5px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}
.media-actions {
  display: flex;
  gap: 7px;
  margin-top: 12px;
}
.media-actions button {
  min-width: 0;
  flex: 1;
  min-height: 32px;
  padding: 6px 5px;
  border-color: var(--line);
  border-radius: 7px;
  background: var(--surface);
  color: #526674;
  font-size: 12px;
  font-weight: 500;
}
.media-actions button:hover {
  border-color: #98c7c2;
  color: var(--accent);
  background: var(--accent-soft);
}
.media-actions svg {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}
@media (max-width: 1440px) {
  .media-grid {
    grid-template-columns: repeat(5, minmax(0, 1fr));
  }
}
@media (max-width: 1100px) {
  .media-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 25px 17px;
  }
}
@media (max-width: 760px) {
  .media-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .media-name strong {
    font-size: 13px;
  }
}
@media (max-width: 520px) {
  .media-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 24px 14px;
  }
  .poster-action {
    border-radius: 10px;
  }
  .media-name {
    height: 52px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .poster-action,
  .poster-action img {
    transition: none;
  }
  .poster-action:hover,
  .media-card:hover .poster-action img {
    transform: none;
  }
}
</style>
