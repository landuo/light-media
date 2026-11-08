<script setup lang="ts">
import { computed, onDeactivated, onUnmounted, ref, shallowRef } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VBottomSheet } from 'vuetify/components/VBottomSheet';
import { VCard, VCardActions, VCardItem, VCardText, VCardTitle } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { VChip } from 'vuetify/components/VChip';
import { VDivider } from 'vuetify/components/VDivider';
import { VIcon } from 'vuetify/components/VIcon';
import { VImg } from 'vuetify/components/VImg';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { VSwitch } from 'vuetify/components/VSwitch';
import { api, errorText, image, label, toast, ui } from '../api';
import { startSearch } from '../features/search-session';
import {
  cancelExploreSubscriptions,
  enabledExploreSites,
  exploreMovieIntent,
  exploreResolvePath,
  exploreResourceRoute,
  exploreSeasonRows,
  exploreSubscriptionBody,
  exploreSubscriptions,
  pendingExploreMovieMutation,
  rememberExploreSites,
  selectedExploreSites,
  submitExploreSeasons,
  tmdbExploreMedia,
  type ExploreAction,
  type ExploreActionMedia,
  type ExploreMovieIntent,
  type ExploreSeasonRow,
  type ExploreSite,
} from '../features/explore-actions';

type Action = ExploreAction | 'add-seasons';
const emit = defineEmits<{
  changed: [media: ExploreActionMedia];
  singleSeason: [media: ExploreActionMedia, season: number];
}>();
const display = useDisplay();
const visible = ref(false),
  phase = ref<'loading' | 'resolve' | 'sites' | 'seasons' | 'error'>('loading'),
  loading = ref(false),
  saving = ref(false),
  error = ref(''),
  action = ref<Action>('search'),
  original = shallowRef<ExploreActionMedia | null>(null),
  resolved = shallowRef<ExploreActionMedia | null>(null),
  candidates = ref<ExploreActionMedia[]>([]),
  sites = ref<ExploreSite[]>([]),
  selectedSites = ref<string[]>([]),
  seasons = ref<ExploreSeasonRow[]>([]);
let generation = 0;
let existing: any[] = [];
let movieIntent: ExploreMovieIntent | null = null;
let cancelSeasonIDs: string[] | null = null;
const pendingSeasons = computed(() =>
  seasons.value.filter((row) => row.selected && ['ready', 'failed'].includes(row.state)),
);
const showSeasonSheet = computed(
  () =>
    visible.value &&
    (phase.value === 'seasons' ||
      (action.value === 'add-seasons' && ['loading', 'error'].includes(phase.value))),
);
const title = computed(() =>
  phase.value === 'sites'
    ? '选择站点'
    : phase.value === 'resolve'
      ? '选择影视'
      : action.value !== 'search'
        ? `订阅 ${original.value?.title || ''}`
        : `搜索 ${original.value?.title || ''}`,
);

