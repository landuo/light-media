<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VChip } from 'vuetify/components/VChip';
import {
  VExpansionPanel,
  VExpansionPanels,
  VExpansionPanelText,
  VExpansionPanelTitle,
} from 'vuetify/components/VExpansionPanel';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { VIcon } from 'vuetify/components/VIcon';
import { api, errorText, image } from '../api';

type SeasonSummary = {
  season_number: number;
  name?: string | null;
  episode_count?: number | null;
};
type Episode = {
  season_number: number;
  episode_number: number;
  name: string;
  overview?: string;
  air_date?: string;
  still_path?: string;
};
type EpisodeGroup = { id: string; name: string; group_count?: number; episode_count?: number };
type GroupSeason = { order: number; name: string; episodes: Episode[] };
type SeasonPanel = { number: number; title: string; count?: number | null; episodes?: Episode[] };

const props = defineProps<{
  seasons: SeasonSummary[];
  seasonData?: (SeasonSummary & { episodes: Episode[] }) | null;
  loading: boolean;
  mediaId?: number;
  groups?: EpisodeGroup[] | null;
}>();
const season = defineModel<number>('season', { required: true });
const selected = defineModel<number[]>('selected', { required: true });
const emit = defineEmits<{ 'change-season': []; retry: [] }>();

const expanded = ref<number | null>(null);
const selectedGroup = ref('');
const groupSeasons = ref<GroupSeason[]>([]);
const groupLoading = ref(false);
const groupError = ref('');
let groupRequest = 0;

const groupOptions = computed(() => [
  {
    title: '默认',
    value: '',
    seasonCount: props.seasons.length,
    episodeCount: props.seasons.reduce((sum, row) => sum + (row.episode_count || 0), 0),
  },
  ...(Array.isArray(props.groups) ? props.groups : [])
    .filter((group) => group?.id && group.name)
    .map((group) => ({
      title: group.name,
      value: group.id,
      seasonCount: group.group_count,
      episodeCount: group.episode_count,
    })),
]);
const activeGroup = computed(() =>
  groupOptions.value.find((option) => option.value === selectedGroup.value),
);
const groupCount = (option: (typeof groupOptions.value)[number]) =>
  option.seasonCount != null && option.episodeCount != null
    ? `${option.seasonCount} 季 · ${option.episodeCount} 集`
    : '';
const standardPanels = computed<SeasonPanel[]>(() => {
  const rows = props.seasons.length ? props.seasons : props.seasonData ? [props.seasonData] : [];
  return rows.map((row) => ({
    number: row.season_number,
    title: row.season_number === 0 ? row.name || '特别篇' : `第 ${row.season_number} 季`,
    count:
      row.episode_count ?? (row === props.seasonData ? props.seasonData.episodes.length : null),
  }));
});
const panels = computed<SeasonPanel[]>(() =>
  selectedGroup.value
    ? groupSeasons.value.map((row) => ({
        number: row.order,
        title: row.name || `第 ${row.order + 1} 组`,
        count: row.episodes.length,
        episodes: row.episodes,
      }))
    : standardPanels.value,
);
const episodes = (panel: SeasonPanel) =>
  panel.episodes ??
  (panel.number === props.seasonData?.season_number ? props.seasonData.episodes : []);

function setExpanded(value: unknown) {
  expanded.value = typeof value === 'number' ? value : null;
  if (typeof value !== 'number' || selectedGroup.value || value === season.value) return;
  season.value = value;
  emit('change-season');
}

async function loadGroup() {
  const request = ++groupRequest;
  groupSeasons.value = [];
  groupError.value = '';
  groupLoading.value = !!(props.mediaId && selectedGroup.value);
  if (!props.mediaId || !selectedGroup.value) return;
  try {
    const query = new URLSearchParams({ id: String(props.mediaId), group: selectedGroup.value });
    const result = await api<{ groups: GroupSeason[] }>('/catalog/episode-group?' + query);
    if (request === groupRequest) groupSeasons.value = result.groups;
  } catch (error) {
    if (request === groupRequest) groupError.value = errorText(error);
  } finally {
    if (request === groupRequest) groupLoading.value = false;
  }
}
watch(selectedGroup, () => {
  expanded.value = null;
  void loadGroup();
});
onUnmounted(() => ++groupRequest);
</script>

