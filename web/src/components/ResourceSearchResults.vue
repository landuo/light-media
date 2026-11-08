<script setup lang="ts">
import { VBtn } from 'vuetify/components/VBtn';
import { VCard } from 'vuetify/components/VCard';
import { VIcon } from 'vuetify/components/VIcon';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { candidateKey, type Candidate } from '../features/resources';
import ResourceCard from './ResourceCard.vue';

defineProps<{
  groups: Candidate[][];
  results: Candidate[];
  names: Record<string, string>;
  downloads: Record<string, string>;
  selected: string[];
  mediaTitle?: string;
  showIdentityReview?: boolean;
  hasCandidates: boolean;
  loading: boolean;
}>();
const view = defineModel<string>('view', { required: true });
defineEmits<{
  download: [candidate: Candidate];
  select: [candidate: Candidate];
  clear: [];
}>();
</script>

<template>
  <div v-if="results.length && view === 'card'" class="release-grid">
    <ResourceCard
      v-for="group in groups"
      :key="candidateKey(group[0]!)"
      :candidates="group"
      :names="names"
      :downloads="downloads"
      :selected="selected"
      :media-title="mediaTitle"
      :show-identity-review="showIdentityReview"
      @download="$emit('download', $event)"
      @select="$emit('select', $event)"
    />
  </div>
  <VCard v-else-if="results.length" class="resource-list" variant="flat">
    <ResourceCard
      v-for="candidate in results"
      :key="candidateKey(candidate)"
      layout="list"
      :candidates="[candidate]"
      :names="names"
      :downloads="downloads"
      :selected="selected"
      :media-title="mediaTitle"
      :show-identity-review="showIdentityReview"
      @download="$emit('download', $event)"
      @select="$emit('select', $event)"
    />
  </VCard>
  <div v-else-if="loading" :class="view === 'card' ? 'release-grid' : 'resource-list'" aria-hidden="true">
    <VSkeletonLoader
      v-for="index in (view === 'card' ? 6 : 3)"
      :key="index"
      :type="view === 'card' ? 'article, actions' : 'list-item-avatar-two-line'"
    />
  </div>
  <div v-else class="empty-state resource-empty">
    <VIcon icon="mdi-file-search-outline" size="64" color="grey-lighten-1" />
    <h3>{{ hasCandidates ? '没有符合筛选条件的资源' : '没有搜索到资源' }}</h3>
    <VBtn v-if="hasCandidates" color="primary" variant="text" @click="$emit('clear')">清空筛选</VBtn>
  </div>
  <VBtn
    v-if="hasCandidates"
    class="resource-view-toggle"
    :icon="view === 'card' ? 'mdi-view-list-outline' : 'mdi-view-grid-outline'"
    :aria-label="view === 'card' ? '列表视图' : '卡片视图'"
    :title="view === 'card' ? '列表视图' : '卡片视图'"
    color="primary"
    size="48"
    @click="view = view === 'card' ? 'list' : 'card'"
  />
</template>

<style scoped>
.release-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  align-items: start;
  gap: 16px;
}
.resource-list {
  padding: 8px;
  background: var(--surface);
  border-radius: 12px;
}
.resource-list > :deep(.release-card + .release-card) {
  border-top: 1px solid var(--line);
}
.resource-empty {
  display: grid;
  justify-items: center;
  align-content: center;
  gap: 12px;
  min-height: 300px;
}
.resource-view-toggle {
  position: fixed;
  inset-block-end: max(20px, env(safe-area-inset-bottom));
  inset-inline-end: max(20px, env(safe-area-inset-right));
  z-index: 5;
}
</style>
