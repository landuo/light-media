<script setup lang="ts">
import { VSelect } from 'vuetify/components/VSelect';
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, bytes, date, errorText, label, toast } from '../api';
import ReorganizeDialog from './ReorganizeDialog.vue';
import { deleteHistoryBatch } from '../features/history-delete';
import {
  historyGroups,
  historyTitle,
  historyMediaType,
  historyPageButtons,
  historyPageSizes,
  historyQuery,
  historyViewHash,
  historyViewState,
  sortHistory,
  transferQueueState,
  groupTransferQueue,
  type HistoryRow,
  type HistorySort,
  type TransferQueueGroup,
} from '../features/transfer-history';

const preparationLabels = {
  queued: '等待准备',
  running: '正在准备整理',
  retry_wait: '等待重试',
  waiting_paused: '已暂停',
};
type QueuePreparation = {
  id: string;
  paths: string[];
  state: keyof typeof preparationLabels;
  history_count?: number;
};
const failedPreparationStates = ['failed', 'cancelled', 'needs_review'];
const finishedPreparationStates = ['succeeded', ...failedPreparationStates];

// Legacy page events remain compatible; reorganization now opens in place.
defineEmits<{ organize: [paths: string[]]; job: [id: string] }>();
const initialState = historyViewState(location.hash);
const query = ref(initialState.search),
  page = ref(initialState.page),
  pageSize = ref(initialState.pageSize),
  grouped = ref(initialState.grouped),
  result = ref<{ items: HistoryRow[]; total: number } | null>(null),
  loading = ref(false),
  error = ref(''),
  selected = ref<string[]>([]),
  knownRows = ref(new Map<string, HistoryRow>()),
  expandedGroups = ref<Set<string>>(new Set()),
  sortKey = ref<HistorySort | ''>(''),
  sortDescending = ref(false),
  openMenu = ref(''),
  menuPosition = ref({ top: '0px', left: '0px' }),
  composing = ref(false),
  reorganizeItems = ref<HistoryRow[]>([]),
  deleteItems = ref<HistoryRow[]>([]),
  deleting = ref(false),
  deleteError = ref(''),
  deleteProgress = ref({ done: 0, total: 0, title: '' }),
  queueOpen = ref(false),
  queueLoading = ref(false),
  queueError = ref(''),
  queueCancelling = ref(''),
  queueGroups = ref<any[]>([]),
  queuePreparations = ref<QueuePreparation[]>([]),
  activeQueue = ref('');
const items = computed(() => result.value?.items || []),
  total = computed(() => result.value?.total || 0),
  pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value))),
  pageButtons = computed(() => historyPageButtons(page.value, pages.value)),
  sortedItems = computed(() =>
    sortKey.value ? sortHistory(items.value, sortKey.value, sortDescending.value) : items.value,
  ),
  groups = computed(() =>
    grouped.value ? historyGroups(sortedItems.value) : [{ title: '', items: sortedItems.value }],
  ),
  selectedItems = computed(() =>
    selected.value
      .map((id) => knownRows.value.get(id))
      .filter((item): item is HistoryRow => !!item),
  ),
  allSelected = computed(
    () => items.value.length > 0 && items.value.every((item) => selected.value.includes(item.id)),
  ),
  hints = computed(() => [
    '失败',
    '成功',
    ...new Set(items.value.map(historyTitle).filter(Boolean)),
  ]),
  first = computed(() => (page.value - 1) * pageSize.value + 1),
  last = computed(() => Math.min(page.value * pageSize.value, total.value));
const queueFiles = computed(() => queueGroups.value.flatMap((group) => group.files || [])),
  completedQueue = computed(
    () => queueFiles.value.filter((file) => isQueueComplete(file.status)).length,
  ),
  activeQueueGroup = computed(() =>
    queueGroups.value.find((group) => group.id === activeQueue.value),
  );
const pendingPreparations = new Set<string>();
let queueGeneration = 0,
  searchTimer: number | undefined,
  historyRequest: AbortController | undefined,
  preparationRequest: AbortController | undefined,
  refreshAfterPreparation = false;
