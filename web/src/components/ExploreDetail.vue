<script setup lang="ts">
import CatalogRelated from './CatalogRelated.vue';
import MediaDetailLayout from './MediaDetailLayout.vue';
import { onMounted, ref } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { api, errorText } from '../api';
import { exploreImage, parseExploreDetailRoute } from '../features/explore-media';
import ExploreActions from './ExploreActions.vue';

const props = defineProps<{ route: string }>();
const { source, kind, sourceId, title, year } = parseExploreDetailRoute(props.route);
const media = ref<any>(null),
  loading = ref(true),
  error = ref('');
const actions = ref<InstanceType<typeof ExploreActions>>();
async function load() {
  loading.value = true;
  error.value = '';
  try {
    const query = new URLSearchParams({ source: source!, kind: kind!, source_id: sourceId! });
    if (title != null) query.set('title', title);
    if (year != null) query.set('year', year);
    media.value =
      source === 'themoviedb'
        ? {
            ...(await api(
              '/catalog/detail?' + new URLSearchParams({ kind: kind!, id: sourceId! }),
            )),
            source,
            source_id: sourceId,
          }
        : await api('/catalog/explore/detail?' + query);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
onMounted(load);
</script>

<template>
  <div v-if="error" class="error-box" role="alert">
    {{ error }} <VBtn variant="text" @click="load">重试</VBtn>
  </div>
  <div v-else-if="loading" class="explore-detail-loading">
    <VProgressCircular indeterminate color="primary" aria-label="加载详情" />
  </div>
  <template v-else-if="media">
    <MediaDetailLayout
      :media="media"
      :poster-url="exploreImage(media)"
      :backdrop-url="exploreImage(media, media.backdrop_path, 'w1280')"
    >
      <template #actions>
        <VBtn color="primary" variant="tonal" prepend-icon="mdi-magnify" @click="actions?.open(media, 'search')">
          搜索资源
        </VBtn>
        <VBtn
          :color="media.subscription_id ? 'warning' : 'error'"
          variant="tonal"
          :prepend-icon="media.subscription_id ? 'mdi-heart' : 'mdi-heart-outline'"
          @click="actions?.open(media, 'subscribe')"
        >
          {{ media.subscription_id ? '已订阅' : '订阅' }}
        </VBtn>
      </template>
    </MediaDetailLayout>
    <CatalogRelated :key="route + 'credits'" :media="media" part="credits" />
    <CatalogRelated :key="route + 'recommendations'" :media="media" part="recommendations" />
  </template>
  <ExploreActions ref="actions" />
</template>

<style scoped>
.explore-detail-loading {
  display: grid;
  min-height: 280px;
  place-items: center;
}
</style>
