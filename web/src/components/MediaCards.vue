<script setup lang="ts">
import { ref } from 'vue';
import { VIcon } from 'vuetify/components/VIcon';
import { go, image, label } from '../api';
import AppIcon from './AppIcon.vue';
import { startSearch } from '../features/search-session';
import tmdbLogo from '../assets/tmdb.png';
import doubanLogo from '../assets/douban.png';
import bangumiLogo from '../assets/bangumi.png';
import ExploreActions from './ExploreActions.vue';
import MediaPoster from './MediaPoster.vue';
import { exploreMediaKey } from '../features/explore-feed';
import { exploreImage, exploreDetailRoute } from '../features/explore-media';
const props = defineProps<{ items: any[]; quickActions?: boolean; originalActions?: boolean }>();
const actions = ref<InstanceType<typeof ExploreActions>>();
const sourceLogos: Record<string, string> = {
  themoviedb: tmdbLogo,
  douban: doubanLogo,
  bangumi: bangumiLogo,
};
const failedImages = ref(new Set<string>());
function detail(m: any) {
  go(props.originalActions ? exploreDetailRoute(m) : 'detail/' + m.media_type + '/' + m.id);
}
function poster(m: any) {
  return props.originalActions ? exploreImage(m) : image(m.poster_path);
}
function posterKey(m: any) {
  return exploreMediaKey(m) + ':' + poster(m);
}
function search(m: any) {
  if (props.originalActions) actions.value?.open(m, 'search');
  else startSearch('resources/' + m.media_type + '/' + m.id);
}
function subscribe(m: any) {
  if (props.originalActions) actions.value?.open(m, 'subscribe');
  else go(m.subscription_id ? 'subscriptions' : 'subscriptions/new/' + m.media_type + '/' + m.id);
}
</script>
<template>
  <div class="media-grid">
    <article
      v-for="m in items"
      :key="exploreMediaKey(m)"
      class="media-card"
      :class="{ 'no-poster': !poster(m) || failedImages.has(posterKey(m)) }"
    >
      <button class="poster poster-action" @click="detail(m)" :aria-label="'查看' + m.title">
        <MediaPoster
          :src="poster(m)"
          :alt="m.title"
          @error="failedImages.add(posterKey(m))"
          @load="failedImages.delete(posterKey(m))"
          v-slot="{ loaded }"
        >
          <span v-if="m.vote_average > 0" class="rating">{{
            originalActions ? Number(m.vote_average.toFixed(1)) : m.vote_average.toFixed(1)
          }}</span>
          <span class="poster-kind" :class="m.media_type">{{
            m.media_type === 'tv' ? '电视剧' : label(m.media_type)
          }}</span>
          <span
            v-if="['present', 'managed', 'external'].includes(m.library_state)"
            class="media-exists"
            title="已入库"
            aria-label="已入库"
            ><AppIcon name="check-circle"
          /></span>
          <img
            v-if="loaded && sourceLogos[m.source || 'themoviedb']"
            class="media-source"
            :src="sourceLogos[m.source || 'themoviedb']"
            :alt="m.source || 'TheMovieDb'"
          />
          <span
            v-else-if="loaded && m.source === 'anilist'"
            class="media-source source-anilist"
            aria-label="AniList"
            >A</span
          >
        </MediaPoster>
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
            @click.stop="search(m)"
          >
            <VIcon v-if="originalActions" icon="mdi-magnify" size="20" /><AppIcon
              v-else
              name="search"
            />
          </button>
          <button
            class="icon-button"
            :class="{ subscribed: m.subscription_id }"
            :aria-label="
              (m.subscription_id ? (originalActions ? '取消' : '查看') : '添加') + m.title + '订阅'
            "
            :title="m.subscription_id ? (originalActions ? '取消订阅' : '查看订阅') : '添加订阅'"
            @click.stop="subscribe(m)"
          >
            <VIcon v-if="originalActions" icon="mdi-heart" size="20" /><AppIcon
              v-else
              name="heart"
            />
          </button>
        </div>
      </div>
    </article>
  </div>
  <ExploreActions v-if="originalActions" ref="actions" />
</template>
<style scoped>
.media-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(144px, 1fr));
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
.poster-action::after {
  content: none;
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
.media-exists {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 4;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--success);
  color: white;
  display: grid;
  place-items: center;
}
.media-exists svg {
  width: 20px;
  height: 20px;
}
.media-card:hover .media-exists {
  display: none;
}
.poster-action .media-source {
  position: absolute;
  right: 4px;
  bottom: 4px;
  width: 24px;
  height: 24px;
  object-fit: contain;
}
.media-card:hover .media-source {
  visibility: hidden;
}
.source-anilist {
  position: absolute;
  right: 4px;
  bottom: 4px;
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #53b5ed;
  color: #143447;
  font-size: 15px;
  font-weight: 900;
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
.media-card.no-poster .media-overlay,
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
    gap: 16px;
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
