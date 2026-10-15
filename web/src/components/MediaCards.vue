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
          ⌕ 搜索资源
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
.media-card {
  text-align: left;
}
.poster-action {
  padding: 0;
  border: 0;
  width: 100%;
  display: block;
  border-radius: 10px;
  overflow: hidden;
}
.media-name {
  display: block;
  padding: 10px 0 0;
  border: 0;
  background: none;
  width: 100%;
  text-align: left;
}
.media-name strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.media-actions {
  display: flex;
  gap: 5px;
  margin-top: 8px;
}
.media-actions button {
  min-width: 0;
  flex: 1;
  padding: 6px 4px;
  font-size: 11px;
}
</style>
