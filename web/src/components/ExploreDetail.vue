<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { api, errorText, go } from '../api';
import { exploreImage } from '../features/explore-media';
import ExploreActions from './ExploreActions.vue';
import MediaCards from './MediaCards.vue';

const props = defineProps<{ route: string }>();
const [, source, kind, sourceId] = props.route.split('?')[0]!.split('/');
const media = ref<any>(null),
  loading = ref(true),
  error = ref('');
const actions = ref<InstanceType<typeof ExploreActions>>();
const sources: Record<string, string> = {
  themoviedb: 'TheMovieDb',
  douban: '豆瓣',
  bangumi: 'Bangumi',
  anilist: 'AniList',
};
async function load() {
  loading.value = true;
  error.value = '';
  try {
    const query = new URLSearchParams({ source: source!, kind: kind!, source_id: sourceId! });
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
    <section
      class="detail-hero explore-detail"
      :style="
        exploreImage(media, media.backdrop_path, 'w1280')
          ? {
              backgroundImage: `linear-gradient(180deg,rgba(244,245,250,.8),rgba(244,245,250,.98)),url(${exploreImage(media, media.backdrop_path, 'w1280')})`,
            }
          : {}
      "
    >
      <img
        v-if="exploreImage(media)"
        class="detail-poster"
        :src="exploreImage(media)"
        :alt="media.title"
      />
      <div class="detail-copy">
        <h1>
          {{ media.title }} <span v-if="media.year">({{ media.year }})</span>
        </h1>
        <p class="original-title">{{ media.original_title }}</p>
        <div class="tags">
          <span v-if="media.vote_average">★ {{ media.vote_average.toFixed(1) }}</span>
          <span>{{ media.media_type === 'movie' ? '电影' : '电视剧' }}</span>
          <span v-for="genre in media.genres || []" :key="genre.name">{{ genre.name }}</span>
          <span v-if="media.runtime">{{ media.runtime }} 分钟</span>
          <span v-if="media.number_of_episodes">{{ media.number_of_episodes }} 集</span>
        </div>
        <p class="overview">{{ media.overview || '暂无剧情简介。' }}</p>
        <div class="button-row">
          <VBtn
            color="info"
            variant="tonal"
            prepend-icon="mdi-magnify"
            @click="actions?.open(media, 'search')"
            >搜索资源</VBtn
          >
          <VBtn
            color="error"
            variant="tonal"
            prepend-icon="mdi-heart"
            @click="actions?.open(media, 'subscribe')"
            >{{ media.subscription_id ? '已订阅' : '订阅' }}</VBtn
          >
        </div>
        <p class="hint">
          {{ sources[source!] }} {{ sourceId }}
          <span v-if="media.release_date"> · {{ media.release_date }}</span>
        </p>
      </div>
    </section>
    <section v-if="media.cast?.length" class="panel">
      <h3>演员</h3>
      <div class="explore-cast">
        <button
          v-for="(person, index) in media.cast"
          :key="person.id || person.name + index"
          :disabled="source !== 'themoviedb' || !person.id"
          @click="go('person/' + person.id)"
        >
          <img
            v-if="exploreImage(media, person.profile_path)"
            :src="exploreImage(media, person.profile_path)"
            :alt="person.name"
            loading="lazy"
          />
          <strong>{{ person.name }}</strong
          ><small>{{ person.character }}</small>
        </button>
      </div>
    </section>
    <section v-if="media.recommendations?.length" class="explore-related">
      <h3>相关推荐</h3>
      <MediaCards :items="media.recommendations" quick-actions original-actions />
    </section>
  </template>
  <ExploreActions ref="actions" />
</template>

<style scoped>
.explore-detail-loading {
  display: grid;
  min-height: 280px;
  place-items: center;
}
.explore-detail {
  gap: 32px;
  padding: 32px;
  background-color: var(--bg);
  color: var(--text);
  border-radius: var(--radius);
}
.detail-poster {
  width: 184px;
  height: 276px;
  border-radius: var(--radius);
  border: 1px solid var(--line);
}
.detail-copy h1 {
  color: var(--text);
  font-size: clamp(26px, 2.7vw, 36px);
}
.detail-copy .original-title,
.detail-copy .hint {
  color: var(--muted);
}
.detail-copy .overview {
  color: var(--text);
  line-height: 1.8;
}
.detail-copy .tags span {
  color: var(--text);
  background: var(--surface);
}
.explore-cast {
  display: flex;
  overflow-x: auto;
  gap: 16px;
  padding-top: 12px;
}
.explore-cast button {
  flex: 0 0 112px;
  padding: 0;
  border: 0;
  background: transparent;
  display: flex;
  flex-direction: column;
  gap: 6px;
  white-space: normal;
}
.explore-cast img {
  width: 112px;
  height: 168px;
  object-fit: cover;
  border-radius: var(--radius);
}
.explore-cast small {
  color: var(--muted);
}
.explore-related {
  margin: 24px 0;
}
.explore-related h3 {
  margin-bottom: 16px;
}
@media (max-width: 600px) {
  .explore-detail {
    padding: 16px;
    gap: 20px;
  }
  .detail-poster {
    width: 136px;
    height: 204px;
  }
}
</style>
