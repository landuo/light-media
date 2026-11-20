<script setup lang="ts">
import { useDisplay } from 'vuetify';
import { VBottomSheet } from 'vuetify/components/VBottomSheet';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VWindow, VWindowItem } from 'vuetify/components/VWindow';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { VChip } from 'vuetify/components/VChip';
import { VIcon } from 'vuetify/components/VIcon';
import { VDivider } from 'vuetify/components/VDivider';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { VSelect } from 'vuetify/components/VSelect';
import { VCombobox } from 'vuetify/components/VCombobox';
import { VCheckboxBtn } from 'vuetify/components/VCheckbox';
import { VDataTableVirtual } from 'vuetify/components/VDataTable';
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
const display = useDisplay();
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
  tableSort = ref<{ key: string; order?: boolean | 'asc' | 'desc' }[]>([]),
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
  queueActionError = ref(''),
  queueCancelling = ref(''),
  queueGroups = ref<any[]>([]),
  queuePreparations = ref<QueuePreparation[]>([]),
  activeQueue = ref('');
const items = computed(() => result.value?.items || []),
  total = computed(() => result.value?.total || 0),
  pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value))),
  pageButtons = computed(() => historyPageButtons(page.value, pages.value)),
  tableItems = computed(() =>
    items.value.map((item) => ({ ...item, historyGroup: historyTitle(item) })),
  ),
  groupBy = computed(() => (grouped.value ? [{ key: 'historyGroup', order: false as const }] : [])),
  groups = computed(
    () => new Map(historyGroups(items.value).map((group) => [group.title, group.items])),
  ),
  // Keep the existing title/season/episode and locale ordering. Vuetify owns
  // header state/direction; rank raw rows with the shared domain comparator.
  sortRanks = computed(
    () =>
      new Map(
        (tableSort.value[0]
          ? sortHistory(items.value, tableSort.value[0].key as HistorySort, false)
          : items.value
        ).map((item, index) => [item.id, index]),
      ),
  ),
  headers = computed(() =>
    [
      {
        title: grouped.value ? '季集/类别' : '标题',
        key: 'title',
        cellProps: { class: 'th-title-cell' },
      },
      { title: '路径', key: 'source' },
      { title: '转移方式', key: 'mode' },
      { title: '大小', key: 'size', cellProps: { class: 'th-nowrap' } },
      { title: '时间', key: 'updated_at', cellProps: { class: 'th-date' } },
      { title: '状态', key: 'status' },
    ]
      .map((header) => ({
        ...header,
        sortable: true,
        sortRaw: (a: HistoryRow, b: HistoryRow) =>
          (sortRanks.value.get(a.id) ?? 0) - (sortRanks.value.get(b.id) ?? 0),
      }))
      .concat([
        {
          title: '',
          key: 'actions',
          cellProps: { class: 'th-menu-cell' },
          sortable: false,
          sortRaw: () => 0,
        },
      ]),
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
  queueProgress = computed(() =>
    queueFiles.value.length ? (completedQueue.value / queueFiles.value.length) * 100 : 0,
  );
const pendingPreparations = new Set<string>();
let queueGeneration = 0,
  searchTimer: number | undefined,
  pollTimer: number | undefined,
  historyRequest: AbortController | undefined,
  preparationRequest: AbortController | undefined,
  queueRequest: AbortController | undefined,
  stopped = false,
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
function queueStateColor(file: any) {
  const state = transferQueueState(file);
  return state === 'running'
    ? 'primary'
    : state === 'completed'
      ? 'success'
      : state === 'failed' || state === 'cancelled'
        ? 'error'
        : 'secondary';
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
function rowsInGroup(title: unknown) {
  return groups.value.get(String(title)) || [];
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
function reorganize(rows: HistoryRow[]) {
  openMenu.value = '';
  // Selection is authoritative: organize exactly the chosen rows, no expansion.
  if (!rows.length) return;
  if (rows.length === 1) selected.value = [];
  reorganizeItems.value = rows;
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
  if (stopped) return;
  if (silent && (historyRequest || searchTimer !== undefined || composing.value || deleting.value))
    return;
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
function setSearchValue(value: unknown) {
  query.value = typeof value === 'string' ? value : '';
  search();
}
function endComposition(event: CompositionEvent) {
  // VCombobox emits model updates while composing; use the final native value
  // as well so the trailing event order never submits a partial IME syllable.
  composing.value = false;
  setSearchValue((event.target as HTMLInputElement).value);
}
function clearSearch() {
  query.value = '';
  searchNow();
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
  if (stopped || !pendingPreparations.size || preparationRequest) return;
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
  if (stopped || queueRequest) return;
  const request = new AbortController();
  queueRequest = request;
  const run = ++queueGeneration;
  queueLoading.value = true;
  queueError.value = '';
  try {
    const next = await api<{ groups: TransferQueueGroup[]; preparations?: QueuePreparation[] }>(
      '/history/transfers/queue',
      undefined,
      undefined,
      request.signal,
    );
    if (request.signal.aborted || run !== queueGeneration) return;
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
    if (preparationEnded || fileEnded) {
      refreshAfterPreparation = true;
      void load(true);
    }
  } catch (e) {
    if (!request.signal.aborted && run === queueGeneration) queueError.value = errorText(e);
  } finally {
    if (queueRequest === request) queueRequest = undefined;
    if (run === queueGeneration) queueLoading.value = false;
  }
}
async function showQueue() {
  queueOpen.value = true;
  await loadQueue();
}
async function cancelQueueFile(file: any) {
  if (!file.can_cancel || queueCancelling.value || isQueueComplete(file.status)) return;
  // Queue IDs identify executions, not the stable history record or media tab.
  queueCancelling.value = file.id;
  queueActionError.value = '';
  try {
    await api('/history/transfers/queue/' + encodeURIComponent(file.id) + '/cancel', {});
    await loadQueue();
    await load(true);
  } catch (e) {
    queueActionError.value = errorText(e);
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
  // Vuetify owns modal Escape/focus handling; persistent deletion stays open.
}
onMounted(() => {
  void load();
  pollTimer = window.setInterval(() => {
    void pollPreparations();
    if (queueOpen.value) void loadQueue();
    if (refreshAfterPreparation || items.value.some((item) => item.status === 'pending'))
      void load(true);
  }, 3000);
  window.addEventListener('hashchange', restoreURL);
  document.addEventListener('click', dismissMenu);
  document.addEventListener('keydown', escape);
});
onUnmounted(() => {
  stopped = true;
  window.clearInterval(pollTimer);
  historyRequest?.abort();
  preparationRequest?.abort();
  queueRequest?.abort();
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
          <VCombobox
            :model-value="query || null"
            :items="hints"
            :return-object="false"
            label="搜索"
            placeholder="搜索（支持 * ? 通配符）"
            aria-label="搜索（支持 * ? 通配符）"
            variant="outlined"
            density="compact"
            hide-details
            clearable
            clear-icon="$clear"
            prepend-inner-icon="mdi-magnify"
            @update:model-value="setSearchValue"
            @compositionstart="beginComposition"
            @compositionend="endComposition"
            @keydown.enter="searchNow"
            @click:clear="clearSearch"
          />
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
      <VDataTableVirtual
        v-model="selected"
        v-model:sort-by="tableSort"
        class="th-table"
        :headers="headers"
        :items="tableItems"
        :group-by="groupBy"
        :loading="loading"
        :aria-busy="loading"
        :row-props="({ item }) => ({ class: { 'th-selected': selected.includes(item.id) } })"
        item-value="id"
        density="compact"
        fixed-header
        show-select
        must-sort
        hover
        loading-text="加载中…"
        no-data-text="没有数据"
      >
        <template #header.data-table-select>
          <VCheckboxBtn
            aria-label="选择本页全部整理历史"
            :model-value="allSelected"
            :indeterminate="selectedInGroup(items) > 0 && !allSelected"
            @update:model-value="selectAll(!!$event)"
          />
        </template>
        <template #header.data-table-group><span class="sr-only">分组</span></template>
        <template #header.actions><span class="sr-only">操作</span></template>
        <template #group-header="{ item, columns, toggleGroup, isGroupOpen }">
          <tr class="th-group-row">
            <td :colspan="columns.length">
              <div class="th-group-heading">
                <button
                  class="th-expand"
                  :aria-label="(isGroupOpen(item) ? '收起 ' : '展开 ') + item.value"
                  :aria-expanded="isGroupOpen(item)"
                  @click="toggleGroup(item)"
                >
                  {{ isGroupOpen(item) ? '⌄' : '›' }}
                </button>
                <VCheckboxBtn
                  :aria-label="'选择 ' + item.value + ' 全部记录'"
                  :model-value="
                    selectedInGroup(rowsInGroup(item.value)) === rowsInGroup(item.value).length
                  "
                  :indeterminate="
                    selectedInGroup(rowsInGroup(item.value)) > 0 &&
                    selectedInGroup(rowsInGroup(item.value)) < rowsInGroup(item.value).length
                  "
                  @update:model-value="selectGroup(rowsInGroup(item.value), !!$event)"
                />
                <span>{{ item.value }}</span>
              </div>
            </td>
          </tr>
        </template>
        <template #item.data-table-select="{ item }">
          <VCheckboxBtn
            :model-value="selected.includes(item.id)"
            :aria-label="'选择 ' + historyTitle(item) + ' ' + numbers(item)"
            @update:model-value="selectGroup([item], !!$event)"
          />
        </template>
        <template #item.title="{ item }">
          <div class="th-title">
            <svg v-if="item.media.media_type === 'tv'" viewBox="0 0 24 24" aria-hidden="true">
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
              }}<small
                >{{ historyMediaType(item)
                }}<template v-if="item.category"> · {{ item.category }}</template></small
              ></span
            >
          </div>
        </template>
        <template #item.source="{ item }">
          <div class="th-path">
            <span><span class="th-storage">本地</span>{{ item.source }}</span>
            <span class="th-arrow"> ⇒ </span>
            <span v-if="item.target"><span class="th-storage">本地</span>{{ item.target }}</span>
          </div>
        </template>
        <template #item.mode="{ item }"
          ><span class="th-mode">{{ label(item.mode) }}</span></template
        >
        <template #item.size="{ item }">{{ bytes(item.size) }}</template>
        <template #item.updated_at="{ item }">{{ date(item.updated_at) }}</template>
        <template #item.status="{ item }">
          <span
            class="th-status"
            :class="item.status"
            :title="item.error || statusLabel(item)"
            tabindex="0"
            >{{ statusLabel(item) }}</span
          >
        </template>
        <template #item.actions="{ item }">
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
        </template>
        <template #no-data>{{ loading ? '加载中…' : '没有数据' }}</template>
      </VDataTableVirtual>
      <div class="th-pagination">
        <VSelect
          v-model="pageSize"
          label="每页条数"
          aria-label="每页条数"
          variant="outlined"
          hide-details
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
        <span aria-hidden="true">↻</span
        ><span>批量重新整理（{{ selectedItems.length }}）</span></button
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
    <VBottomSheet
      v-if="deleteItems.length"
      :model-value="true"
      inset
      aria-label="删除整理记录"
      @update:model-value="!$event && (deleteItems = [])"
    >
      <VCard class="th-delete-sheet text-center">
        <VBtn
          class="th-dialog-close"
          icon="mdi-close"
          variant="text"
          size="small"
          aria-label="关闭删除窗口"
          @click="deleteItems = []"
        />
        <VCardTitle class="th-delete-title">
          {{
            deleteItems.length === 1
              ? '确认删除 ' + historyTitle(deleteItems[0]!) + ' ' + numbers(deleteItems[0]!) + ' ?'
              : '确认批量删除选中的 ' + deleteItems.length + ' 条整理记录 ?'
          }}
        </VCardTitle>
        <div class="th-delete-actions">
          <VBtn color="primary" :disabled="deleting" @click="remove(false, false)">
            仅删除整理记录
          </VBtn>
          <VBtn color="warning" :disabled="deleting" @click="remove(true, false)">
            删除整理记录和原文件
          </VBtn>
          <VBtn color="info" :disabled="deleting" @click="remove(false, true)">
            删除媒体库文件和整理记录
          </VBtn>
          <VBtn color="error" :disabled="deleting" @click="remove(true, true)">
            删除媒体库文件、整理记录和原文件
          </VBtn>
        </div>
      </VCard>
    </VBottomSheet>
    <VDialog
      v-if="deleting"
      :model-value="true"
      persistent
      max-width="480"
      aria-label="正在删除整理记录"
    >
      <VCard class="th-progress">
        <VCardText aria-live="polite">
          <p>正在删除 {{ deleteProgress.title }} …</p>
          <VProgressLinear
            :model-value="
              deleteProgress.total ? (deleteProgress.done / deleteProgress.total) * 100 : 0
            "
            color="primary"
            aria-label="删除进度"
          />
          <p>{{ deleteProgress.done }} / {{ deleteProgress.total }}</p>
        </VCardText>
      </VCard>
    </VDialog>
    <VDialog
      v-if="queueOpen"
      v-model="queueOpen"
      scrollable
      max-width="60rem"
      :fullscreen="!display.mdAndUp.value"
      aria-label="整理队列"
    >
      <VCard class="th-queue-dialog" width="100%">
        <VCardItem><VCardTitle>整理队列</VCardTitle></VCardItem>
        <VBtn
          class="th-dialog-close"
          icon="mdi-close"
          variant="text"
          size="small"
          aria-label="关闭整理队列"
          @click="queueOpen = false"
        />
        <VProgressLinear
          v-if="queueGroups.length"
          :model-value="queueProgress"
          color="primary"
          aria-label="整体整理进度"
        />
        <VDivider v-else />
        <VCardText class="th-queue-body">
          <p v-if="queueError" class="th-error th-notice" role="alert">{{ queueError }}</p>
          <p v-if="queueActionError" class="th-error th-notice" role="alert">
            {{ queueActionError }}
          </p>
          <VList v-if="queuePreparations.length" aria-label="整理准备任务">
            <VListItem v-for="preparation in queuePreparations" :key="preparation.id">
              <VListItemTitle class="th-queue-file-name">
                {{
                  preparation.history_count
                    ? '重新整理 ' + preparation.history_count + ' 条记录'
                    : preparation.paths[0]
                }}
              </VListItemTitle>
              <VListItemSubtitle class="th-queue-file-name">
                <template v-if="preparation.history_count">{{ preparation.paths[0] }}</template>
                <template v-else-if="preparation.paths.length > 1"
                  >共 {{ preparation.paths.length }} 个选中路径</template
                >
              </VListItemSubtitle>
              <template #append>
                <VChip size="small" color="primary">{{
                  preparationLabels[preparation.state]
                }}</VChip>
              </template>
            </VListItem>
          </VList>
          <template v-if="queueGroups.length">
            <p class="th-queue-count">{{ completedQueue }} / {{ queueFiles.length }}</p>
            <VTabs
              v-model="activeQueue"
              show-arrows
              stacked
              class="v-tabs-pill"
              aria-label="整理影视"
            >
              <VTab
                v-for="group in queueGroups"
                :key="group.id"
                :value="group.id"
                selected-class="v-slide-group-item--active v-tab--selected"
              >
                <div class="th-queue-tab-title">{{ group.title || group.media?.title }}</div>
                <div>
                  ({{ group.files.filter((file: any) => isQueueComplete(file.status)).length }} /
                  {{ group.files.length }})
                </div>
              </VTab>
            </VTabs>
            <VWindow v-model="activeQueue" class="mt-5 disable-tab-transition" :touch="false">
              <VWindowItem v-for="group in queueGroups" :key="group.id" :value="group.id">
                <VList>
                  <VListItem v-for="file in group.files" :key="file.id" class="th-queue-file">
                    <VListItemTitle class="th-queue-file-name" :title="file.source">
                      {{ file.source?.split('/').pop() || file.target?.split('/').pop() }}
                    </VListItemTitle>
                    <VListItemSubtitle class="py-1">
                      文件大小：{{ bytes(file.size || 0) }}
                      <VChip
                        size="small"
                        :color="queueStateColor(file)"
                        class="mx-2"
                        :title="file.error || ''"
                      >
                        {{ queueStatusLabel(file) }}
                      </VChip>
                    </VListItemSubtitle>
                    <div
                      v-if="
                        transferQueueState(file) === 'running' &&
                        typeof file.progress === 'number' &&
                        file.progress >= 0 &&
                        file.progress <= 100
                      "
                      class="th-file-progress"
                    >
                      <VProgressLinear
                        :model-value="file.progress"
                        color="success"
                        :height="3"
                        aria-label="文件整理进度"
                      />
                      <small>{{ file.progress.toFixed(1) }}%</small>
                    </div>
                    <p v-if="file.error" class="th-error" role="alert">{{ file.error }}</p>
                    <template #append>
                      <VBtn
                        icon="mdi-cancel"
                        variant="text"
                        size="small"
                        :aria-label="'取消整理 ' + (file.source?.split('/').pop() || '')"
                        title="取消整理"
                        :loading="queueCancelling === file.id"
                        :disabled="
                          !file.can_cancel || !!queueCancelling || isQueueComplete(file.status)
                        "
                        @click="cancelQueueFile(file)"
                      />
                    </template>
                  </VListItem>
                </VList>
              </VWindowItem>
            </VWindow>
          </template>
          <div v-else-if="!queuePreparations.length" class="th-queue-empty">
            <VIcon icon="mdi-sync" size="30" />
            <div>{{ queueLoading ? '加载中…' : '没有正在整理的任务' }}</div>
          </div>
        </VCardText>
      </VCard>
    </VDialog>
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
  flex: 0 1 400px;
  min-width: 0;
  margin-right: auto;
}
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
.th-group-toggle {
  display: flex;
  padding: 10px 14px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: transparent;
  color: var(--muted);
}
.th-table {
  height: calc(100dvh - 285px);
  min-height: 300px;
}
.th-table :deep(.v-table__wrapper) {
  border-radius: 0;
}
.th-table :deep(th) {
  background: var(--surface-subtle, #f9fafc);
  white-space: nowrap;
}
.th-table :deep(.v-data-table__tr > td) {
  padding-block: 8px;
  font-size: 14px;
}
.th-table :deep(.th-selected) {
  background: var(--accent-soft);
}
.th-group-heading :deep(.v-checkbox-btn) {
  flex: 0 0 auto;
}
.th-table :deep(.th-title-cell) {
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
.th-table :deep(.th-nowrap) {
  white-space: nowrap;
}
.th-table :deep(.th-date) {
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
.th-table :deep(.th-menu-cell) {
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
  width: 120px;
  flex: 0 0 120px;
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
  /* The transparent area beside the queue button overlaps pagination. */
  pointer-events: none;
}
.th-fab {
  pointer-events: auto;
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
.th-dialog-close {
  position: absolute;
  right: 12px;
  top: 12px;
  z-index: 1;
}
.th-delete-sheet {
  padding: 20px;
}
.th-delete-title {
  padding: 8px 36px 16px;
  white-space: normal;
  overflow-wrap: anywhere;
}
.th-delete-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  justify-content: center;
  margin: 12px 0;
}
.th-progress {
  text-align: center;
}
.th-progress p {
  margin: 12px 0;
}
.th-queue-dialog :deep(.v-card-item) {
  padding: 20px 56px 20px 24px;
}
.th-queue-body {
  min-height: 0;
}
.th-queue-count {
  text-align: right;
  color: var(--muted);
  font-size: 12px;
  margin: 0 0 12px;
}
.th-queue-tab-title {
  font-size: 16px;
  font-weight: 600;
}
.th-queue-file {
  padding-block: 12px;
}
.th-queue-file-name {
  white-space: normal;
  overflow-wrap: anywhere;
}
.th-file-progress {
  margin-top: 8px;
  text-align: center;
  color: var(--muted);
}
.th-queue-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  min-height: 208px;
  text-align: center;
  color: var(--muted);
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
  .th-table {
    height: calc(100dvh - 270px);
  }
  .th-delete-actions {
    flex-direction: column;
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
