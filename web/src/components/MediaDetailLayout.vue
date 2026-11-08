<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { VChip } from 'vuetify/components/VChip';
import { VIcon } from 'vuetify/components/VIcon';
import { VRating } from 'vuetify/components/VRating';
import { label } from '../api';

const props = defineProps<{ media: any; posterUrl?: string; backdropUrl?: string }>();
const original = computed(() => props.media.original_media || {});
const posterFailed = ref(false);
const backdropFailed = ref(false);
watch(
  () => props.posterUrl,
  () => {
    posterFailed.value = false;
  },
);
watch(
  () => props.backdropUrl,
  () => {
    backdropFailed.value = false;
  },
);
const poster = computed(() => (posterFailed.value ? '' : props.posterUrl));
const backdrop = computed(() => (!backdropFailed.value && props.backdropUrl) || poster.value);
function backdropError() {
  const failed = backdrop.value;
  if (failed === props.backdropUrl) backdropFailed.value = true;
  if (failed === props.posterUrl) posterFailed.value = true;
}
function providerID(value: unknown) {
  const id = String(value ?? '');
  return /^[1-9][0-9]*$/.test(id) ? id : '';
}
function sourceID(source: string, value: unknown) {
  return (
    providerID(value) || (props.media.source === source ? providerID(props.media.source_id) : '')
  );
}
const tmdb = computed(() =>
  sourceID(
    'themoviedb',
    original.value.tmdb_id ||
      (!props.media.source || props.media.source === 'themoviedb' ? props.media.id : undefined),
  ),
);
const douban = computed(() => sourceID('douban', original.value.douban_id));
const bangumi = computed(() => sourceID('bangumi', original.value.bangumi_id));
const anilist = computed(() => sourceID('anilist', original.value.anilist_id));
const links = computed(() => {
  const imdb = String(original.value.imdb_id || props.media.imdb_id || '');
  const tvdb = providerID(original.value.tvdb_id);
  const kind = props.media.media_type === 'tv' ? 'tv' : 'movie';
  return [
    { name: 'TheMovieDb', href: tmdb.value && `https://www.themoviedb.org/${kind}/${tmdb.value}` },
    { name: '豆瓣', href: douban.value && `https://movie.douban.com/subject/${douban.value}` },
    { name: 'IMDb', href: /^tt[0-9]+$/.test(imdb) && `https://www.imdb.com/title/${imdb}` },
    { name: 'TheTvDb', href: tvdb && `https://www.thetvdb.com/series/${tvdb}` },
    { name: 'Bangumi', href: bangumi.value && `https://bgm.tv/subject/${bangumi.value}` },
    { name: 'AniList', href: anilist.value && `https://anilist.co/anime/${anilist.value}` },
  ].filter((link) => link.href);
});
const runtime = computed(() => props.media.runtime || original.value.episode_run_time?.[0]);
const genres = computed(() =>
  (props.media.genres || [])
    .map((genre: { name: string }) => genre.name)
    .filter(Boolean)
    .join('、'),
);
const rating = computed(() => Math.min(10, Math.max(0, Number(props.media.vote_average) || 0)));
const inLibrary = computed(() =>
  ['present', 'managed', 'external'].includes(props.media.library_state),
);
const directors = computed(() =>
  (original.value.directors || [])
    .filter((person: any) => person?.name)
    .map((person: any) => ({
      name: person.name,
      role: person.job || (Array.isArray(person.roles) ? person.roles.join('、') : ''),
      href: tmdb.value && providerID(person.id) ? `#person/${providerID(person.id)}` : '',
    })),
);
function namedValues(items: unknown) {
  return Array.isArray(items)
    ? items.map((item) => (typeof item === 'string' ? item : item?.name)).filter(Boolean)
    : [];
}
const firstRelease = computed(() => {
  if (props.media.media_type !== 'movie') return;
  return (original.value.release_dates || [])
    .filter(
      (entry: any) =>
        (entry?.type === 4 || entry?.type === 5) && Number.isFinite(Date.parse(entry.date)),
    )
    .sort((a: any, b: any) => Date.parse(a.date) - Date.parse(b.date))[0];
});
const facts = computed(() => {
  const source = original.value;
  const countries = namedValues(source.production_countries);
  const release = firstRelease.value;
  return [
    { label: 'ID', values: [tmdb.value || douban.value || bangumi.value || anilist.value] },
    { label: '原始标题', values: [props.media.original_title || source.original_name] },
    { label: '状态', values: [props.media.status] },
    { label: '上映日期', values: [props.media.release_date || source.first_air_date] },
    {
      label: release?.type === 4 ? '数字发行' : '实体发行',
      values: release
        ? [[release.iso_code, release.date.slice(0, 10)].filter(Boolean).join(' ')]
        : [],
    },
    { label: '原始语言', values: [props.media.original_language] },
    {
      label: '出品国家',
      values: countries.length
        ? countries
        : namedValues(props.media.production_countries || props.media.origin_country),
    },
    { label: '制作公司', values: namedValues(source.production_companies) },
  ]
    .map((fact) => ({ ...fact, values: fact.values.filter(Boolean) }))
    .filter((fact) => fact.values.length);
});
</script>

