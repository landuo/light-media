<script setup lang="ts">
import { go, image, label } from '../api';
import AppIcon from './AppIcon.vue';
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
        <span class="poster-kind" :class="m.media_type">{{ label(m.media_type) }}</span>
        <div class="media-state">
          <span v-if="m.library_state === 'present' || m.library_state === 'managed'">已入库</span>
          <span v-else-if="m.library_state">{{ label(m.library_state) }}</span>
          <span v-if="m.subscription_id">已订阅</span>
          <span v-if="m.download_state">{{ label(m.download_state) }}</span>
        </div>
      </button>
      <div class="media-overlay" @click="detail(m)">
        <span class="media-year">{{ m.year || '日期待定' }}</span>
        <button class="media-name" @click.stop="detail(m)">
          <strong>{{ m.title }}</strong>
        </button>
        <p v-if="m.overview || m.character" class="media-overview">
          {{ m.character || m.overview }}
        </p>
        <div v-if="quickActions" class="media-actions">
          <button
            class="icon-button"
            :aria-label="'搜索' + m.title + '资源'"
            title="搜索资源"
            @click.stop="go('resources/' + m.media_type + '/' + m.id)"
          >
            <AppIcon name="search" />
          </button>
          <button
            class="icon-button"
            :class="{ subscribed: m.subscription_id }"
            :aria-label="(m.subscription_id ? '查看' : '添加') + m.title + '订阅'"
            :title="m.subscription_id ? '查看订阅' : '添加订阅'"
            @click.stop="
              go(
                m.subscription_id
                  ? 'subscriptions'
                  : 'subscriptions/new/' + m.media_type + '/' + m.id,
              )
            "
          >
            <AppIcon name="heart" />
          </button>
        </div>
      </div>
    </article>
  </div>
</template>
<style scoped>
.media-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(156px, 1fr));
  gap: 16px;
}
.media-card {
  position: relative;
  min-width: 0;
  aspect-ratio: 2/3;
  border-radius: var(--radius);
  overflow: hidden;
  background: var(--surface);
  transition: transform 0.3s;
}
.media-card:hover {
  transform: translateY(-4px);
}
.poster-action {
  width: 100%;
  height: 100%;
  padding: 0;
  border: 0;
  border-radius: var(--radius);
  background: var(--surface-subtle);
  display: block;
}
.poster-action img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.poster-action::after {
  content: none;
}
.poster-empty {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 12px;
  background: var(--surface-subtle);
  color: var(--muted);
  font-size: 32px;
}
.rating,
.poster-kind {
  position: absolute;
  top: 8px;
  bottom: auto;
  z-index: 3;
  padding: 3px 8px;
  border-radius: 16px;
  color: #fff;
  font-size: 12px;
  line-height: 18px;
  font-weight: 700;
}
.rating {
  right: 8px;
  background: #9333eacc;
}
.poster-kind {
  left: 8px;
  background: #4f46e5cc;
  letter-spacing: 0;
}
.poster-kind.movie {
  background: #2563ebcc;
}
.media-state {
  top: 40px;
  left: 8px;
  right: auto;
  align-items: flex-start;
}
.media-state > span {
  padding: 2px 6px;
  border-radius: 12px;
  background: var(--success);
  color: white;
  font-size: 11px;
}
.media-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  padding: 8px;
  background: linear-gradient(rgba(45, 55, 72, 0.4), rgba(45, 55, 72, 0.9));
  color: #fff;
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
  transition: opacity 0.2s;
}
.media-card:hover .media-overlay,
.media-card:focus-within .media-overlay {
  opacity: 1;
  visibility: visible;
  pointer-events: auto;
}
.media-year {
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
}
.media-name {
  display: block;
  padding: 4px 0 8px;
  min-height: 0;
  width: 100%;
  border: 0;
  background: none;
  color: inherit;
  text-align: left;
  white-space: normal;
}
.media-name:hover {
  background: none;
  color: #fff;
}
.media-name strong {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font-size: 18px;
  line-height: 20px;
  font-weight: 700;
}
.media-overview {
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 0 0 8px;
  font-size: 14px;
  line-height: 16px;
}
.media-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.media-actions .icon-button {
  border: 0;
  background: transparent;
  color: white;
  width: 32px;
  height: 32px;
  min-height: 32px;
  padding: 5px;
}
.media-actions .icon-button:hover {
  background: #ffffff1f;
}
.media-actions .subscribed {
  color: var(--danger);
}
@media (hover: none) {
  .media-overlay {
    opacity: 1;
    visibility: visible;
    pointer-events: auto;
    background: linear-gradient(transparent 35%, rgba(45, 55, 72, 0.85));
  }
  .media-overview {
    display: none;
  }
}
@media (max-width: 600px) {
  .media-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .media-card,
  .media-overlay {
    transition: none;
  }
  .media-card:hover {
    transform: none;
  }
}
</style>
