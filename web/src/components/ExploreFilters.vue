<script setup lang="ts">
import { computed } from 'vue';
import { VChip } from 'vuetify/components/VChip';
import { VChipGroup } from 'vuetify/components/VChipGroup';
import { VLabel } from 'vuetify/components/VLabel';
import { VSlider } from 'vuetify/components/VSlider';
import { VTextField } from 'vuetify/components/VTextField';
import {
  mediaTypes,
  tmdbMovieSort,
  tmdbTVSort,
  tmdbMovieGenres,
  tmdbTVGenres,
  tmdbLanguages,
  doubanSort,
  doubanGenres,
  doubanZones,
  bangumiCategories,
  bangumiSort,
  exploreYears,
  normalizeExploreFilters,
  anilistSort,
  anilistFormats,
  anilistGenres,
  anilistSeasons,
  anilistYears,
  anilistStatuses,
  anilistCountries,
  type ExploreSource,
  type ExploreFilterState,
} from '../features/explore-filters';

const props = defineProps<{ source: ExploreSource; modelValue: ExploreFilterState }>();
const emit = defineEmits<{ 'update:modelValue': [value: ExploreFilterState] }>();
type Row = {
  label: string;
  key: keyof ExploreFilterState;
  options: Record<string, string> | [string, string][];
};
function entries(options: Row['options']): [string, string][] {
  return Array.isArray(options) ? options : Object.entries(options);
}
const rows = computed<Row[]>(() => {
  if (props.source === 'themoviedb')
    return [
      { label: '类型', key: 'type', options: mediaTypes },
      {
        label: '排序',
        key: 'sort_by',
        options: props.modelValue.type === 'movies' ? tmdbMovieSort : tmdbTVSort,
      },
      {
        label: '风格',
        key: 'with_genres',
        options: props.modelValue.type === 'movies' ? tmdbMovieGenres : tmdbTVGenres,
      },
      { label: '语言', key: 'with_original_language', options: tmdbLanguages },
    ];
  if (props.source === 'douban')
    return [
      { label: '类型', key: 'type', options: mediaTypes },
      { label: '排序', key: 'sort', options: doubanSort },
      { label: '风格', key: 'category', options: doubanGenres },
      { label: '地区', key: 'zone', options: doubanZones },
      { label: '年代', key: 'year', options: exploreYears('douban') },
    ];
  if (props.source === 'anilist')
    return [
      { label: '排序', key: 'sort', options: anilistSort },
      { label: '形式', key: 'format', options: anilistFormats },
      { label: '风格', key: 'genre', options: anilistGenres },
      { label: '季度', key: 'season', options: anilistSeasons },
      { label: '年份', key: 'season_year', options: anilistYears() },
      { label: '状态', key: 'status', options: anilistStatuses },
      { label: '地区', key: 'country', options: anilistCountries },
    ];
  return [
    { label: '类别', key: 'cat', options: bangumiCategories },
    { label: '排序', key: 'sort', options: bangumiSort },
    { label: '年份', key: 'year', options: exploreYears('bangumi') },
  ];
});
function change(key: keyof ExploreFilterState, value: unknown) {
  const next = { ...props.modelValue, [key]: value ?? '' };
  normalizeExploreFilters(props.source, next);
  emit('update:modelValue', next);
}
</script>

<template>
  <div class="explore-filters">
    <div v-for="row in rows" :key="row.key" class="explore-filter-row">
      <VLabel class="explore-filter-label">{{ row.label }}</VLabel>
      <VChipGroup
        :model-value="modelValue[row.key]"
        :aria-label="row.label"
        @update:model-value="change(row.key, $event)"
      >
        <VChip
          v-for="[value, title] in entries(row.options)"
          :key="value"
          :value="value"
          :color="modelValue[row.key] === value ? 'primary' : undefined"
          filter
          tile
          >{{ title }}</VChip
        >
      </VChipGroup>
    </div>
    <div v-if="source === 'themoviedb'" class="explore-filter-row explore-rating-row">
      <VLabel class="explore-filter-label">评分</VLabel>
      <VSlider
        :model-value="modelValue.vote_average"
        aria-label="最低评分"
        thumb-label
        :max="10"
        :min="0"
        :step="1"
        hide-details
        color="primary"
        @update:model-value="change('vote_average', $event)"
      >
        <template #append>
          <VTextField
            :model-value="modelValue.vote_count"
            aria-label="最低评分人数"
            title="最低评分人数"
            variant="outlined"
            width="5rem"
            density="compact"
            type="number"
            min="0"
            hide-details
            single-line
            @update:model-value="change('vote_count', $event)"
          />
        </template>
      </VSlider>
    </div>
  </div>
</template>

<style scoped>
.explore-filters {
  padding: 0 12px;
}
.explore-filter-row {
  display: flex;
  align-items: center;
  min-height: 48px;
}
.explore-filter-label {
  flex: none;
  margin-right: 20px;
}
.explore-filter-row :deep(.v-chip-group) {
  flex: 1;
  min-width: 0;
}
.explore-filter-row :deep(.v-chip) {
  height: 32px;
  padding: 0 12px;
  border-radius: 0;
  font-weight: 400;
}
.explore-rating-row :deep(.v-slider) {
  align-self: center;
  align-items: center;
}
.explore-rating-row :deep(.v-input__append) {
  margin-inline-start: 16px;
}
</style>