function isQueueComplete(status: string) {
  return ['success', 'imported', 'already_imported', 'completed', 'succeeded'].includes(status);
}
function queueStatusLabel(file: any) {
  const state = transferQueueState(file);
  if (state === 'running' && file.recognizing) return '正在识别';
  const labels: Record<string, string> = {
    waiting: '等待中',
    running: '正在整理',
    completed: '完成',
    failed: '失败',
    cancelled: '已取消',
    unknown: '未知',
  };
  return labels[state];
}
function viewState() {
  return {
    search: query.value,
    page: page.value,
    pageSize: pageSize.value,
    grouped: grouped.value,
  };
}
function syncURL() {
  const hash = historyViewHash(viewState());
  if (location.hash !== hash) history.pushState(null, '', hash);
}
function selectAll(checked: boolean) {
  selectGroup(items.value, checked);
}
function selectedInGroup(rows: HistoryRow[]) {
  return rows.filter((row) => selected.value.includes(row.id)).length;
}
function selectGroup(rows: HistoryRow[], checked: boolean) {
  const ids = new Set(rows.map((row) => row.id));
  selected.value = checked
    ? [...new Set([...selected.value, ...ids])]
    : selected.value.filter((id) => !ids.has(id));
}
function toggleGroup(title: string) {
  const next = new Set(expandedGroups.value);
  next.has(title) ? next.delete(title) : next.add(title);
  expandedGroups.value = next;
}
function toggleMenu(item: HistoryRow, event: MouseEvent) {
  if (openMenu.value === item.id) {
    openMenu.value = '';
    return;
  }
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
  menuPosition.value = {
    top: Math.min(rect.bottom + 3, window.innerHeight - 105) + 'px',
    left: Math.max(8, rect.right - 155) + 'px',
  };
  openMenu.value = item.id;
}
function sort(key: HistorySort) {
  if (sortKey.value === key) sortDescending.value = !sortDescending.value;
  else {
    sortKey.value = key;
    sortDescending.value = false;
  }
}
function sortIndicator(key: HistorySort) {
  return sortKey.value === key ? (sortDescending.value ? ' ↓' : ' ↑') : '';
}
function reorganize(rows: HistoryRow[]) {
  openMenu.value = '';
  // A single-row action must only retry that row. Batch actions follow
  // MoviePilot's semantics: only failed records are retried, never successes.
  const chosen = rows.length === 1 ? rows : rows.filter((row) => row.status === 'failed');
  if (!chosen.length) {
    toast('所选记录中没有失败项，无需重新整理', true);
    return;
  }
  if (chosen.length > 1 && !window.confirm(`将重新整理 ${chosen.length} 条失败记录，是否继续？`)) return;
  if (rows.length === 1) selected.value = [];
  reorganizeItems.value = chosen;
}
async function reorganized(preparationJobID?: string) {
  reorganizeItems.value = [];
  selected.value = [];
  if (preparationJobID) {
    pendingPreparations.add(preparationJobID);
    return;
  }
  await load();
}
function openDelete(rows: HistoryRow[]) {
  if (deleting.value || !rows.length) return;
  openMenu.value = '';
  deleteItems.value = [...rows];
}
async function remove(deleteSource: boolean, deleteTarget: boolean) {
  if (deleting.value || !deleteItems.value.length) return;
  const deletingItems = [...deleteItems.value];
  deleteItems.value = [];
  deleting.value = true;
  deleteProgress.value = { done: 0, total: deletingItems.length, title: '' };
  error.value = '';
  deleteError.value = '';
  const outcome = await deleteHistoryBatch(
    deletingItems,
    deleteSource,
    deleteTarget,
    (item, request) => api('/history/transfers/' + encodeURIComponent(item.id), request, 'DELETE'),
    (item, done, total) => {
      deleteProgress.value = { done, total, title: historyTitle(item) + ' ' + numbers(item) };
    },
  );
  const succeeded = new Set(outcome.succeeded.map((item) => item.id));
  selected.value = [
    ...new Set([
      ...selected.value.filter((id) => !succeeded.has(id)),
      ...outcome.failed.map(({ item }) => item.id),
    ]),
  ];
  for (const id of succeeded) knownRows.value.delete(id);
  if (outcome.failed.length) {
    deleteError.value =
      `已删除 ${outcome.succeeded.length} 条，${outcome.failed.length} 条删除失败，失败项已保留选中。\n` +
      outcome.failed
        .map(({ item, error }) => `${historyTitle(item)} ${numbers(item)}：${errorText(error)}`)
        .join('\n');
  } else {
    toast(`已删除 ${outcome.succeeded.length} 条整理记录`);
  }
  await load();
  deleting.value = false;
}
async function load(silent = false) {
  if (silent && (historyRequest || searchTimer !== undefined || composing.value)) return;
  refreshAfterPreparation = false;
  clearSearchTimer();
  historyRequest?.abort();
  const request = new AbortController();
  historyRequest = request;
  loading.value = !silent;
  error.value = '';
  try {
    while (!request.signal.aborted) {
      const next = await api<{ items: HistoryRow[]; total: number }>(
        '/history/transfers?' + historyQuery(viewState()),
        undefined,
        undefined,
        request.signal,
      );
      if (request.signal.aborted) return;
      const lastPage = Math.max(1, Math.ceil(next.total / pageSize.value));
      if (page.value > lastPage) {
        page.value = lastPage;
        syncURL();
        continue;
      }
      result.value = next;
      for (const item of next.items) knownRows.value.set(item.id, item);
      break;
    }
  } catch (e) {
    if (!request.signal.aborted) error.value = errorText(e);
  } finally {
    if (historyRequest === request) {
      historyRequest = undefined;
      loading.value = false;
    }
  }
}
function clearSearchTimer() {
  clearTimeout(searchTimer);
  searchTimer = undefined;
}
function beginComposition() {
  composing.value = true;
  clearSearchTimer();
  historyRequest?.abort();
}
function search() {
  if (composing.value) return;
  clearSearchTimer();
  historyRequest?.abort();
  searchTimer = window.setTimeout(searchNow, 280);
}
function searchNow() {
  if (composing.value) return;
  page.value = 1;
  syncURL();
  void load();
}
function changePage(next: number) {
  page.value = Math.min(Math.max(1, next), pages.value);
  syncURL();
  void load();
}
function changePageSize() {
  page.value = 1;
  syncURL();
  void load();
}
function toggleGrouped() {
  grouped.value = !grouped.value;
  syncURL();
}
function restoreURL() {
  if (!location.hash.replace(/^#\/?/, '').startsWith('history')) return;
  const state = historyViewState(location.hash);
  query.value = state.search;
  page.value = state.page;
  pageSize.value = state.pageSize;
  grouped.value = state.grouped;
  void load();
}
function statusLabel(item: HistoryRow) {
  return item.status === 'success' ? '成功' : item.status === 'pending' ? '整理中' : '失败';
}
function numbers(item: HistoryRow) {
  return item.media?.media_type === 'tv'
    ? `S${String(item.season).padStart(2, '0')}${item.episodes?.map((ep) => 'E' + String(ep).padStart(2, '0')).join('') || ''}`
    : '';
}
async function pollPreparations() {
  if (!pendingPreparations.size || preparationRequest) return;
  const request = new AbortController();
  preparationRequest = request;
  try {
    await Promise.allSettled(
      [...pendingPreparations].map(async (id) => {
        const { job } = await api<{ job: { state: string; error_message?: string } }>(
          '/jobs/' + encodeURIComponent(id),
          undefined,
          undefined,
          request.signal,
        );
        if (request.signal.aborted || !finishedPreparationStates.includes(job.state)) return;
        pendingPreparations.delete(id);
        refreshAfterPreparation = true;
        if (failedPreparationStates.includes(job.state)) {
          const reason = job.state === 'cancelled' ? '任务已取消' : '请查看关联任务';
          toast(`重新整理准备未完成：${job.error_message || reason}`, true);
        }
      }),
    );
    if (!request.signal.aborted && refreshAfterPreparation) void load(true);
  } finally {
    preparationRequest = undefined;
  }
}
async function loadQueue() {
  const run = ++queueGeneration;
  queueLoading.value = true;
  queueError.value = '';
  try {
    const next = await api<{ groups: TransferQueueGroup[]; preparations?: QueuePreparation[] }>(
      '/history/transfers/queue',
    );
    if (run !== queueGeneration) return;
    const preparations = next.preparations || [];
    const preparationEnded = queuePreparations.value.some(
      ({ id }) => !preparations.some((preparation) => preparation.id === id),
    );
    const groups = groupTransferQueue(next.groups || []);
    const activeFiles = new Set(
      groups.flatMap((group) => group.files.map((file) => file.job_id || file.id)),
    );
    const fileEnded = queueFiles.value.some((file) => !activeFiles.has(file.job_id || file.id));
    queuePreparations.value = preparations;
    queueGroups.value = groups;
    if (!queueGroups.value.some((group) => group.id === activeQueue.value))
      activeQueue.value = queueGroups.value[0]?.id || '';
    if (
      (preparationEnded || fileEnded) &&
      !loading.value &&
      !items.value.some((item) => item.status === 'pending')
    )
      void load(true);
  } catch (e) {
    if (run === queueGeneration) queueError.value = errorText(e);
  } finally {
    if (run === queueGeneration) queueLoading.value = false;
  }
}
async function showQueue() {
  queueOpen.value = true;
  await loadQueue();
}
async function cancelQueueFile(file: any) {
  queueCancelling.value = file.id;
  queueError.value = '';
  try {
    await api('/history/transfers/queue/' + encodeURIComponent(file.id) + '/cancel', {});
    await loadQueue();
    await load(true);
  } catch (e) {
    queueError.value = errorText(e);
  } finally {
    queueCancelling.value = '';
  }
}
function dismissMenu(event: MouseEvent) {
  if (!(event.target as Element).closest('.th-row-menu')) openMenu.value = '';
}
function escape(event: KeyboardEvent) {
  if (event.key !== 'Escape') return;
  openMenu.value = '';
  if (!deleting.value) deleteItems.value = [];
  queueOpen.value = false;
}
onMounted(() => {
  void load();
  window.addEventListener('hashchange', restoreURL);
  document.addEventListener('click', dismissMenu);
  document.addEventListener('keydown', escape);
});
onUnmounted(() => {
  historyRequest?.abort();
  preparationRequest?.abort();
  queueGeneration++;
  clearSearchTimer();
  window.removeEventListener('hashchange', restoreURL);
  document.removeEventListener('click', dismissMenu);
  document.removeEventListener('keydown', escape);
});
</script>

<template>
  <section class="transfer-history" aria-label="整理历史">
    <p v-if="error" class="th-error th-notice" role="alert">{{ error }}</p>
    <p v-if="deleteError" class="th-error th-notice" role="alert">{{ deleteError }}</p>
    <div class="th-card">
      <div class="th-toolbar">
        <form class="th-search" @submit.prevent="searchNow">
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="10.5" cy="10.5" r="6.5" />
            <path d="m16 16 5 5" />
          </svg>
          <input
            v-model="query"
            list="history-search-hints"
            placeholder="搜索（支持 * ? 通配符）"
            aria-label="搜索（支持 * ? 通配符）"
            @input="search"
            @compositionstart="beginComposition"
            @compositionend="
              composing = false;
              search();
            "
          />
          <button
            v-if="query"
            type="button"
            class="th-clear"
            aria-label="清除搜索"
            @click="
              query = '';
              searchNow();
            "
          >
            ×
          </button>
          <datalist id="history-search-hints">
            <option v-for="hint in hints" :key="hint" :value="hint" />
          </datalist>
        </form>
        <button
          class="th-group-toggle"
          aria-label="刷新"
          title="刷新"
          :disabled="loading"
          @click="load(true)"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path
              d="M17.65 6.35A7.95 7.95 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
            />
          </svg>
        </button>
        <button
          class="th-group-toggle"
          :aria-label="grouped ? '列表模式' : '分组模式'"
          :title="grouped ? '列表模式' : '分组模式'"
          :aria-pressed="grouped"
          @click="toggleGrouped"
        >
          <svg v-if="grouped" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M8 6h13M8 12h13M8 18h13M3 6h1M3 12h1M3 18h1" />
          </svg>
          <svg v-else viewBox="0 0 24 24" aria-hidden="true">
            <rect x="3" y="3" width="18" height="7" rx="1" />
            <path d="M7 14h14M7 19h14M3 14h1M3 19h1" />
          </svg>
        </button>
      </div>
      <div class="th-table-wrap" :aria-busy="loading">
        <table>
          <thead>
            <tr>
              <th class="th-selection">
                <input
                  type="checkbox"
                  aria-label="选择本页全部整理历史"
                  :checked="allSelected"
                  :indeterminate="selectedInGroup(items) > 0 && !allSelected"
                  @change="selectAll(($event.target as HTMLInputElement).checked)"
                />
              </th>
              <th>
                <button @click="sort('title')">
                  {{ grouped ? '季集/类别' : '标题' }}{{ sortIndicator('title') }}
                </button>
              </th>
              <th>
                <button @click="sort('source')">路径{{ sortIndicator('source') }}</button>
              </th>
              <th>
                <button @click="sort('mode')">转移方式{{ sortIndicator('mode') }}</button>
              </th>
              <th>
                <button @click="sort('size')">大小{{ sortIndicator('size') }}</button>
              </th>
              <th>
                <button @click="sort('updated_at')">时间{{ sortIndicator('updated_at') }}</button>
              </th>
              <th>
                <button @click="sort('status')">状态{{ sortIndicator('status') }}</button>
              </th>
              <th class="th-menu-cell"><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <template v-for="group in groups" :key="group.title">
              <tr v-if="grouped" class="th-group-row">
                <td colspan="8">
                  <div class="th-group-heading">
                    <button
                      class="th-expand"
                      :aria-label="
                        (expandedGroups.has(group.title) ? '收起 ' : '展开 ') + group.title
                      "
                      :aria-expanded="expandedGroups.has(group.title)"
                      @click="toggleGroup(group.title)"
                    >
                      {{ expandedGroups.has(group.title) ? '⌄' : '›' }}
                    </button>
                    <input
                      type="checkbox"
                      :aria-label="'选择 ' + group.title + ' 全部记录'"
                      :checked="selectedInGroup(group.items) === group.items.length"
                      :indeterminate="
                        selectedInGroup(group.items) > 0 &&
                        selectedInGroup(group.items) < group.items.length
                      "
                      @change="
                        selectGroup(group.items, ($event.target as HTMLInputElement).checked)
                      "
                    />
                    <span>{{ group.title }}</span>
                  </div>
                </td>
              </tr>
              <template v-if="!grouped || expandedGroups.has(group.title)">
                <tr
                  v-for="item in group.items"
                  :key="item.id"
                  :class="{ 'th-selected': selected.includes(item.id) }"
                >
                  <td class="th-selection">
                    <input
                      v-model="selected"
                      type="checkbox"
                      :value="item.id"
                      :aria-label="'选择 ' + historyTitle(item) + ' ' + numbers(item)"
                    />
                  </td>
                  <td class="th-title-cell">
                    <div class="th-title">
                      <svg
                        v-if="item.media.media_type === 'tv'"
                        viewBox="0 0 24 24"
                        aria-hidden="true"
                      >
                        <rect x="3" y="6" width="18" height="14" rx="2" />
                        <path d="m8 2 4 4 4-4M7 23h10" />
                      </svg>
                      <svg
                        v-else-if="item.media.media_type === 'movie'"
                        viewBox="0 0 24 24"
                        aria-hidden="true"
                      >
                        <rect x="3" y="3" width="18" height="18" rx="1" />
                        <path d="M7 3v18M17 3v18M3 8h4M3 16h4M17 8h4M17 16h4" />
                      </svg>
                      <svg v-else viewBox="0 0 24 24" aria-hidden="true">
                        <circle cx="12" cy="12" r="9" />
                        <path d="M9 9a3 3 0 0 1 6 0c0 2-3 2-3 4M12 17h.01" />
                      </svg>
                      <span
                        ><span v-if="!grouped">{{ historyTitle(item) }} </span>{{ numbers(item)
                        }}<small>{{ historyMediaType(item) }}</small></span
                      >
                    </div>
                  </td>
                  <td class="th-path">
                    <span><span class="th-storage">本地</span>{{ item.source }}</span
                    ><span class="th-arrow"> ⇒ </span
                    ><span v-if="item.target"
                      ><span class="th-storage">本地</span>{{ item.target }}</span
                    >
                  </td>
                  <td>
                    <span class="th-mode">{{ label(item.mode) }}</span>
                  </td>
                  <td class="th-nowrap">{{ bytes(item.size) }}</td>
                  <td class="th-date">{{ date(item.updated_at) }}</td>
                  <td>
                    <span
                      class="th-status"
                      :class="item.status"
                      :title="item.error || statusLabel(item)"
                      tabindex="0"
                      >{{ statusLabel(item) }}</span
                    >
                  </td>
                  <td class="th-menu-cell">
                    <div class="th-row-menu">
                      <button
                        class="th-more"
                        :aria-label="historyTitle(item) + ' 更多操作'"
                        :aria-expanded="openMenu === item.id"
                        aria-haspopup="menu"
                        @click.stop="toggleMenu(item, $event)"
                      >
                        ⋮
                      </button>
                      <Teleport to="body"
                        ><div
                          v-if="openMenu === item.id"
                          class="th-menu th-row-menu"
                          role="menu"
                          :style="menuPosition"
                        >
                          <button role="menuitem" @click="reorganize([item])">
                            <span aria-hidden="true">↻</span>重新整理</button
                          ><button class="danger" role="menuitem" @click="openDelete([item])">
                            <span aria-hidden="true">⌫</span>删除
                          </button>
                        </div></Teleport
                      >
                    </div>
                  </td>
                </tr>
              </template>
            </template>
            <tr v-if="!items.length">
              <td colspan="8" class="th-empty">{{ loading ? '加载中…' : '没有数据' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="th-pagination">
        <VSelect
          v-model="pageSize"
          aria-label="每页条数"
          @update:model-value="changePageSize"
          density="compact"
          :items="historyPageSizes"
        />
        <span>{{ first }} - {{ last }} / {{ total }}</span>
        <nav aria-label="整理历史分页">
          <button aria-label="首页" :disabled="page <= 1 || loading" @click="changePage(1)">
            «</button
          ><button
            aria-label="上一页"
            :disabled="page <= 1 || loading"
            @click="changePage(page - 1)"
          >
            ‹</button
          ><button
            v-for="number in pageButtons"
            :key="number"
            :aria-label="'第 ' + number + ' 页'"
            :aria-current="number === page ? 'page' : undefined"
            :disabled="loading"
            :class="{ active: number === page }"
            @click="changePage(number)"
          >
            {{ number }}</button
          ><button
            aria-label="下一页"
            :disabled="page >= pages || loading"
            @click="changePage(page + 1)"
          >
            ›</button
          ><button
            aria-label="末页"
            :disabled="page >= pages || loading"
            @click="changePage(pages)"
          >
            »
          </button>
        </nav>
      </div>
    </div>
    <div class="th-fab-stack">
      <button
        v-if="selected.length"
        class="th-fab th-fab-action th-fab-delete"
        aria-label="批量删除"
        :title="'批量删除（已选 ' + selected.length + ' 条）'"
        :disabled="deleting"
        @click="openDelete(selectedItems)"
      >
        <span aria-hidden="true">⌫</span><span>批量删除（{{ selected.length }}）</span></button
      ><button
        v-if="selected.length"
        class="th-fab th-fab-action th-fab-redo"
        aria-label="批量重新整理"
        :title="'批量重新整理（已选 ' + selected.length + ' 条）'"
        :disabled="deleting"
        @click="reorganize(selectedItems)"
      >
        <span aria-hidden="true">↻</span><span>批量重新整理（{{ selectedItems.length }}）</span></button
      ><button
        class="th-fab th-fab-queue"
        aria-label="整理队列"
        title="整理队列"
        @click="showQueue"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M7 3h10M7 21h10M8 3v5l8 8v5M16 3v5l-8 8v5M5 12H2" />
        </svg>
      </button>
    </div>
    <ReorganizeDialog
      v-if="reorganizeItems.length"
      :paths="reorganizeItems.map((item) => item.source)"
      :history-ids="reorganizeItems.map((item) => item.id)"
      @close="reorganizeItems = []"
      @done="reorganized"
      @history-changed="load(true)"
    />
    <div
      v-if="deleteItems.length"
      class="th-overlay th-delete-overlay"
      @click.self="deleteItems = []"
    >
      <section class="th-delete-sheet" role="dialog" aria-modal="true" aria-label="删除整理记录">
        <button class="th-close" aria-label="关闭删除窗口" @click="deleteItems = []">×</button>
        <h2>
          {{
            deleteItems.length === 1
              ? '确认删除 ' + historyTitle(deleteItems[0]!) + ' ' + numbers(deleteItems[0]!) + ' ?'
              : '确认批量删除选中的 ' + deleteItems.length + ' 条整理记录 ?'
          }}
        </h2>
        <div class="th-delete-actions">
          <button class="th-delete-record" @click="remove(false, false)">仅删除整理记录</button
          ><button class="th-delete-source" @click="remove(true, false)">
            删除整理记录和原文件</button
          ><button class="th-delete-dest" @click="remove(false, true)">
            删除媒体库文件和整理记录</button
          ><button class="th-delete-all" @click="remove(true, true)">
            删除媒体库文件、整理记录和原文件
          </button>
        </div>
      </section>
    </div>
    <div v-if="deleting" class="th-overlay">
      <section class="th-progress" role="dialog" aria-modal="true" aria-label="正在删除整理记录">
        <h2>正在删除 {{ deleteProgress.title }} …</h2>
        <progress :value="deleteProgress.done" :max="deleteProgress.total" />
        <p>{{ deleteProgress.done }} / {{ deleteProgress.total }}</p>
      </section>
    </div>
    <div v-if="queueOpen" class="th-overlay" @click.self="queueOpen = false">
      <section class="th-queue-dialog" role="dialog" aria-modal="true" aria-label="整理队列">
        <header>
          <h2>整理队列</h2>
          <button class="th-close" aria-label="关闭整理队列" @click="queueOpen = false">×</button>
        </header>
        <p v-if="queueError" class="th-error th-notice" role="alert">{{ queueError }}</p>
        <div v-if="queuePreparations.length" class="th-queue-body">
          <div v-for="preparation in queuePreparations" :key="preparation.id" class="th-queue-file">
            <div class="th-queue-file-name">
              <template v-if="preparation.history_count">
                重新整理 {{ preparation.history_count }} 条记录
                <small v-if="preparation.paths[0]">{{ preparation.paths[0] }}</small>
              </template>
              <template v-else>
                {{ preparation.paths[0] }}
                <small v-if="preparation.paths.length > 1"
                  >共 {{ preparation.paths.length }} 个选中路径</small
                >
              </template>
            </div>
            <span class="th-status" :class="preparation.state">
              {{ preparationLabels[preparation.state] }}
            </span>
          </div>
        </div>
        <div v-if="queueGroups.length" class="th-queue-body">
          <progress :value="completedQueue" :max="queueFiles.length || 1" />
          <p class="th-queue-count">{{ completedQueue }} / {{ queueFiles.length }}</p>
          <div class="th-queue-tabs" role="tablist" aria-label="整理影视">
            <button
              v-for="group in queueGroups"
              :key="group.id"
              :class="{ active: activeQueue === group.id }"
              role="tab"
              :aria-selected="activeQueue === group.id"
              @click="activeQueue = group.id"
            >
              {{ group.title || group.media?.title }}
              <small
                >{{
                  (group.files || []).filter((file: any) => isQueueComplete(file.status)).length
                }}
                / {{ group.files?.length || 0 }}</small
              >
            </button>
          </div>
          <div v-for="file in activeQueueGroup?.files || []" :key="file.id" class="th-queue-file">
            <div class="th-queue-file-name" :title="file.source">
              {{ file.source?.split('/').pop() || file.target?.split('/').pop() }}
              <small
                >文件大小：{{ bytes(file.size || 0) }}
                <span
                  class="th-status"
                  :class="transferQueueState(file)"
                  :title="file.error || activeQueueGroup?.error || ''"
                  >{{ queueStatusLabel(file) }}</span
                ></small
              >
            </div>
            <button
              class="th-queue-cancel"
              :aria-label="'取消整理 ' + (file.source?.split('/').pop() || '')"
              title="取消整理"
              :disabled="!file.can_cancel || !!queueCancelling || isQueueComplete(file.status)"
              @click="cancelQueueFile(file)"
            >
              ⊘
            </button>
            <div
              v-if="typeof file.progress === 'number' && file.progress >= 0 && file.progress <= 100"
              class="th-file-progress"
            >
              <progress :value="file.progress" max="100" /><small
                >{{ file.progress.toFixed(1) }}%</small
              >
            </div>
            <small
              v-if="
                file.error || (transferQueueState(file) === 'failed' && activeQueueGroup?.error)
              "
              class="th-error"
              >{{ file.error || activeQueueGroup?.error }}</small
            >
          </div>
        </div>
        <p v-else-if="!queuePreparations.length" class="th-empty">
          {{ queueLoading ? '加载中…' : '没有正在整理的任务' }}
        </p>
      </section>
    </div>
  </section>
</template>

<style scoped>
.transfer-history {
  width: 100%;
  margin: auto;
}
.th-card {
  border-radius: 6px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.th-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px 24px;
}
.th-search {
  display: flex;
  align-items: center;
  width: 400px;
  max-width: calc(100% - 65px);
  border: 1px solid transparent;
  border-radius: 24px;
  padding: 0 12px;
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
}
.th-search:focus-within {
  border-color: var(--accent);
}
.th-search input {
  border: 0;
  background: none;
  box-shadow: none;
  min-width: 0;
  padding: 9px 8px;
  font-size: 15px;
}
.th-search svg,
.th-group-toggle svg,
.th-title svg,
.th-fab svg {
  width: 21px;
  height: 21px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.7;
  flex-shrink: 0;
}
.th-search svg {
  color: var(--muted);
}
.th-clear {
  border: 0;
  background: transparent;
  font-size: 22px;
  padding: 0 4px;
}
.th-group-toggle {
  display: flex;
  padding: 10px 14px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: transparent;
  color: var(--muted);
}
.th-table-wrap {
  overflow: auto;
  height: calc(100dvh - 285px);
  min-height: 300px;
}
table {
  border-collapse: collapse;
  width: 100%;
}
th,
td {
  padding: 8px 16px;
  border-bottom: 1px solid var(--line);
  text-align: left;
  vertical-align: middle;
}
th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--surface-subtle, #f9fafc);
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  white-space: nowrap;
}
th button {
  border: 0;
  background: none;
  padding: 0;
  font-size: inherit;
  color: inherit;
}
td {
  font-size: 14px;
}
tbody tr:hover {
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
}
.th-selected,
tbody tr.th-selected:hover {
  background: var(--accent-soft);
}
.th-selection {
  width: 34px;
  padding-left: 16px;
}
input[type='checkbox'] {
  width: 18px;
  height: 18px;
  padding: 0;
  margin: 0;
  vertical-align: middle;
}
.th-title-cell {
  min-width: 155px;
}
.th-title {
  display: flex;
  gap: 10px;
  align-items: center;
  line-height: 1.6;
}
.th-title svg {
  color: var(--muted);
  width: 25px;
  height: 25px;
}
.th-title small {
  display: block;
  color: var(--muted);
}
.th-path {
  min-width: 240px;
  max-width: 480px;
  overflow-wrap: anywhere;
  line-height: 1.8;
}
.th-path > span {
  display: block;
}
.th-storage {
  display: inline-block;
  border-radius: 4px;
  padding: 0 6px;
  background: var(--surface-subtle, #f9fafc);
  color: var(--muted);
  margin-right: 6px;
  font-size: 11px;
}
.th-arrow {
  color: var(--muted);
  line-height: 1;
}
.th-mode {
  border-radius: 4px;
  color: var(--muted);
  padding: 3px 8px;
  background: color-mix(in srgb, var(--text) 8%, transparent);
  white-space: nowrap;
}
.th-nowrap {
  white-space: nowrap;
}
.th-date {
  min-width: 90px;
  max-width: 140px;
  line-height: 1.6;
  color: var(--muted);
}
.th-status {
  display: inline-block;
  padding: 3px 8px;
  border-radius: 4px;
  white-space: nowrap;
  color: var(--muted);
  background: var(--surface-subtle, #f9fafc);
  font-size: 12px;
}
.th-status.success,
.th-status.imported,
.th-status.already_imported,
.th-status.completed,
.th-status.succeeded {
  color: #56ca00;
  background: #56ca001f;
}
.th-status.pending,
.th-status.queued,
.th-status.running {
  color: var(--accent);
  background: var(--accent-soft);
}
.th-status.failed {
  color: var(--danger);
  background: #ff4c511f;
}
.th-menu-cell {
  width: 35px;
  padding: 7px;
}
.th-row-menu {
  position: relative;
}
.th-more {
  border: 0;
  background: transparent;
  min-width: 36px;
  min-height: 36px;
  padding: 2px 10px;
  border-radius: 50%;
  font-size: 25px;
  color: var(--muted);
}
.th-menu {
  position: fixed;
  z-index: 60;
  width: 155px;
  border-radius: 6px;
  padding: 6px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.th-menu button {
  display: flex;
  gap: 12px;
  width: 100%;
  min-height: 40px;
  padding: 9px 10px;
  border: 0;
  background: transparent;
  text-align: left;
  border-radius: 4px;
}
.th-menu button:hover {
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
}
.th-menu button span {
  font-size: 17px;
  width: 18px;
  line-height: 1;
}
.th-group-row td {
  padding: 6px 10px;
  background: var(--surface-subtle, #f9fafc);
}
.th-group-heading {
  display: flex;
  gap: 15px;
  align-items: center;
}
.th-expand {
  border: 0;
  background: none;
  font-size: 24px;
  padding: 0 4px;
  width: 26px;
}
.th-empty {
  text-align: center;
  padding: 60px 20px;
  color: var(--muted);
}
.th-pagination {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px 24px;
  border-top: 1px solid var(--line);
}
.th-pagination > .v-input {
  width: 90px;
  flex: 0 0 90px;
}
.th-pagination > span {
  margin-right: auto;
  white-space: nowrap;
  font-size: 14px;
  color: var(--muted);
}
.th-pagination nav {
  display: flex;
  gap: 4px;
}
.th-pagination nav button {
  min-width: 34px;
  min-height: 34px;
  padding: 5px 8px;
  border: 0;
  background: transparent;
}
.th-pagination nav .active {
  background: var(--accent-soft);
  color: var(--accent);
}
.th-error {
  color: var(--danger);
  white-space: pre-wrap;
}
.th-notice {
  margin: 0 0 12px;
  padding: 10px 14px;
}
.th-fab-stack {
  position: fixed;
  right: 28px;
  bottom: max(70px, var(--app-floating-bottom, 0px));
  z-index: 20;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 10px;
}
.th-fab {
  display: flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 50%;
  width: 43px;
  height: 43px;
  box-shadow: var(--shadow);
  font-size: 26px;
}
.th-fab-delete {
  background: #ffb4001f;
  color: #bd8500;
}
.th-fab-action {
  width: auto;
  gap: 8px;
  padding: 0 14px;
  border-radius: 6px;
}
.th-fab-action span:last-child {
  font-size: 13px;
  white-space: nowrap;
}
.th-fab-redo {
  background: #56ca001f;
  color: #439d00;
}
.th-fab-queue {
  background: var(--accent);
  color: white;
  width: 56px;
  height: 56px;
}
.th-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: color-mix(in srgb, var(--text) 50%, transparent);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.th-overlay > section {
  position: relative;
  background: var(--surface);
  border-radius: 6px;
  box-shadow: var(--shadow);
  width: 800px;
  max-width: 100%;
  max-height: 90dvh;
  overflow: auto;
}
.th-overlay h2 {
  font-size: 20px;
  font-weight: 500;
  margin: 0;
}
.th-close {
  position: absolute;
  right: 12px;
  top: 12px;
  border: 0;
  background: var(--surface);
  padding: 0 8px;
  font-size: 24px;
  min-width: 36px;
  min-height: 36px;
  border-radius: 6px;
  color: var(--muted);
}
.th-delete-overlay {
  align-items: flex-end;
  padding-bottom: 0;
}
.th-overlay .th-delete-sheet {
  width: 1300px;
  padding: 25px 25px 20px;
  border-radius: 6px 6px 0 0;
  text-align: center;
}
.th-delete-sheet h2 {
  padding: 5px 25px 15px;
}
.th-delete-actions {
  display: flex;
  gap: 12px;
  justify-content: center;
  margin-top: 10px;
}
.th-delete-actions button {
  border: 0;
  color: #fff;
  padding: 10px 15px;
  font-size: 13px;
}
.th-delete-record {
  background: var(--accent);
}
.th-delete-source {
  background: #ffb400;
}
.th-delete-dest {
  background: #16b1ff;
}
.th-delete-all {
  background: var(--danger, #ff4c51);
}
.th-overlay .th-progress {
  width: 480px;
  padding: 30px;
  text-align: center;
}
.th-progress h2 {
  margin-bottom: 20px;
  font-size: 15px;
}
progress {
  width: 100%;
  height: 6px;
  border: 0;
  border-radius: 6px;
  accent-color: var(--accent);
}
.th-progress p,
.th-queue-count {
  font-size: 12px;
  color: var(--muted);
}
.th-queue-dialog header {
  padding: 23px;
  border-bottom: 1px solid var(--line);
}
.th-queue-body {
  padding: 18px 22px 25px;
}
.th-queue-body + .th-queue-body {
  padding-top: 0;
}
.th-queue-count {
  text-align: right;
  margin: 4px 0 12px;
}
.th-queue-tabs {
  display: flex;
  overflow: auto;
  border-bottom: 1px solid var(--line);
  margin-bottom: 10px;
}
.th-queue-tabs button {
  border: 0;
  background: transparent;
  border-radius: 0;
  white-space: nowrap;
  padding: 10px 15px;
}
.th-queue-tabs .active {
  border-bottom: 2px solid var(--accent);
  color: var(--accent);
}
.th-queue-tabs small {
  display: block;
  font-size: 10px;
  color: var(--muted);
  margin-top: 5px;
}
.th-queue-file {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 15px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
  flex-wrap: wrap;
}
.th-queue-file-name {
  min-width: 0;
  overflow-wrap: anywhere;
  flex: 1;
  font-size: 13px;
}
.th-queue-file small {
  flex-basis: 100%;
}
.th-queue-file-name small {
  display: block;
  margin-top: 6px;
  color: var(--muted);
  font-size: 11px;
}
.th-queue-file-name .th-status {
  margin-left: 8px;
}
.th-queue-cancel {
  border: 0;
  background: none;
  font-size: 22px;
  padding: 0 7px;
}
.th-file-progress {
  flex-basis: 100%;
  text-align: center;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
}
@media (max-width: 900px) {
  .th-pagination nav button:not(.active):not([aria-label='上一页']):not([aria-label='下一页']) {
    display: none;
  }
  .th-table-wrap {
    height: calc(100dvh - 270px);
  }
  .th-delete-actions {
    flex-direction: column;
  }
  .th-delete-actions button {
    margin: 0;
  }
  .th-pagination {
    gap: 10px;
  }
  .th-fab-stack {
    right: 15px;
    bottom: 65px;
  }
}
</style>
