<script setup lang="ts">
import { VSelect } from 'vuetify/components/VSelect';
import { VTextField } from 'vuetify/components/VTextField';
import { VBtn } from 'vuetify/components/VBtn';
import { VTooltip } from 'vuetify/components/VTooltip';
import { computed, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue';
import { api, errorText, image, label, toast, ui } from '../api';
import { boundedRead } from '../features/bounded-read';
import FileManagerIcon from './FileManagerIcon.vue';
import DirectoryPathPicker from './DirectoryPathPicker.vue';

type OrganizeResultItem = {
  source: string;
  target?: string;
  status?: string;
  error?: string;
  reason?: string;
  mode?: string;
  job_id?: string;
  media?: any;
};
type OrganizeResponse = {
  items: OrganizeResultItem[];
  total: number;
  submitted: number;
  failed: number;
  preparation_job_id?: string;
};
const props = defineProps<{
  paths: string[];
  items?: { path: string; type: string }[];
  historyIds?: string[];
  initial?: {
    target_path?: string;
    media_type?: string;
    tmdb_id?: number;
    season?: number;
    [key: string]: any;
  };
}>();
const emit = defineEmits<{ close: []; done: [preparationJobID?: string]; historyChanged: [] }>();
const targetListID = 'organize-targets-' + useId();
const storage = ref(props.initial?.target_path ? 'local' : ''),
  mode = ref(''),
  targetPath = ref(props.initial?.target_path || '');
const localSettings = ref<any>(null);
const mediaSource = computed(
  () =>
    (ui.settings || localSettings.value)?.recognition_source ||
    (ui.settings || localSettings.value)?.effective_recognition_source ||
    'themoviedb',
);
const mediaSourceReady = computed(() => !!(ui.settings || localSettings.value));
const mediaSourceLabel = computed(
  () =>
    (
      ({
        themoviedb: 'TheMovieDb',
        douban: '豆瓣',
        bangumi: 'Bangumi',
        anilist: 'AniList',
      }) as Record<string, string>
    )[String(mediaSource.value)] || '媒体',
);
const mediaType = ref(props.initial?.media_type || ''),
  mediaID = ref(props.initial?.tmdb_id ? String(props.initial.tmdb_id) : '');
type EpisodeGroup = { id: string; name: string; group_count: number; episode_count: number };
// No client-side history inheritance: the explicit FromHistory option is handled server-side.
const episodeGroup = ref<string | null>(null),
  episodeGroups = ref<EpisodeGroup[]>([]),
  episodeGroupLoading = ref(false),
  episodeGroupError = ref('');
const episodeGroupTMDBID = computed(() => {
  const id = mediaID.value.trim();
  const value = Number(id);
  return mediaSource.value === 'themoviedb' &&
    mediaType.value === 'tv' &&
    /^\d+$/.test(id) &&
    Number.isSafeInteger(value) &&
    value > 0
    ? value
    : null;
});
const episodeGroupOptions = computed(() => [
  { title: '不指定剧集组', subtitle: '使用 TMDB 默认季集排序', value: null },
  ...episodeGroups.value.map((group) => ({
    title: group.name,
    subtitle: `${group.group_count} 季 • ${group.episode_count} 集`,
    value: group.id,
  })),
]);
const episodeGroupItemProps = (item: { title: string; subtitle: string }) => ({
  title: item.title,
  subtitle: item.subtitle,
});
let episodeGroupTimer: ReturnType<typeof setTimeout> | undefined;
let episodeGroupRead: AbortController | undefined;
let episodeGroupGeneration = 0;
const season = ref<number | ''>(props.initial?.season ?? ''),
  episodeDetail = ref(''),
  episodeFormat = ref(''),
  episodeOffset = ref(''),
  episodePart = ref(''),
  minFilesize = ref(0);
const scraping = ref<boolean | null>(props.initial?.target_path ? false : null),
  typeFolder = ref<boolean | null>(null),
  fromHistory = ref(false);
const busy = ref(false),
  previewBusy = ref(false),
  previewVisible = ref(false),
  resultIsPreview = ref(true),
  error = ref('');
const result = ref<OrganizeResponse | null>(null),
  progress = ref(''),
  completed = ref(0),
  totalJobs = ref(0);
const picker = ref(false),
  query = ref(''),
  searchBusy = ref(false),
  searchError = ref(''),
  searchResults = ref<any[]>([]);
const subtitle = computed(() =>
  props.historyIds?.length
    ? `共 ${props.historyIds.length} 项`
    : props.paths.length === 1
      ? props.paths[0]
      : `共 ${props.paths.length} 项`,
);
const settingsRead = new AbortController();
const targetPaths = computed(
  () =>
    [
      ...new Set(
        ((ui.settings || localSettings.value)?.directories || [])
          .map((directory: any) => directory.library_path)
          .filter(Boolean),
      ),
    ] as string[],
);
const episodeDetailDisabled = computed(
  () =>
    !props.historyIds?.length &&
    !!props.paths.length &&
    !episodeFormat.value &&
    (props.paths.length !== 1 || props.items?.some((item) => item.type === 'dir')),
);
type EpisodeRecommendation = {
  episode_format: string;
  rule_name: string;
  pattern: string | null;
  sample_file: string;
  sample_count: number;
  matched_count: number;
  majority_count?: number;
  confidence?: 'high' | 'low';
  size_filter_relaxed?: boolean;
  native_verified_count?: number;
  native_fallback_count?: number;
  native_conflict_count?: number;
  reason?: string;
  reasons?: string[];
  message: string;
};
const recommendBusy = ref(false),
  recommendConfigured = ref<boolean | null>(null),
  recommendError = ref(''),
  recommendation = ref<EpisodeRecommendation | null>(null);
const recommendRead = new AbortController();
const recommendationSelection = computed(() =>
  JSON.stringify([props.paths, props.items, props.historyIds]),
);
const recommendSelectionValid = computed(() => {
  if (props.historyIds?.length) return true; // Resolve the current source server-side, never trust display paths.
  if (!props.paths.length) return false;
  if (props.paths.length === 1) return true;
  if (props.items?.some((item) => item.type !== 'file')) return false;
  return new Set(props.paths.map((path) => path.slice(0, path.lastIndexOf('/')))).size === 1;
});
const recommendNeedsRules = '手动整理集数定位规则为空，请先前往设置的规则页填写';
const recommendTooltip = computed(() => {
  if (recommendBusy.value) return '正在生成集数定位';
  if (!recommendSelectionValid.value) return '请选择单个文件、目录或同目录的多个文件';
  if (recommendConfigured.value === false) return recommendNeedsRules;
  return '优先匹配配置规则，未命中时智能分析；单文件使用同目录样本，多选仅使用所选文件';
});
const episodeFormatHint = computed(() => {
  if (recommendError.value) return recommendError.value;
  const data = recommendation.value;
  if (data && episodeFormat.value === data.episode_format) {
    const details = [`规则：${data.rule_name}`, `样本：${data.sample_file}`];
    if (data.confidence) details.push(`置信度：${data.confidence === 'low' ? '低' : '高'}`);
    details.push(
      data.majority_count != null
        ? `多数派 ${data.majority_count}/${data.sample_count} 个文件`
        : `规则命中 ${data.matched_count}/${data.sample_count} 个文件`,
    );
    if (data.size_filter_relaxed) details.push('已放宽体积限制');
    if (data.native_verified_count) details.push(`原生验证 ${data.native_verified_count} 项`);
    if (data.native_fallback_count) details.push(`原生回退 ${data.native_fallback_count} 项`);
    if (data.message) details.push(data.message);
    return details.join(' · ');
  }
  return '使用{ep}定位文件名中的集数部分以辅助识别';
});
watch(recommendationSelection, () => {
  recommendation.value = null;
  recommendError.value = '';
});
async function loadEpisodeRules() {
  try {
    const data = await boundedRead(
      (signal) => api('/files/episode-format/rules', undefined, 'GET', signal),
      recommendRead.signal,
    );
    // MP checks table length, not whether any rule is enabled. A nonempty,
    // fully disabled table reaches the backend's automatic fallback.
    if (alive) recommendConfigured.value = !!data.rules?.length;
  } catch (e) {
    // An unsuccessful config read must not look like an empty configuration.
    if (alive) recommendError.value = errorText(e);
  }
}
async function recommendEpisode() {
  if (busy.value || previewBusy.value || recommendBusy.value || !recommendSelectionValid.value)
    return;
  // Preserve MP's UI/backend asymmetry: an empty table shows a reminder on
  // click, although the backend independently supports an empty-table fallback.
  if (recommendConfigured.value === false) {
    toast(recommendNeedsRules);
    return;
  }
  recommendBusy.value = true;
  recommendError.value = '';
  const selection = recommendationSelection.value,
    previousFormat = episodeFormat.value,
    previousType = mediaType.value;
  try {
    const data = await boundedRead(
      (signal) =>
        api<EpisodeRecommendation>(
          '/files/episode-format/recommend',
          props.historyIds?.length ? { history_ids: props.historyIds } : { paths: props.paths },
          'POST',
          signal,
        ),
      recommendRead.signal,
    );
    if (
      !alive ||
      selection !== recommendationSelection.value ||
      previousFormat !== episodeFormat.value ||
      previousType !== mediaType.value
    )
      return;
    if (!data.episode_format) throw new Error('推荐未返回有效的集数定位模板');
    episodeFormat.value = data.episode_format;
    recommendation.value = data;
    toast(previousFormat.trim() ? '已覆盖集数定位，其他选项保持不变' : '已填入推荐集数定位');
  } catch (e) {
    if (alive && selection === recommendationSelection.value) recommendError.value = errorText(e);
  } finally {
    recommendBusy.value = false;
  }
}
const previewRows = computed(() => result.value?.items || []);
const formScroll = ref<HTMLElement | null>(null),
  previewPanel = ref<HTMLElement | null>(null);
watch([previewVisible, result], async ([visible, response]) => {
  if (!visible || !response) return;
  await nextTick();
  const content = formScroll.value,
    panel = previewPanel.value;
  if (content && panel)
    content.scrollTo({
      top:
        content.scrollTop + panel.getBoundingClientRect().top - content.getBoundingClientRect().top,
      behavior: 'smooth',
    });
});
const failedRows = computed(() =>
  previewRows.value.filter((item) => item.status === 'failed' || item.status === 'blocked'),
);
let alive = true;
let sleepTimer: ReturnType<typeof setTimeout> | undefined;
let wake: (() => void) | undefined;

function close() {
  emit('close');
}
function resetPreview() {
  result.value = null;
  error.value = '';
}
watch([mediaType, mediaSource], () => {
  mediaID.value = '';
  season.value = '';
  episodeDetail.value = '';
  resetPreview();
});
async function loadEpisodeGroups(id: number, generation: number, controller: AbortController) {
  episodeGroupLoading.value = true;
  try {
    const groups = await boundedRead(
      (signal) => api<EpisodeGroup[]>(`/catalog/episode-groups?id=${id}`, undefined, 'GET', signal),
      controller.signal,
    );
    if (alive && generation === episodeGroupGeneration && episodeGroupTMDBID.value === id)
      episodeGroups.value = groups;
  } catch (e) {
    if (alive && generation === episodeGroupGeneration && episodeGroupTMDBID.value === id)
      episodeGroupError.value = errorText(e);
  } finally {
    if (alive && generation === episodeGroupGeneration) episodeGroupLoading.value = false;
  }
}
watch(
  [mediaType, mediaID],
  () => {
    const generation = ++episodeGroupGeneration;
    if (episodeGroupTimer) clearTimeout(episodeGroupTimer);
    episodeGroupRead?.abort();
    episodeGroup.value = null;
    episodeGroups.value = [];
    episodeGroupError.value = '';
    episodeGroupLoading.value = false;
    const id = episodeGroupTMDBID.value;
    if (!id) return;
    const controller = new AbortController();
    episodeGroupRead = controller;
    // MP debounces typed TMDB IDs by 400ms; all stale results are additionally isolated.
    episodeGroupTimer = setTimeout(() => {
      episodeGroupTimer = undefined;
      void loadEpisodeGroups(id, generation, controller);
    }, 400);
  },
  { immediate: true },
);
watch(
  [
    storage,
    mode,
    targetPath,
    mediaID,
    episodeGroup,
    season,
    episodeDetail,
    episodeFormat,
    episodeOffset,
    episodePart,
    minFilesize,
    scraping,
    typeFolder,
    fromHistory,
  ],
  resetPreview,
);
function payload(preview: boolean, background = false) {
  const id = mediaID.value.trim();
  if (id && (!/^\d+$/.test(id) || Number(id) < 1 || !mediaType.value))
    throw new Error(`请选择类型并填写有效的 ${mediaSourceLabel.value} 编号`);
  if (id && !mediaSourceReady.value) throw new Error('识别来源配置尚未加载');
  if (!Number.isInteger(Number(minFilesize.value)) || Number(minFilesize.value) < 0)
    throw new Error('最小文件大小须为不小于 0 的整数');
  return {
    paths: props.paths,
    ...(props.historyIds?.length ? { history_ids: props.historyIds } : {}),
    target_storage: storage.value,
    target_path: targetPath.value.trim(),
    mode: mode.value,
    media_type: mediaType.value,
    tmdb_id: mediaSource.value === 'themoviedb' && id ? Number(id) : 0,
    ...(mediaSource.value !== 'themoviedb'
      ? { media_source: mediaSource.value, media_id: id }
      : {}),
    ...(mediaType.value === 'tv'
      ? {
          ...(mediaSource.value === 'themoviedb'
            ? {
                episode_group: episodeGroupTMDBID.value ? episodeGroup.value?.trim() || null : null,
              }
            : {}),
          ...(season.value === '' ? {} : { season: Number(season.value) }),
          episode_detail: episodeDetailDisabled.value ? '' : episodeDetail.value.trim(),
          episode_format: episodeFormat.value,
          episode_offset: episodeOffset.value.trim(),
        }
      : {}),
    episode_part: episodePart.value.trim(),
    min_filesize: Number(minFilesize.value),
    ...(scraping.value == null ? {} : { scraping: scraping.value }),
    ...(typeFolder.value == null ? {} : { library_type_folder: typeFolder.value }),
    ...(props.historyIds?.length ? { from_history: fromHistory.value } : {}),
    preview,
    background,
  };
}
async function preview() {
  if (busy.value || previewBusy.value || recommendBusy.value) return;
  previewVisible.value = true;
  resultIsPreview.value = true;
  previewBusy.value = true;
  error.value = '';
  result.value = null;
  try {
    result.value = await api('/files/organize', payload(true));
  } catch (e) {
    error.value = errorText(e);
    await nextTick();
    formScroll.value?.scrollTo({ top: 0, behavior: 'smooth' });
  } finally {
    previewBusy.value = false;
  }
}
function waitForPoll() {
  return new Promise<void>((resolve) => {
    wake = resolve;
    sleepTimer = setTimeout(resolve, 1000);
  });
}
async function organize(background: boolean) {
  if (busy.value || previewBusy.value || recommendBusy.value) return;
  busy.value = true;
  resultIsPreview.value = false;
  error.value = '';
  result.value = null;
  completed.value = 0;
  totalJobs.value = 0;
  progress.value = '正在处理 ...';
  try {
    const response = await api<OrganizeResponse>('/files/organize', payload(false, background));
    if (!alive) return;
    if (response.preparation_job_id) {
      if (props.historyIds?.length) {
        toast(`已提交 ${props.historyIds.length} 条整理记录，正在后台准备`);
        emit('done', response.preparation_job_id);
      } else {
        toast('目录已提交，正在后台准备整理');
        emit('done');
      }
      return;
    }
    result.value = response;
    if (
      !response.submitted &&
      response.items?.length &&
      response.items.every((item) => item.status === 'skipped')
    ) {
      error.value = '没有符合整理条件的文件';
      previewVisible.value = true;
      return;
    }
    const jobs = [
      ...new Set((response.items || []).map((item) => item.job_id).filter(Boolean)),
    ] as string[];
    totalJobs.value = jobs.length;
    if (background) {
      if (response.failed) {
        error.value = `${response.submitted || 0} 项已加入整理队列，${response.failed} 项失败`;
        previewVisible.value = true;
      } else {
        toast('已加入整理队列');
        emit('done');
      }
      return;
    }
    const pending = new Set(jobs);
    const failures: string[] = [];
    while (alive && pending.size) {
      for (const id of [...pending]) {
        const detail = await api('/jobs/' + encodeURIComponent(id));
        if (!alive) return;
        const job = detail.job || detail;
        const actualItems = detail.transfer?.items;
        if (Array.isArray(actualItems) && actualItems.length) {
          const first = response.items.findIndex((item) => item.job_id === id);
          if (first >= 0) {
            const remaining = response.items.filter((item) => item.job_id !== id);
            remaining.splice(first, 0, ...actualItems);
            response.items = remaining;
          }
        }
        if (['succeeded', 'done', 'failed', 'cancelled', 'needs_review'].includes(job.state)) {
          pending.delete(id);
          completed.value++;
          if (['failed', 'cancelled', 'needs_review'].includes(job.state)) {
            const message =
              job.error_message || (job.state === 'cancelled' ? '整理任务已取消' : '整理失败');
            failures.push(message);
            if (!Array.isArray(actualItems) || !actualItems.length)
              response.items = response.items.map((item) =>
                item.job_id === id ? { ...item, status: 'failed', error: message } : item,
              );
          }
        }
        response.total = response.items.length;
        response.failed = response.items.filter((item) =>
          ['failed', 'blocked'].includes(item.status || ''),
        ).length;
        result.value = { ...response, items: [...response.items] };
        progress.value =
          job.state === 'retry_wait'
            ? job.error_message || '任务稍后重试'
            : `正在整理 ${completed.value} / ${totalJobs.value}`;
      }
      if (pending.size) await waitForPoll();
    }
    if (!alive) return;
    if (response.failed || failures.length) {
      error.value =
        [
          ...new Set([
            ...response.items.map((item) => item.error || '').filter(Boolean),
            ...failures,
          ]),
        ].join('；') || '部分文件整理失败';
      previewVisible.value = true;
    } else if (!response.total && !response.submitted) error.value = '没有找到符合条件的文件';
    else {
      toast('整理完成');
      emit('done');
    }
  } catch (e) {
    if (alive) error.value = errorText(e);
  } finally {
    busy.value = false;
    progress.value = '';
    if (alive && totalJobs.value && error.value) emit('historyChanged');
  }
}
async function search() {
  if (!query.value.trim()) return;
  searchBusy.value = true;
  searchError.value = '';
  try {
    searchResults.value =
      (
        await api(
          '/catalog/search?' +
            new URLSearchParams({
              q: query.value.trim(),
              kind: mediaType.value || 'all',
              page: '1',
              source: mediaSource.value,
            }),
        )
      ).results || [];
  } catch (e) {
    searchError.value = errorText(e);
  } finally {
    searchBusy.value = false;
  }
}
function choose(media: any) {
  const type = media.media_type;
  // The type watcher clears a previous id; let it finish before applying this id.
  mediaType.value = type;
  queueMicrotask(() => {
    mediaID.value = String(mediaSource.value === 'themoviedb' ? media.id : media.source_id);
  });
  picker.value = false;
}
onMounted(async () => {
  const episodeRulesRead = loadEpisodeRules();
  try {
    if (!ui.settings) {
      const settings = await boundedRead(
        (signal) => api('/settings', undefined, 'GET', signal),
        settingsRead.signal,
      );
      if (alive) localSettings.value = settings;
    }
  } catch (e) {
    if (alive) error.value = errorText(e);
  }
  await episodeRulesRead;
});
onUnmounted(() => {
  alive = false;
  settingsRead.abort();
  recommendRead.abort();
  episodeGroupRead?.abort();
  if (episodeGroupTimer) clearTimeout(episodeGroupTimer);
  if (sleepTimer) clearTimeout(sleepTimer);
  wake?.();
});
</script>

<template>
  <Teleport to="body">
    <div class="organize-backdrop" @click.self="!busy && close()">
      <section class="reorganize-dialog" role="dialog" aria-modal="true" aria-label="手动整理">
        <header class="organize-header">
          <FileManagerIcon name="organize" />
          <div>
            <h2>手动整理</h2>
            <p :title="subtitle">{{ subtitle }}</p>
          </div>
          <button class="icon-button" aria-label="关闭手动整理" @click="close">
            <FileManagerIcon name="close" />
          </button>
        </header>
        <div ref="formScroll" class="organize-form">
          <p v-if="error" class="error-box" role="alert">{{ error }}</p>
          <div class="organize-grid">
            <VSelect
              v-model="storage"
              :disabled="busy"
              label="目的存储"
              hint="整理目的存储，选择自动时由后端匹配"
              persistent-hint
              :items="[
                { title: '自动', value: '' },
                { title: '本地', value: 'local' },
              ]"
            />
            <VSelect
              v-model="mode"
              :disabled="busy"
              label="整理方式"
              hint="选择自动时使用目录配置的整理方式"
              persistent-hint
              :items="[
                { title: '自动', value: '' },
                { title: '复制', value: 'copy' },
                { title: '移动', value: 'move' },
                { title: '硬链接', value: 'hardlink' },
                { title: '软链接', value: 'softlink' },
              ]"
            />
            <VTextField
              :model-value="targetPath"
              :list="targetListID"
              class="span-all"
              label="目的路径"
              aria-label="目的路径"
              placeholder="自动（可输入路径）"
              hint="整理目的路径，选择自动将按源路径匹配"
              persistent-hint
              :disabled="busy"
              @focus="($event.target as HTMLInputElement).select()"
              @update:model-value="targetPath = $event === '自动' ? '' : ($event ?? '')"
            >
              <template #append-inner>
                <DirectoryPathPicker
                  v-model="targetPath"
                  :suggestions="targetPaths"
                  :disabled="busy"
                />
              </template>
            </VTextField>
            <datalist :id="targetListID">
              <option value="自动" />
              <option v-for="path in targetPaths" :key="path" :value="path" />
            </datalist>
            <VSelect
              v-model="mediaType"
              :disabled="busy"
              label="类型"
              hint="文件的媒体类型"
              persistent-hint
              :items="[
                { title: '自动', value: '' },
                { title: '电影', value: 'movie' },
                { title: '电视剧', value: 'tv' },
              ]"
            />
            <VTextField
              v-model="mediaID"
              inputmode="numeric"
              :label="mediaSourceLabel + '编号'"
              placeholder="留空自动识别"
              hint="按名称查询媒体编号，留空自动识别"
              persistent-hint
              :disabled="busy || !mediaType || !mediaSourceReady"
            >
              <template #append-inner>
                <button
                  class="icon-button"
                  aria-label="查询媒体编号"
                  :disabled="busy || !mediaType || !mediaSourceReady"
                  @click="picker = true"
                >
                  <FileManagerIcon name="search" />
                </button>
              </template>
            </VTextField>
            <template v-if="mediaType === 'tv'">
              <VSelect
                v-if="mediaSource === 'themoviedb'"
                v-model="episodeGroup"
                label="剧集组"
                placeholder="先输入 TMDB 编号"
                :items="episodeGroupOptions"
                item-title="title"
                item-value="value"
                :item-props="episodeGroupItemProps"
                :loading="episodeGroupLoading"
                :disabled="busy || !episodeGroupTMDBID"
                :hint="
                  episodeGroupError || '输入 TMDB 编号后自动查询剧集组；不指定时使用默认季集排序'
                "
                :error="!!episodeGroupError"
                clearable
                persistent-hint
                prepend-inner-icon="mdi-view-list"
              />
              <div class="episode-number-fields">
                <VSelect
                  v-model="season"
                  :disabled="busy"
                  label="季"
                  hint="第几季"
                  persistent-hint
                  :items="[
                    { title: '自动', value: '' },
                    ...Array.from({ length: 101 }, (_, index) => index + 1).map((number) => ({
                      title: (' 第 ' + String(number - 1) + ' 季 ').trim(),
                      value: number - 1,
                    })),
                  ]"
                />
                <VTextField
                  v-model="episodeDetail"
                  placeholder="起始集,终止集"
                  :disabled="busy || episodeDetailDisabled"
                  label="集"
                  hint="集数或范围，如1或1,2"
                  persistent-hint
                />
              </div>
              <VTextField
                v-model="episodeFormat"
                placeholder="使用{ep}定位集数"
                :disabled="busy"
                label="集数定位"
                :hint="episodeFormatHint"
                :error="!!recommendError"
                persistent-hint
              >
                <template #append-inner>
                  <VTooltip location="top" content-class="episode-recommend-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <span v-bind="tooltipProps">
                        <VBtn
                          type="button"
                          color="primary"
                          variant="text"
                          size="small"
                          icon="mdi-auto-fix"
                          aria-label="推荐集数定位"
                          :loading="recommendBusy"
                          :disabled="
                            busy || previewBusy || recommendBusy || !recommendSelectionValid
                          "
                          @click.stop="recommendEpisode"
                        />
                      </span>
                    </template>
                    {{ recommendTooltip }}
                  </VTooltip>
                </template>
              </VTextField>
              <VTextField
                v-model="episodeOffset"
                placeholder="如-10"
                :disabled="busy"
                label="集数偏移"
                hint="集数偏移运算，如-10或EP*2"
                persistent-hint
              />
            </template>
            <VTextField
              v-model="episodePart"
              placeholder="如part1"
              :disabled="busy"
              label="指定Part"
              hint="指定Part，如part1"
              persistent-hint
            />
            <VTextField
              v-model.number="minFilesize"
              type="number"
              min="0"
              placeholder="0"
              :disabled="busy"
              label="最小文件大小（MB）"
              hint="只整理大于最小文件大小的文件"
              persistent-hint
            />
            <label class="organize-switch"
              ><input
                type="checkbox"
                :checked="typeFolder === true"
                :disabled="busy"
                @change="typeFolder = ($event.target as HTMLInputElement).checked"
              /><span>按类型分类<small>按二级分类策略添加子目录</small></span></label
            >
            <label class="organize-switch"
              ><input
                type="checkbox"
                :checked="scraping === true"
                :disabled="busy"
                @change="scraping = ($event.target as HTMLInputElement).checked"
              /><span>刮削元数据<small>整理完成后自动刮削元数据</small></span></label
            >
            <label v-if="historyIds?.length" class="organize-switch span-all"
              ><input v-model="fromHistory" type="checkbox" :disabled="busy" /><span
                >复用历史识别信息<small>使用历史整理记录中已识别的媒体信息</small></span
              ></label
            >
          </div>
          <section v-if="previewVisible" ref="previewPanel" class="organize-preview">
            <header>
              <h3>{{ resultIsPreview ? '整理结果预览' : '整理结果' }}</h3>
              <span v-if="result"
                >总数 {{ result.total }} · 失败
                {{ Math.max(result.failed || 0, failedRows.length) }}</span
              >
            </header>
            <p v-if="resultIsPreview" class="preview-note">
              本次整理的预计结果；预览不会改动文件。
            </p>
            <p v-if="previewBusy" class="notice">正在生成预览结果…</p>
            <p v-else-if="!previewRows.length" class="notice">
              {{ resultIsPreview ? '暂无预览结果' : '暂无整理结果' }}
            </p>
            <article
              v-for="(row, index) in previewRows"
              :key="row.source + ':' + index"
              class="preview-file"
            >
              <strong>{{
                row.media?.title || row.media?.name || row.source?.split('/').pop()
              }}</strong>
              <p>
                整理前 <span>{{ row.source }}</span>
              </p>
              <p v-if="row.target">
                整理后 <span>{{ row.target }}</span>
              </p>
              <small v-if="row.mode">{{ label(row.mode) }}</small>
              <p v-if="row.error || row.reason" class="preview-failure">
                {{ row.error || row.reason }}
              </p>
            </article>
          </section>
        </div>
        <footer class="organize-actions">
          <button
            class="preview-action"
            :disabled="busy || previewBusy || recommendBusy"
            :aria-busy="previewBusy"
            @click="preview"
          >
            预览</button
          ><button
            class="queue-action"
            :disabled="busy || previewBusy || recommendBusy"
            @click="organize(true)"
          >
            ＋ 加入整理队列</button
          ><span></span
          ><button
            class="primary"
            :disabled="busy || previewBusy || recommendBusy"
            @click="organize(false)"
          >
            立即整理 →
          </button>
        </footer>
        <div v-if="busy" class="organize-progress" role="status">
          <span class="spinner"></span>
          <div>
            <strong>{{ progress || '正在处理 ...' }}</strong
            ><progress v-if="totalJobs" :value="completed" :max="totalJobs"></progress
            ><small>关闭窗口后，已提交的整理任务会继续执行。</small>
          </div>
        </div>
      </section>
    </div>
    <div v-if="picker" class="organize-backdrop media-selector" @click.self="picker = false">
      <section
        class="media-selector-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="选择媒体信息"
      >
        <header>
          <h2>选择媒体信息</h2>
          <button class="icon-button" aria-label="关闭媒体选择" @click="picker = false">×</button>
        </header>
        <form @submit.prevent="search">
          <VTextField v-model="query" label="搜索影视名称" density="compact" autofocus /><button
            class="primary"
            :disabled="searchBusy"
          >
            搜索
          </button>
        </form>
        <p v-if="searchError" class="error-box">{{ searchError }}</p>
        <div class="media-results">
          <button
            v-for="media in searchResults"
            :key="media.media_type + ':' + (media.source_id || media.id)"
            @click="choose(media)"
          >
            <img v-if="media.poster_path" :src="image(media.poster_path, 'w185')" alt="" /><span
              ><strong>{{ media.title || media.name }}</strong
              ><small
                >{{ label(media.media_type) }} ·
                {{ (media.release_date || media.first_air_date || '').slice(0, 4) }} ·
                {{ mediaSource === 'themoviedb' ? media.id : media.source_id }}</small
              ></span
            >
          </button>
        </div>
      </section>
    </div>
  </Teleport>
