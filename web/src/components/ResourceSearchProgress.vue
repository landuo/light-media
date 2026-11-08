<script setup lang="ts">
import { computed } from 'vue';
import { VCard } from 'vuetify/components/VCard';
import { VChip } from 'vuetify/components/VChip';
import { VIcon } from 'vuetify/components/VIcon';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { searchPending } from '../features/search-progress';

const props = defineProps<{
  title?: string;
  keyword?: string;
  season?: number;
  siteIds: string[];
  sites: { id: string; status: string }[];
}>();
const completed = computed(() => {
  const settled = new Set(props.sites.filter(site => !searchPending(site.status)).map(site => site.id));
  return props.siteIds.filter(id => settled.has(id)).length;
});
const progress = computed(() => props.siteIds.length ? Math.round(completed.value * 100 / props.siteIds.length) : 0);
</script>

<template>
  <VCard class="resource-progress" variant="flat" role="status">
    <div class="progress-heading">
      <VProgressCircular :model-value="progress" :indeterminate="!completed" color="primary" :size="48" :width="4">
        <VIcon icon="mdi-movie-search" size="22" />
      </VProgressCircular>
      <div class="progress-copy">
        <strong>正在搜索站点</strong>
        <div class="progress-tags">
          <VChip v-if="keyword" color="primary" variant="tonal" size="small">关键词 · {{ keyword }}</VChip>
          <VChip v-if="title" color="primary" variant="tonal" size="small">{{ title }}</VChip>
          <VChip v-if="season" color="primary" variant="tonal" size="small">第 {{ season }} 季</VChip>
        </div>
      </div>
      <span v-if="siteIds.length" class="progress-count">{{ completed }} / {{ siteIds.length }}</span>
    </div>
    <VProgressLinear :model-value="progress" :indeterminate="!completed" color="primary" rounded />
  </VCard>
</template>

<style scoped>
.resource-progress {
  margin-bottom: 12px;
  padding: 16px;
  border-radius: 12px;
}
.progress-heading,
.progress-tags {
  display: flex;
  align-items: center;
  gap: 8px;
}
.progress-heading {
  margin-bottom: 12px;
  gap: 12px;
}
.progress-copy { flex: 1; min-width: 0; }
.progress-tags { flex-wrap: wrap; margin-top: 4px; }
.progress-tags :deep(.v-chip__content) { display: block; overflow: hidden; text-overflow: ellipsis; }
.progress-tags .v-chip { max-width: 100%; }
.progress-count { flex: none; color: var(--accent); font-weight: 600; }
</style>
