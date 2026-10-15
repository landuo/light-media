<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue';
import { api, errorText, image, label, toast, ui } from '../api';
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
const emit = defineEmits<{ close: []; done: [] }>();
const targetListID = 'organize-targets-' + useId();
const storage = ref(props.initial?.target_path ? 'local' : ''),
  mode = ref(''),
  targetPath = ref(props.initial?.target_path || '');
const mediaType = ref(props.initial?.media_type || ''),
  mediaID = ref(props.initial?.tmdb_id ? String(props.initial.tmdb_id) : '');
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
const targetPaths = computed(
  () =>
    [
      ...new Set(
        (ui.settings?.directories || [])
          .map((directory: any) => directory.library_path)
          .filter(Boolean),
      ),
    ] as string[],
);
const episodeDetailDisabled = computed(
  () =>
    !!props.paths.length &&
    !episodeFormat.value &&
    (props.paths.length !== 1 || props.items?.some((item) => item.type === 'dir')),
);
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
watch(mediaType, () => {
  mediaID.value = '';
  season.value = '';
  episodeDetail.value = '';
  resetPreview();
});
watch(
  [
    storage,
    mode,
    targetPath,
    mediaID,
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
    throw new Error('请选择类型并填写有效的 TheMovieDb 编号');
  if (!Number.isInteger(Number(minFilesize.value)) || Number(minFilesize.value) < 0)
    throw new Error('最小文件大小须为不小于 0 的整数');
  return {
    paths: props.paths,
    ...(props.historyIds?.length ? { history_ids: props.historyIds } : {}),
    target_storage: storage.value,
    target_path: targetPath.value.trim(),
    mode: mode.value,
    media_type: mediaType.value,
    tmdb_id: id ? Number(id) : 0,
    ...(mediaType.value === 'tv'
      ? {
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
  if (busy.value || previewBusy.value) return;
  previewVisible.value = true;
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
  if (busy.value || previewBusy.value) return;
  busy.value = true;
  error.value = '';
  result.value = null;
  completed.value = 0;
  totalJobs.value = 0;
  progress.value = '正在处理 ...';
  try {
    const response = await api<OrganizeResponse>('/files/organize', payload(false, background));
    if (!alive) return;
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
        if (['succeeded', 'done', 'failed', 'cancelled', 'needs_review'].includes(job.state)) {
          pending.delete(id);
          completed.value++;
          if (['failed', 'cancelled', 'needs_review'].includes(job.state)) {
            const message =
              job.error_message || (job.state === 'cancelled' ? '整理任务已取消' : '整理失败');
            failures.push(message);
            response.items
              .filter((item) => item.job_id === id)
              .forEach((item) => {
                item.status = 'failed';
                item.error = message;
              });
          }
        }
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
    mediaID.value = String(media.id);
  });
  picker.value = false;
}
onMounted(async () => {
  try {
    if (!ui.settings) ui.settings = await api('/settings');
  } catch (e) {
    error.value = errorText(e);
  }
});
onUnmounted(() => {
  alive = false;
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
            <label
              >目的存储<select v-model="storage" :disabled="busy">
                <option value="">自动</option>
                <option value="local">本地</option></select
              ><small>整理目的存储，选择自动时由后端匹配</small></label
            >
            <label
              >整理方式<select v-model="mode" :disabled="busy">
                <option value="">自动</option>
                <option value="copy">复制</option>
                <option value="hardlink">硬链接</option></select
              ><small>选择自动时使用目录配置的整理方式</small></label
            >
            <label class="span-all"
              >目的路径
              <div class="target-combobox">
                <input
                  :value="targetPath"
                  :list="targetListID"
                  aria-label="目的路径"
                  placeholder="自动（可输入路径）"
                  :disabled="busy"
                  @focus="($event.target as HTMLInputElement).select()"
                  @input="
                    targetPath =
                      ($event.target as HTMLInputElement).value === '自动'
                        ? ''
                        : ($event.target as HTMLInputElement).value
                  "
                />
                <datalist :id="targetListID">
                  <option value="自动" />
                  <option v-for="path in targetPaths" :key="path" :value="path" />
                </datalist>
                <DirectoryPathPicker
                  v-model="targetPath"
                  :suggestions="targetPaths"
                  :disabled="busy"
                />
              </div>
              <small>整理目的路径，选择自动将按源路径匹配</small></label
            >
            <label
              >类型<select v-model="mediaType" :disabled="busy">
                <option value="">自动</option>
                <option value="movie">电影</option>
                <option value="tv">电视剧</option></select
              ><small>文件的媒体类型</small></label
            >
            <label
              >TheMovieDb编号
              <div class="id-input">
                <input
                  v-model="mediaID"
                  inputmode="numeric"
                  placeholder="留空自动识别"
                  :disabled="busy || !mediaType"
                /><button
                  class="icon-button"
                  aria-label="查询媒体编号"
                  :disabled="busy || !mediaType"
                  @click="picker = true"
                >
                  <FileManagerIcon name="search" />
                </button>
              </div>
              <small>按名称查询媒体编号，留空自动识别</small></label
            >
            <template v-if="mediaType === 'tv'">
              <label
                >季<select v-model="season" :disabled="busy">
                  <option value="">自动</option>
                  <option v-for="number in 101" :key="number - 1" :value="number - 1">
                    第 {{ number - 1 }} 季
                  </option></select
                ><small>第几季</small></label
              >
              <label
                >集<input
                  v-model="episodeDetail"
                  placeholder="起始集,终止集"
                  :disabled="busy || episodeDetailDisabled"
                /><small>集数或范围，如1或1,2</small></label
              >
              <label
                >集数定位<input
                  v-model="episodeFormat"
                  placeholder="使用{ep}定位集数"
                  :disabled="busy"
                /><small>使用{ep}定位文件名中的集数部分以辅助识别</small></label
              >
              <label
                >集数偏移<input
                  v-model="episodeOffset"
                  placeholder="如-10"
                  :disabled="busy"
                /><small>集数偏移运算，如-10或EP*2</small></label
              >
            </template>
            <label
              >指定Part<input v-model="episodePart" placeholder="如part1" :disabled="busy" /><small
                >指定Part，如part1</small
              ></label
            >
            <label
              >最小文件大小（MB）<input
                v-model.number="minFilesize"
                type="number"
                min="0"
                placeholder="0"
                :disabled="busy"
              /><small>只整理大于最小文件大小的文件</small></label
            >
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
              <h3>整理结果预览</h3>
              <span v-if="result"
                >总数 {{ result.total }} · 失败
                {{ Math.max(result.failed || 0, failedRows.length) }}</span
              >
            </header>
            <p class="preview-note">本次整理的预计结果；预览不会改动文件。</p>
            <p v-if="previewBusy" class="notice">正在生成预览结果…</p>
            <p v-else-if="!previewRows.length" class="notice">暂无预览结果</p>
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
            :disabled="busy || previewBusy"
            :aria-busy="previewBusy"
            @click="preview"
          >
            预览</button
          ><button class="queue-action" :disabled="busy || previewBusy" @click="organize(true)">
            ＋ 加入整理队列</button
          ><span></span
          ><button class="primary" :disabled="busy || previewBusy" @click="organize(false)">
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
          <input v-model="query" placeholder="搜索影视名称" autofocus /><button
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
            :key="media.media_type + media.id"
            @click="choose(media)"
          >
            <img v-if="media.poster_path" :src="image(media.poster_path, 'w185')" alt="" /><span
              ><strong>{{ media.title || media.name }}</strong
              ><small
                >{{ label(media.media_type) }} ·
                {{ (media.release_date || media.first_air_date || '').slice(0, 4) }} ·
                {{ media.id }}</small
              ></span
            >
          </button>
        </div>
      </section>
    </div>
  </Teleport>
</template>
<style scoped>
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
.organize-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
}
.organize-grid label {
  display: block;
  font-size: 14px;
}
.organize-grid label > select,
.organize-grid label > input,
.target-combobox,
.id-input {
  margin-top: 7px;
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
.target-combobox {
  display: flex;
  border: 1px solid var(--line);
  border-radius: 6px;
}
.target-combobox select {
  width: 94px;
  flex: none;
  border: 0;
  background: var(--accent-soft);
  padding: 10px 8px;
}
.target-combobox input {
  border: 0;
  box-shadow: none;
  min-width: 0;
}
.id-input {
  display: flex;
  border: 1px solid var(--line);
  border-radius: 6px;
}
.id-input input {
  min-width: 0;
  border: 0;
  box-shadow: none;
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
  .organize-grid {
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