<template>
  <section class="media-detail">
    <div v-if="backdrop" class="media-detail-backdrop" aria-hidden="true">
      <img :src="backdrop" alt="" @error="backdropError" />
    </div>
    <header class="media-detail-header">
      <div class="media-detail-poster">
        <img v-if="poster" :src="poster" :alt="media.title" @error="posterFailed = true" />
        <VIcon v-else icon="mdi-image-outline" size="36" aria-hidden="true" />
      </div>
      <div class="media-detail-title">
        <div
          v-if="inLibrary || media.subscription_id || media.download_state"
          class="media-detail-status"
        >
          <VChip v-if="inLibrary" color="success" size="x-small" variant="flat">已入库</VChip>
          <VChip v-if="media.subscription_id" color="error" size="x-small" variant="tonal"
            >已订阅</VChip
          >
          <VChip v-if="media.download_state" size="x-small" variant="tonal">{{
            label(media.download_state)
          }}</VChip>
        </div>
        <h1>
          {{ media.title }} <span v-if="media.year">({{ media.year }})</span>
        </h1>
        <p v-if="runtime || genres" class="media-detail-attributes">
          <span v-if="runtime">{{ runtime }} 分钟</span>
          <span v-if="runtime && genres" aria-hidden="true">|</span>
          <span v-if="genres">{{ genres }}</span>
        </p>
      </div>
      <div v-if="$slots.actions" class="media-detail-actions"><slot name="actions" /></div>
    </header>

    <div class="media-detail-overview">
      <div class="media-detail-summary">
        <p v-if="original.tagline" class="media-detail-tagline">{{ original.tagline }}</p>
        <h2>简介</h2>
        <p class="media-detail-description">{{ media.overview || '暂无剧情简介。' }}</p>
        <ul v-if="directors.length" class="media-detail-crew">
          <li v-for="(director, index) in directors" :key="index">
            <strong v-if="director.role">{{ director.role }}</strong>
            <a
              v-if="director.href"
              :href="director.href"
              target="_blank"
              rel="noopener noreferrer"
              >{{ director.name }}</a
            >
            <span v-else>{{ director.name }}</span>
          </li>
        </ul>
        <div v-if="links.length" class="media-detail-links">
          <a
            v-for="link in links"
            :key="link.name"
            :href="link.href || undefined"
            target="_blank"
            rel="noopener noreferrer"
          >
            <VIcon icon="mdi-link" size="17" aria-hidden="true" />{{ link.name }}
          </a>
        </div>
        <slot name="seasons" />
      </div>

      <aside v-if="rating || facts.length" class="media-detail-facts" aria-label="影视信息">
        <div
          v-if="rating"
          class="media-detail-rating"
          role="img"
          :aria-label="`评分 ${rating} / 10`"
        >
          <VRating
            :model-value="rating"
            :length="10"
            color="warning"
            active-color="warning"
            density="compact"
            size="small"
            readonly
            aria-hidden="true"
          />
        </div>
        <dl v-if="facts.length">
          <div v-for="fact in facts" :key="fact.label" class="media-detail-fact">
            <dt>{{ fact.label }}</dt>
            <dd>
              <span v-for="(value, index) in fact.values" :key="index">{{ value }}</span>
            </dd>
          </div>
        </dl>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.media-detail {
  position: relative;
  isolation: isolate;
  padding-inline: 16px;
  color: var(--text);
}
.media-detail-backdrop {
  position: absolute;
  inset: -16px 0 auto;
  height: 384px;
  overflow: hidden;
  z-index: -1;
  pointer-events: none;
}
.media-detail-backdrop img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: top;
}
.media-detail-backdrop::after {
  content: '';
  position: absolute;
  inset: 0;
  background:
    linear-gradient(to bottom, transparent 50%, var(--bg)),
    linear-gradient(to top, transparent 72%, var(--bg)),
    linear-gradient(to right, var(--bg), transparent 50%),
    linear-gradient(to left, var(--bg), transparent 50%);
}
.media-detail-header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}
.media-detail-poster {
  display: grid;
  place-items: center;
  width: 128px;
  flex: none;
  aspect-ratio: 2 / 3;
  border: 1px solid var(--line);
  border-radius: 8px;
  overflow: hidden;
  background: var(--surface);
  color: var(--muted);
  box-shadow: 0 16px 30px -12px #0005;
}
.media-detail-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.media-detail-title {
  flex: 1;
  min-width: 0;
  text-align: center;
}
.media-detail-title h1 {
  margin: 0;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.25;
  overflow-wrap: anywhere;
}
.media-detail-title h1 span {
  font-size: 17px;
  white-space: nowrap;
}
.media-detail-status,
.media-detail-attributes,
.media-detail-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 8px;
}
.media-detail-status {
  margin-bottom: 8px;
}
.media-detail-attributes {
  margin: 5px 0 0;
  font-size: 14px;
}
.media-detail-overview {
  display: grid;
  align-items: start;
  gap: 24px;
  padding-block: 32px 16px;
}
.media-detail-summary {
  min-width: 0;
}
.media-detail-summary h2 {
  margin: 0 0 4px;
  font-size: 22px;
  font-weight: 700;
}
.media-detail-tagline {
  margin: 0 0 16px;
  font-size: 20px;
  font-style: italic;
}
.media-detail-description {
  margin: 0;
  font-size: 14px;
  line-height: 1.7;
  white-space: pre-line;
}
.media-detail-crew {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin: 22px 0 0;
  padding: 0;
  list-style: none;
}
.media-detail-crew li {
  display: grid;
  gap: 3px;
}
.media-detail-links {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 40px;
}
.media-detail-links a {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 9px;
  border-radius: 999px;
  background: #4b5563;
  color: #f3f4f6;
  font-size: 13px;
  text-decoration: none;
}
.media-detail-links a:hover {
  background: #374151;
}
.media-detail-facts {
  --facts-line: #606a7a;
  min-width: 0;
  border: 1px solid var(--facts-line);
  border-radius: 8px;
  font-size: 14px;
}
.media-detail-facts dl {
  margin: 0;
}
.media-detail-rating {
  display: flex;
  justify-content: center;
  padding: 14px 8px;
}
.media-detail-rating + dl,
.media-detail-fact + .media-detail-fact {
  border-top: 1px solid var(--facts-line);
}
.media-detail-fact {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 9px 14px;
}
.media-detail-fact dt {
  flex: none;
  font-weight: 600;
}
.media-detail-fact dd {
  display: grid;
  gap: 2px;
  min-width: 0;
  margin: 0;
  font-weight: 600;
  text-align: end;
  overflow-wrap: anywhere;
}
@media (min-width: 640px) {
  .media-detail-crew {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
@media (min-width: 768px) {
  .media-detail-poster {
    width: 176px;
  }
}
@media (min-width: 1024px) {
  .media-detail-overview {
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 32px;
  }
}
@media (min-width: 1280px) {
  .media-detail-header {
    flex-direction: row;
    align-items: end;
  }
  .media-detail-title {
    text-align: start;
  }
  .media-detail-title h1 {
    font-size: 36px;
  }
  .media-detail-status,
  .media-detail-attributes {
    justify-content: start;
  }
  .media-detail-actions {
    justify-content: end;
    flex: none;
  }
}
</style>
