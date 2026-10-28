<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { api, errorText, go } from '../api';
import { exploreImage } from '../features/explore-media';
import MediaCards from './MediaCards.vue';

const props = defineProps<{ media: any; part: 'credits' | 'recommendations' }>();
const root = ref<HTMLElement>();
const rows = ref<any[]>([]),
  error = ref(''),
  loading = ref(false);
const identity = computed(() => {
  const original = props.media.original_media;
  for (const [key, source] of [
    ['tmdb_id', 'themoviedb'],
    ['douban_id', 'douban'],
    ['bangumi_id', 'bangumi'],
  ]) {
    if (original?.[key]) return { source, id: String(original[key]) };
  }
  return null;
});
let observer: IntersectionObserver | undefined;
let active = true;
async function load() {
  if (!identity.value || loading.value) return;
  loading.value = true;
  error.value = '';
  try {
    const query = new URLSearchParams({
      source: identity.value.source,
      source_id: identity.value.id,
      kind: props.media.media_type,
    });
    const result = await api('/catalog/detail/' + props.part + '?' + query);
    if (active) rows.value = result || [];
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
onMounted(() => {
  // The fixed view selects TMDB, then Douban, then Bangumi from recognized IDs.
  // AniList has no related card in that view; retain this host's existing view.
  if (!identity.value) {
    if (props.media.source === 'anilist')
      rows.value =
        props.part === 'credits' ? props.media.cast || [] : props.media.recommendations || [];
    return;
  }
  observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        observer?.disconnect();
        void load();
      }
    },
    { rootMargin: '300px' },
  );
  if (root.value) observer.observe(root.value);
});
onUnmounted(() => {
  active = false;
  observer?.disconnect();
});
</script>

<template>
  <div ref="root" class="catalog-related">
    <div v-if="error" role="alert" class="error-box">
      {{ error }} <VBtn variant="text" :loading="loading" @click="load">重试</VBtn>
    </div>
    <section v-if="rows.length" class="section">
      <h3>{{ part === 'credits' ? '演员阵容' : '相关推荐' }}</h3>
      <div v-if="part === 'credits'" class="related-cast">
        <button
          v-for="(person, index) in rows.slice(0, 20)"
          :key="person.id || index"
          :disabled="identity?.source !== 'themoviedb' || !person.id"
          @click="go('person/' + person.id)"
        >
          <img
            v-if="
              exploreImage(
                { source: identity?.source || media.source },
                person.profile_path,
                'w185',
              )
            "
            :src="
              exploreImage(
                { source: identity?.source || media.source },
                person.profile_path,
                'w185',
              )
            "
            :alt="person.name"
            loading="lazy"
          />
          <strong>{{ person.name }}</strong
          ><small>{{ person.character }}</small>
        </button>
      </div>
      <MediaCards v-else :items="rows" quick-actions original-actions />
    </section>
  </div>
</template>

<style scoped>
.catalog-related {
  min-height: 1px;
}
.related-cast {
  display: flex;
  gap: 16px;
  overflow-x: auto;
}
.related-cast button {
  flex: 0 0 112px;
  padding: 0;
  border: 0;
  background: transparent;
  text-align: left;
  color: inherit;
}
.related-cast img {
  width: 112px;
  height: 168px;
  object-fit: cover;
  border-radius: 8px;
}
.related-cast strong,
.related-cast small {
  display: block;
}
.related-cast small {
  color: var(--muted);
}
</style>
