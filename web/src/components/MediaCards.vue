<script setup lang="ts">
import { go, image, label } from '../api';
defineProps<{ items: any[] }>();
</script>
<template>
  <div class="media-grid">
    <button
      v-for="m in items"
      :key="m.media_type + ':' + m.id"
      class="media-card"
      @click="go('detail/' + m.media_type + '/' + m.id)"
    >
      <div class="poster">
        <img
          v-if="image(m.poster_path)"
          :src="image(m.poster_path)"
          :alt="m.title"
          loading="lazy"
        />
        <div v-else class="poster-empty">◈<small>暂无海报</small></div>
        <span v-if="m.vote_average > 0" class="rating">★ {{ m.vote_average.toFixed(1) }}</span
        ><span class="poster-kind">{{ label(m.media_type) }}</span>
        <div class="media-state">
          <span v-if="m.library_state === 'present' || m.library_state === 'managed'">已入库</span
          ><span v-else-if="m.library_state">{{ label(m.library_state) }}</span
          ><span v-if="m.subscription_id">已订阅</span
          ><span v-if="m.download_state">{{ label(m.download_state) }}</span>
        </div>
      </div>
      <strong>{{ m.title }}</strong>
      <p>
        {{ m.year || '日期待定' }}<span v-if="m.character"> · {{ m.character }}</span>
      </p>
    </button>
  </div>
</template>