<template>
  <section class="media-detail-seasons" aria-label="季">
    <h2>季</h2>
    <template v-if="mediaId && groupOptions.length > 1">
      <p class="hint episode-group-label">选择剧集组</p>
      <div class="episode-group-picker" role="group" aria-label="选择剧集组">
        <button
          v-for="option in groupOptions"
          :key="option.value"
          type="button"
          :aria-pressed="selectedGroup === option.value"
          @click="selectedGroup = option.value"
        >
          <VIcon v-if="option.value" icon="mdi-folder-play-outline" size="20" aria-hidden="true" />
          <VIcon v-else icon="mdi-layers-triple-outline" size="20" aria-hidden="true" />
          <span>
            <strong>{{ option.title }}</strong>
            <small v-if="groupCount(option)">{{ groupCount(option) }}</small>
          </span>
        </button>
      </div>
      <p v-if="activeGroup" class="hint episode-group-current">
        当前：{{ activeGroup.title
        }}{{ groupCount(activeGroup) ? ' · ' + groupCount(activeGroup) : '' }}
      </p>
    </template>

    <template v-if="selectedGroup">
      <VProgressLinear v-if="groupLoading" color="primary" indeterminate aria-label="加载剧集组" />
      <p v-else-if="groupError" class="hint">
        {{ groupError }}
        <VBtn variant="text" size="small" @click="loadGroup">重试加载剧集组</VBtn>
      </p>
      <p v-else-if="!panels.length" class="hint">该组暂无剧集</p>
      <p v-else class="hint">按集搜索请切换至标准季集。</p>
    </template>
    <template v-else-if="!panels.length">
      <VProgressLinear v-if="loading" color="primary" indeterminate aria-label="加载季度" />
      <p v-else class="hint">
        暂无季度资料
        <VBtn variant="text" size="small" @click="emit('retry')">重试加载本季</VBtn>
      </p>
    </template>

    <VExpansionPanels
      v-if="panels.length"
      :model-value="expanded"
      @update:model-value="setExpanded"
    >
      <VExpansionPanel v-for="panel in panels" :key="panel.number" :value="panel.number">
        <VExpansionPanelTitle>
          <div class="season-title">
            <strong>{{ panel.title }}</strong>
            <VChip v-if="panel.count != null" size="small">{{ panel.count }}集</VChip>
          </div>
        </VExpansionPanelTitle>
        <VExpansionPanelText>
          <template v-if="selectedGroup || panel.number === season">
            <VProgressLinear
              v-if="!selectedGroup && loading"
              color="primary"
              indeterminate
              aria-label="加载本季剧集"
            />
            <VBtn
              v-else-if="!selectedGroup && !seasonData"
              variant="text"
              size="small"
              @click="emit('retry')"
            >
              重试加载本季
            </VBtn>
            <p v-else-if="!episodes(panel).length" class="hint">暂无剧集资料</p>
            <template v-else>
              <p v-if="!selectedGroup" class="hint">可选择本季剧集搜索资源。</p>
              <div
                v-for="(episode, index) in episodes(panel)"
                :key="`${episode.season_number}:${episode.episode_number}:${index}`"
                class="season-episode"
              >
                <div class="episode-copy">
                  <div class="episode-heading">
                    <label v-if="!selectedGroup" class="episode-selection">
                      <input v-model="selected" type="checkbox" :value="episode.episode_number" />
                      <strong>{{ episode.episode_number }} - {{ episode.name }}</strong>
                    </label>
                    <strong v-else>{{ index + 1 }} - {{ episode.name }}</strong>
                    <VChip v-if="episode.air_date" size="x-small">{{ episode.air_date }}</VChip>
                  </div>
                  <p v-if="episode.overview">{{ episode.overview }}</p>
                </div>
                <img
                  v-if="episode.still_path && image(episode.still_path)"
                  :src="image(episode.still_path)"
                  loading="lazy"
                  alt=""
                />
              </div>
            </template>
          </template>
        </VExpansionPanelText>
      </VExpansionPanel>
    </VExpansionPanels>
  </section>
</template>

<style scoped>
.media-detail-seasons {
  margin-block: 16px;
  min-width: 0;
}
.season-title,
.episode-heading,
.episode-selection {
  display: flex;
  align-items: center;
  gap: 8px;
}
.episode-group-label {
  margin-block: 16px 8px;
}
.episode-group-picker {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 132px), 1fr));
  gap: 8px;
}
.episode-group-picker button {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  padding: 8px 12px;
  border: 1px solid var(--line);
  border-radius: 6px;
  color: var(--text);
  background: var(--surface);
  text-align: start;
}
.episode-group-picker button[aria-pressed='true'] {
  border-color: var(--accent);
  color: var(--accent);
  background: var(--accent-soft);
}
.episode-group-picker small {
  display: block;
  opacity: 0.8;
}
.episode-group-current {
  margin-block: 12px;
}
.season-episode {
  display: flex;
  gap: 16px;
  padding-block: 16px;
}
.season-episode + .season-episode {
  border-top: 1px solid var(--line);
}
.episode-copy {
  flex: 1;
  min-width: 0;
}
.episode-heading {
  flex-wrap: wrap;
}
.episode-selection {
  cursor: pointer;
}
.episode-selection input {
  margin: 0;
  accent-color: var(--accent);
}
.episode-copy p {
  margin: 8px 0 0;
}
.season-episode img {
  align-self: flex-start;
  border-radius: 8px;
  width: min(34%, 15rem);
  aspect-ratio: 16 / 9;
  object-fit: cover;
}
@media (max-width: 680px) {
  .season-episode {
    flex-direction: column;
  }
  .season-episode img {
    width: min(100%, 15rem);
  }
}
</style>