function close() {
  if (saving.value) return;
  hide();
}
function hide() {
  generation++;
  visible.value = false;
  loading.value = false;
}
function updateSubscription() {
  if (!original.value) return;
  const season = Number(original.value.season);
  const subscription =
    action.value !== 'add-seasons' && Number.isInteger(season) && season > 0
      ? existing.find((row) => Number(row.season) === season)
      : existing[0];
  original.value.subscription_id = subscription?.id || '';
  emit('changed', original.value);
}
function fail(cause: unknown, token: number) {
  if (token !== generation) return;
  error.value = errorText(cause);
  if (phase.value === 'loading') phase.value = 'error';
}
async function open(media: ExploreActionMedia, nextAction: Action) {
  if (saving.value) return;
  const token = ++generation;
  original.value = media;
  action.value = nextAction;
  resolved.value = null;
  candidates.value = [];
  sites.value = [];
  seasons.value = [];
  selectedSites.value = [];
  existing = [];
  movieIntent = null;
  cancelSeasonIDs = null;
  visible.value = true;
  loading.value = true;
  phase.value = 'loading';
  error.value = '';
  try {
    const direct = tmdbExploreMedia(media);
    if (direct) {
      await prepare(direct, token);
      return;
    }
    const result = await api(exploreResolvePath(media));
    if (token !== generation) return;
    const match = tmdbExploreMedia(result?.media);
    if (match) {
      await prepare(match, token);
      return;
    }
    candidates.value = (result?.candidates || []).flatMap((candidate: ExploreActionMedia) => {
      const candidateMedia = tmdbExploreMedia(candidate);
      return candidateMedia ? [candidateMedia] : [];
    });
    phase.value = 'resolve';
    if (!candidates.value.length) error.value = '未找到对应的影视条目，请稍后重试。';
  } catch (cause) {
    fail(cause, token);
  } finally {
    if (token === generation) loading.value = false;
  }
}
async function choose(candidate: ExploreActionMedia) {
  if (loading.value || saving.value) return;
  const token = generation;
  loading.value = true;
  error.value = '';
  try {
    await prepare(candidate, token);
  } catch (cause) {
    fail(cause, token);
  } finally {
    if (token === generation) loading.value = false;
  }
}
async function prepare(media: ExploreActionMedia, token: number) {
  if (token !== generation) return;
  resolved.value = media;
  phase.value = 'loading';
  if (action.value === 'search') {
    const response = await api('/sites/manage');
    if (token !== generation) return;
    sites.value = enabledExploreSites(response);
    selectedSites.value = selectedExploreSites(sites.value, ui.settings?.indexer_sites);
    phase.value = 'sites';
    return;
  }
  if (action.value === 'add-seasons') {
    const [response, rows] = await Promise.all([
      api('/subscriptions'),
      api('/catalog/seasons?' + new URLSearchParams({ id: String(media.id) })),
    ]);
    if (token !== generation) return;
    existing = exploreSubscriptions(media, response);
    seasons.value = exploreSeasonRows({ ...media, seasons: rows }, existing);
    updateSubscription();
    const soleSeason = seasons.value[0];
    if (seasons.value.length === 1 && soleSeason?.state === 'ready' && original.value) {
      emit('singleSeason', original.value, soleSeason.season);
      hide();
    } else {
      phase.value = 'seasons';
    }
    return;
  }
  const response = await api('/subscriptions');
  if (token !== generation) return;
  existing = exploreSubscriptions(media, response);
  if (media.media_type === 'movie') {
    movieIntent = exploreMovieIntent(existing, movieIntent);
    const mutation = pendingExploreMovieMutation(movieIntent, existing);
    // The explicit heart click (or identity selection) authorizes this change.
    saving.value = true;
    try {
      if (mutation === 'cancel') {
        const result = await api(
          '/subscriptions/' + encodeURIComponent(movieIntent.id!),
          undefined,
          'DELETE',
        );
        if (!result?.cancelled) throw new Error('取消订阅的结果未确认，请刷新后重试');
        existing = existing.filter((row) => row.id !== movieIntent!.id);
      } else if (mutation === 'create') {
        const subscription = await api('/subscriptions', exploreSubscriptionBody(media));
        if (!subscription?.id) throw new Error('订阅保存结果未确认，请刷新后重试');
        existing = [subscription];
      }
      updateSubscription();
      toast(movieIntent.action === 'cancel' ? '订阅已取消' : '订阅已添加');
      visible.value = false;
    } finally {
      saving.value = false;
    }
    return;
  }
  if (cancelSeasonIDs === null) {
    const season = Number(original.value?.season);
    cancelSeasonIDs = existing
      .filter((row) => !Number.isInteger(season) || season <= 0 || Number(row.season) === season)
      .map((row) => String(row.id));
  }
  if (cancelSeasonIDs.length) {
    saving.value = true;
    try {
      const result = await cancelExploreSubscriptions(
        cancelSeasonIDs,
        existing,
        (id) => api('/subscriptions/' + encodeURIComponent(id), undefined, 'DELETE'),
        (remaining) => {
          existing = remaining;
          updateSubscription();
        },
        errorText,
      );
      existing = result.remaining;
      updateSubscription();
      if (result.failures.length)
        throw new Error('部分订阅未取消，重试将仅处理剩余订阅。' + result.failures.join('；'));
      toast('订阅已取消');
      visible.value = false;
    } finally {
      saving.value = false;
    }
    return;
  }
  const detail = await api(
    '/catalog/detail?' + new URLSearchParams({ kind: 'tv', id: String(media.id) }),
  );
  if (token !== generation) return;
  if (
    !tmdbExploreMedia(detail) ||
    Number(detail.id) !== Number(media.id) ||
    detail.media_type !== 'tv'
  )
    throw new Error('季集信息与当前影视不匹配');
  resolved.value = detail;
  seasons.value = exploreSeasonRows(detail, existing);
  phase.value = 'seasons';
  updateSubscription();
}
function retry() {
  if (loading.value || saving.value) return;
  // Reuse the resolved identity and original operation after an uncertain write.
  if (resolved.value) void choose(resolved.value);
  else if (original.value) void open(original.value, action.value);
}
function toggleSites() {
  selectedSites.value =
    selectedSites.value.length < sites.value.length ? sites.value.map((site) => site.id) : [];
}
function toggleSite(id: string) {
  selectedSites.value = selectedSites.value.includes(id)
    ? selectedSites.value.filter((value) => value !== id)
    : [...selectedSites.value, id];
}
function search() {
  if (!resolved.value || loading.value || saving.value) return;
  try {
    const route = exploreResourceRoute(resolved.value, selectedSites.value, sites.value);
    rememberExploreSites(selectedSites.value, sites.value);
    close();
    startSearch(route);
  } catch (cause) {
    error.value = errorText(cause);
  }
}
function toggleSeason(row: ExploreSeasonRow) {
  if (saving.value || !['ready', 'failed'].includes(row.state)) return;
  row.selected = !row.selected;
}
async function subscribeSeasons() {
  if (!resolved.value || saving.value || !pendingSeasons.value.length) return;
  const token = generation;
  saving.value = true;
  error.value = '';
  try {
    await submitExploreSeasons(
      seasons.value,
      resolved.value,
      (body) => api('/subscriptions', body),
      (subscription) => {
        existing.push(subscription);
        updateSubscription();
      },
      errorText,
    );
    if (token !== generation) return;
    const failed = seasons.value.filter((row) => row.state === 'failed').length;
    const conflicts = seasons.value.filter(
      (row) => row.selected && row.state === 'conflict',
    ).length;
    const succeeded = seasons.value.filter((row) => row.state === 'success').length;
    if (failed || conflicts) {
      error.value =
        [
          `已成功订阅 ${succeeded} 季`,
          failed ? `${failed} 季失败，可重试` : '',
          conflicts ? `${conflicts} 季已有订阅或发生冲突` : '',
        ]
          .filter(Boolean)
          .join('，') + '。';
    } else {
      toast('所选季度已订阅');
      visible.value = false;
    }
  } catch (cause) {
    if (token === generation) error.value = errorText(cause);
  } finally {
    saving.value = false;
  }
}
onDeactivated(hide);
onUnmounted(() => generation++);
defineExpose({ open });
</script>