</template>
<style scoped>
/* Keep natural tooltip width independent of the connected overlay's left edge.
   Vuetify still supplies the viewport max-width and wraps long mobile hints. */
:global(.v-tooltip > .v-overlay__content.episode-recommend-tooltip) {
  width: max-content;
}
.organize-backdrop {
  position: fixed;
  inset: 0;
  z-index: 120;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--text) 50%, transparent);
  padding: 20px;
}
.reorganize-dialog {
  position: relative;
  background: var(--surface);
  border-radius: 6px;
  width: min(720px, 100%);
  max-height: min(92dvh, 1024px);
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.organize-header {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 12px;
  padding: 12px 24px;
  border-bottom: 1px solid var(--line);
}
.organize-header > svg {
  color: var(--muted);
}
.organize-header > div {
  min-width: 0;
  flex: 1;
}
.organize-header h2 {
  font-size: 20px;
  font-weight: 500;
  margin: 0 0 4px;
}
.organize-header p {
  font-size: 14px;
  color: var(--muted);
  margin: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.organize-form {
  padding: 24px;
  min-height: 0;
  overflow-y: auto;
  flex: 1;
}
.organize-grid,
.episode-number-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
}
.organize-grid small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.55;
  margin-top: 5px;
}
.span-all {
  grid-column: 1 / -1;
}
.organize-grid .organize-switch {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 10px;
}
.organize-switch input {
  width: 34px;
  height: 20px;
  appearance: none;
  border-radius: 12px;
  padding: 0;
  background: color-mix(in srgb, var(--text) 25%, transparent);
  border: 0;
  position: relative;
  cursor: pointer;
  flex: none;
}
.organize-switch input::after {
  content: '';
  position: absolute;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  top: 2px;
  left: 2px;
  background: #fff;
  transition: transform 0.15s;
}
.organize-switch input:checked {
  background: var(--accent);
}
.organize-switch input:checked::after {
  transform: translateX(14px);
}
.organize-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 10px;
  padding: 16px 24px;
  border-top: 1px solid var(--line);
}
.organize-actions > span {
  flex: 1;
}
.preview-action {
  background: #16b1ff1f;
  color: var(--info, #16b1ff);
  border-color: transparent;
}
.queue-action {
  background: #56ca001f;
  color: var(--success, #56ca00);
  border-color: transparent;
}
.organize-preview {
  margin-top: 24px;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 20px;
  background: var(--surface-subtle, #f9fafc);
}
.organize-preview header {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.organize-preview h3 {
  margin: 0;
  font-size: 16px;
}
.organize-preview header span {
  font-size: 12px;
  color: var(--muted);
}
.preview-note {
  font-size: 12px;
  color: var(--muted);
}
.preview-file {
  padding: 14px 0;
  border-top: 1px solid var(--line);
}
.preview-file strong {
  font-size: 13px;
}
.preview-file p {
  font-size: 11px;
  color: var(--muted);
}
.preview-file span {
  display: block;
  font-size: 12px;
  color: var(--text);
  margin-top: 4px;
  overflow-wrap: anywhere;
}
.preview-file .preview-failure {
  color: var(--danger);
}
.organize-progress {
  position: absolute;
  inset: 76px 0 0;
  background: color-mix(in srgb, var(--surface) 95%, transparent);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 15px;
  padding: 20px;
}
.organize-progress > div {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.organize-progress small {
  color: var(--muted);
}
.organize-progress progress {
  width: 100%;
  height: 8px;
}
.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--accent-soft);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
.media-selector {
  z-index: 130;
}
.media-selector-dialog {
  padding: 22px;
  background: var(--surface);
  border-radius: 6px;
  width: min(600px, 100%);
  max-height: 80dvh;
  overflow: auto;
}
.media-selector-dialog header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.media-selector-dialog h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 500;
}
.media-selector-dialog form {
  display: flex;
  gap: 10px;
  margin: 15px 0;
}
.media-results > button {
  display: flex;
  width: 100%;
  text-align: left;
  justify-content: flex-start;
  gap: 12px;
  padding: 12px;
  border: 0;
}
.media-results img {
  width: 44px;
  height: 64px;
  object-fit: cover;
  border-radius: 5px;
}
.media-results span {
  min-width: 0;
}
.media-results small {
  display: block;
  color: var(--muted);
  margin-top: 6px;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
@media (max-width: 959px) {
  .organize-backdrop {
    padding: 0;
  }
  .reorganize-dialog {
    width: 100%;
    height: 100dvh;
    max-height: 100dvh;
    border-radius: 0;
  }
  .organize-form {
    padding: 24px;
  }
  .organize-grid,
  .episode-number-fields {
    grid-template-columns: minmax(0, 1fr);
  }
  .organize-actions {
    background: var(--surface);
    z-index: 1;
    padding: 14px 12px;
    gap: 7px;
  }
  .organize-actions button {
    padding: 10px;
    font-size: 12px;
  }
}
</style>
