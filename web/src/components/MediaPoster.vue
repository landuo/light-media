<script setup lang="ts">
import { ref, watch } from 'vue';
import { VImg } from 'vuetify/components/VImg';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';

const props = defineProps<{ src: string; alt: string }>();
const emit = defineEmits<{ error: []; load: [] }>();
const loaded = ref(false),
  failed = ref(false);
watch(
  () => props.src,
  () => {
    loaded.value = false;
    failed.value = false;
  },
  { flush: 'sync' },
);
function fail() {
  failed.value = true;
  emit('error');
}
function ready() {
  loaded.value = true;
  emit('load');
}
</script>

<template>
  <div class="media-poster">
    <VImg
      v-if="src && !failed"
      :key="src"
      :src="src"
      :alt="alt"
      :aspect-ratio="2 / 3"
      height="100%"
      cover
      @load="ready"
      @loadstart="loaded = false"
      @error="fail"
    >
      <template #placeholder>
        <VSkeletonLoader class="poster-skeleton" type="image" aria-label="海报加载中" />
      </template>
    </VImg>
    <div v-else class="poster-empty">◈<small>暂无海报</small></div>
    <slot v-if="loaded || failed || !src" :loaded="loaded" />
  </div>
</template>

<style scoped>
.media-poster {
  position: relative;
  width: 100%;
  height: 100%;
}
.poster-skeleton,
.poster-skeleton :deep(.v-skeleton-loader__image) {
  width: 100%;
  height: 100%;
  border-radius: 0;
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
</style>