<template>
  <Teleport to="body"
    ><VProgressLinear
      v-if="visible && !showSeasonSheet && phase === 'loading' && (loading || saving)"
      class="explore-action-progress"
      indeterminate
      color="primary"
      aria-label="正在处理"
  /></Teleport>
  <VDialog
    :model-value="visible && !showSeasonSheet && phase !== 'loading'"
    :persistent="saving"
    :fullscreen="!display.smAndUp.value"
    max-width="40rem"
    scrollable
    content-class="explore-action-dialog"
    @update:model-value="close"
  >
    <VCard>
      <VCardItem>
        <template #prepend
          ><VIcon
            :icon="
              phase === 'sites'
                ? 'mdi-web-check'
                : action === 'search'
                  ? 'mdi-magnify'
                  : 'mdi-heart-outline'
            "
        /></template>
        <VCardTitle>{{ title }}</VCardTitle>
        <template #append
          ><VBtn
            icon="mdi-close"
            variant="text"
            :disabled="saving"
            aria-label="关闭"
            @click="close"
        /></template>
      </VCardItem>
      <VDivider />
      <VProgressLinear v-if="loading || saving" indeterminate color="primary" />
      <VCardText class="explore-action-content">
        <VAlert v-if="error" type="error" variant="tonal" class="mb-4"
          >{{ error
          }}<VBtn v-if="!loading && !saving" variant="text" @click="retry">重试</VBtn></VAlert
        >
        <p v-if="phase === 'loading'" role="status">{{ saving ? '正在保存…' : '正在加载…' }}</p>
        <template v-if="phase === 'sites'">
          <div v-if="sites.length" class="site-selection-header">
            <VBtn
              rounded="pill"
              size="small"
              :color="selectedSites.length < sites.length ? 'primary' : 'error'"
              @click="toggleSites"
              >{{ selectedSites.length < sites.length ? '全选' : '全不选' }}</VBtn
            >
            <span>已选 {{ selectedSites.length }} / {{ sites.length }} 个站点</span>
          </div>
          <div v-if="sites.length" class="site-selection-grid">
            <VBtn
              v-for="site in sites"
              :key="site.id"
              class="site-choice"
              :class="{ 'site-selected': selectedSites.includes(site.id) }"
              variant="outlined"
              color="secondary"
              :aria-pressed="selectedSites.includes(site.id)"
              @click="toggleSite(site.id)"
            >
              <VIcon
                :icon="
                  selectedSites.includes(site.id)
                    ? 'mdi-check-circle'
                    : 'mdi-checkbox-blank-circle-outline'
                "
                :color="selectedSites.includes(site.id) ? 'primary' : undefined"
                size="small"
              />
              <span>{{ site.name }}</span>
            </VBtn>
          </div>
          <div v-else class="empty-action">
            <VIcon icon="mdi-alert-circle-outline" color="warning" size="32" />
            <p>暂无已启用站点</p>
            <VBtn variant="text" @click="retry">重新加载</VBtn>
          </div>
        </template>
        <template v-if="phase === 'resolve' && candidates.length">
          <p class="text-medium-emphasis">请选择对应的影视条目。</p>
          <VList>
            <VListItem
              v-for="candidate in candidates"
              :key="candidate.media_type + '/' + candidate.id"
              :disabled="loading || saving"
              @click="choose(candidate)"
            >
              <template #prepend
                ><VImg
                  v-if="image(candidate.poster_path)"
                  :src="image(candidate.poster_path, 'w92')"
                  width="48"
                  height="72"
                  class="me-3 rounded"
                  cover
              /></template>
              <VListItemTitle>{{ candidate.title }}</VListItemTitle>
              <VListItemSubtitle
                >{{ candidate.year || '年份待定' }} ·
                {{ label(candidate.media_type) }}</VListItemSubtitle
              >
              <VListItemSubtitle v-if="candidate.original_title">{{
                candidate.original_title
              }}</VListItemSubtitle>
            </VListItem>
          </VList>
        </template>
      </VCardText>
      <VCardActions v-if="phase === 'sites'" class="explore-action-footer"
        ><VBtn
          variant="flat"
          color="primary"
          prepend-icon="mdi-magnify"
          :disabled="!selectedSites.length || loading"
          @click="search"
          >搜索</VBtn
        ></VCardActions
      >
    </VCard>
  </VDialog>
  <VBottomSheet
    :model-value="showSeasonSheet"
    :persistent="saving"
    inset
    scrollable
    aria-label="选择订阅季度"
    @update:model-value="close"
  >
    <VCard class="explore-season-card">
      <VCardItem
        ><VCardTitle>订阅 {{ original?.title }}</VCardTitle
        ><template #append
          ><VBtn
            icon="mdi-close"
            variant="text"
            :disabled="saving"
            aria-label="关闭"
            @click="close" /></template
      ></VCardItem>
      <VDivider />
      <VProgressLinear v-if="loading || saving" indeterminate color="primary" />
      <VCardText class="explore-season-content">
        <VAlert v-if="error" type="error" variant="tonal" class="mb-3">{{ error }}</VAlert>
        <p v-if="loading" role="status">正在加载季集信息…</p>
        <VBtn v-else-if="phase === 'error'" variant="text" @click="retry">重试</VBtn>
        <VList v-else-if="seasons.length" lines="three">
          <VListItem
            v-for="row in seasons"
            :key="row.season"
            :active="row.selected"
            :disabled="saving || row.state === 'success' || row.state === 'conflict'"
            color="primary"
            @click="toggleSeason(row)"
          >
            <template #prepend
              ><VImg
                :src="image(row.poster_path || '', 'w185')"
                width="60"
                height="90"
                class="me-3 rounded"
                cover
            /></template>
            <VListItemTitle>第 {{ row.season }} 季</VListItemTitle>
            <VListItemSubtitle
              >{{ row.air_date?.slice(0, 4) || '年份待定' }} ·
              {{ row.episode_count ? row.episode_count + ' 集' : '集数待定' }}</VListItemSubtitle
            >
            <VListItemSubtitle>播出日期：{{ row.air_date || '待定' }}</VListItemSubtitle>
            <VListItemSubtitle
              v-if="row.state === 'conflict' || row.state === 'success' || row.state === 'failed'"
            >
              <VChip
                size="small"
                class="mt-1"
                :color="row.state === 'failed' ? 'error' : 'success'"
                >{{
                  row.state === 'success'
                    ? '订阅成功'
                    : row.state === 'failed'
                      ? '添加失败'
                      : row.subscription_state
                        ? '已订阅 · ' + label(row.subscription_state)
                        : '已有订阅'
                }}</VChip
              >
            </VListItemSubtitle>
            <div
              v-if="row.state === 'failed' || (row.state === 'conflict' && row.selected)"
              class="season-error"
            >
              {{ row.message }}
            </div>
            <template #append
              ><VSwitch
                :model-value="row.selected || row.state === 'success' || row.state === 'conflict'"
                :disabled="saving || row.state === 'success' || row.state === 'conflict'"
                hide-details
                :aria-label="`选择第 ${row.season} 季`"
                @click.stop="toggleSeason(row)"
            /></template>
          </VListItem>
        </VList>
        <div v-else class="empty-action">
          未查询到可订阅的季集信息。
          <VBtn v-if="action === 'add-seasons'" variant="text" @click="retry">重试</VBtn>
        </div>
      </VCardText>
      <VCardActions class="season-action-footer"
        ><VBtn
          variant="flat"
          color="primary"
          :loading="saving"
          :disabled="!pendingSeasons.length || saving"
          @click="subscribeSeasons"
          >{{
            pendingSeasons.length
              ? '提交订阅（' + pendingSeasons.length + ' 季）'
              : '请选择订阅季度'
          }}</VBtn
        ></VCardActions
      >
    </VCard>
  </VBottomSheet>
</template>

<style scoped>
.explore-action-progress {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 2700;
}
.explore-action-content {
  max-height: 420px;
  overflow-y: auto;
  padding: 16px;
}
.site-selection-header {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 16px;
}
.site-selection-header span {
  font-size: 14px;
  color: var(--muted);
}
.site-selection-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}
.site-choice {
  justify-content: flex-start;
  min-width: 0;
  height: 48px;
  padding: 8px 12px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
  border-radius: 8px;
  text-transform: none;
}
.site-choice :deep(.v-btn__content) {
  width: 100%;
  justify-content: flex-start;
  gap: 8px;
}
.site-choice span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.site-selected {
  color: rgb(var(--v-theme-primary)) !important;
  border-color: rgba(var(--v-theme-primary), 0.2);
  background: rgba(var(--v-theme-primary), 0.08);
}
.explore-action-footer {
  justify-content: flex-end;
  padding: 12px 20px;
}
.explore-season-card {
  max-height: 85vh;
}
.explore-season-content {
  overflow-y: auto;
}
.season-action-footer {
  justify-content: center;
  padding: 12px;
}
.season-action-footer .v-btn {
  min-width: 30%;
}
.empty-action {
  padding: 32px 0;
  text-align: center;
  color: var(--muted);
}
.season-error {
  margin-top: 6px;
  color: rgb(var(--v-theme-error));
  font-size: 12px;
}
@media (max-width: 599px) {
  .site-selection-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .explore-action-content {
    max-height: none;
  }
}
</style>
