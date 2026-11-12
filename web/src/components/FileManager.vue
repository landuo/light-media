<script setup lang="ts">
import { VVirtualScroll } from 'vuetify/components/VVirtualScroll';
import { VTextField } from 'vuetify/components/VTextField';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, apiURL, bytes, date, errorText, image, label, toast, ui } from '../api';
import {
  browserRootForPath,
  cleanBrowserPath,
  commonDownloadPath,
  filterBrowserItems,
  isSelectableBrowserItem,
  retainBrowserSelection,
  virtualBrowserChildren,
  type BrowserItem,
  type BrowserRoot,
} from '../features/file-browser';
import ReorganizeDialog from './ReorganizeDialog.vue';
import FileDirectoryTree from './FileDirectoryTree.vue';
import FileManagerIcon from './FileManagerIcon.vue';
import AppIcon from './AppIcon.vue';
const props = defineProps<{ initialPaths?: string[] }>();
defineEmits<{ history: []; job: [id: string] }>();
const roots = ref<BrowserRoot[]>([]),
  currentPath = ref('/'),
  listing = ref<BrowserItem[]>([]),
  detailFile = ref<BrowserItem | null>(null);
const loading = ref(true),
  error = ref(''),
  truncated = ref(false),
  filter = ref(''),
  ignoreCase = ref(true);
const sorting = ref(localStorage.getItem('fileBrowser.sort') === 'time' ? 'time' : 'name');
const tree = ref(localStorage.getItem('fileBrowser.showDirTree') === 'true'),
  selectMode = ref(false),
  selected = ref<string[]>([]),
  refreshKey = ref(0);
const virtualList = ref<InstanceType<typeof VVirtualScroll>>();
const selectedSet = computed(() => new Set(selected.value));
const selectablePaths = computed(
  () => new Set(listing.value.filter(isSelectableBrowserItem).map((item) => item.path)),
);
async function navigateRows(event: KeyboardEvent, index: number) {
  const target =
    event.key === 'ArrowDown'
      ? index + 1
      : event.key === 'ArrowUp'
        ? index - 1
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? items.value.length - 1
            : -1;
  if (target < 0 || target >= items.value.length) return;
  event.preventDefault();
  virtualList.value?.scrollToIndex(target);
  await nextTick();
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      if (alive) virtualList.value?.$el.querySelector(`[data-file-index="${target}"]`)?.focus();
    }),
  );
}
const activeRoot = computed(() => browserRootForPath(roots.value, currentPath.value));
const canManage = computed(() => !!activeRoot.value && activeRoot.value.kind !== 'container');
const items = computed(() => {
  const rows = filterBrowserItems(listing.value, filter.value, ignoreCase.value);
  return [
    ...rows.filter((item) => item.type === 'dir'),
    ...rows.filter((item) => item.type !== 'dir'),
  ];
});
const crumbs = computed(() =>
  currentPath.value
    .split('/')
    .filter(Boolean)
    .map((name, index, parts) => ({ name, path: '/' + parts.slice(0, index + 1).join('/') })),
);
const parentPath = computed(() =>
  detailFile.value
    ? currentPath.value
    : currentPath.value.substring(0, currentPath.value.lastIndexOf('/')) || '/',
);
const chosenPaths = ref<string[]>([]),
  organizeDialog = ref(false);
const organizeItems = computed(() =>
  listing.value.filter((item) => chosenPaths.value.includes(item.path)),
);
const detailImage = computed(() =>
  /\.(png|jpg|jpeg|gif|bmp|webp)$/i.test(detailFile.value?.name || ''),
);
let generation = 0;
let alive = true;
async function load(path = currentPath.value) {
  if (!alive) return;
  const run = ++generation;
  const nextPath = cleanBrowserPath(path);
  loading.value = true;
  error.value = '';
  detailFile.value = null;
  if (nextPath !== currentPath.value) selected.value = [];
  currentPath.value = nextPath;
  try {
    const root = browserRootForPath(roots.value, currentPath.value);
    if (root) {
      const response = await api(
        '/files/list?' +
          new URLSearchParams({
            root_id: root.id,
            path: currentPath.value,
            sort: sorting.value,
            order: 'asc',
          }),
      );
      if (run !== generation) return;
      listing.value = response.items || [];
      truncated.value = !!response.truncated;
    } else {
      listing.value = virtualBrowserChildren(roots.value, currentPath.value);
      truncated.value = false;
    }
    selected.value = canManage.value ? retainBrowserSelection(selected.value, listing.value) : [];
    refreshKey.value++;
  } catch (e) {
    if (run === generation) {
      error.value = errorText(e);
      listing.value = [];
      selected.value = [];
    }
  } finally {
    if (run === generation) loading.value = false;
  }
}
function openItem(item: BrowserItem) {
  if (canManage.value && selectMode.value && isSelectableBrowserItem(item)) {
    select(item.path);
    return;
  }
  if (item.type === 'dir') void load(item.path);
  else if (item.type === 'file') {
    detailFile.value = item;
    selected.value = [];
    filter.value = '';
  }
}
function select(path: string, checked = !selectedSet.value.has(path)) {
  if (!canManage.value || !selectablePaths.value.has(path)) return;
  selected.value = checked
    ? [...new Set([...selected.value, path])]
    : selected.value.filter((value) => value !== path);
}
function toggleSelection() {
  selectMode.value = !selectMode.value;
  if (!selectMode.value) selected.value = [];
}
function toggleTree() {
  tree.value = !tree.value;
  localStorage.setItem('fileBrowser.showDirTree', String(tree.value));
}
async function toggleSort() {
  sorting.value = sorting.value === 'name' ? 'time' : 'name';
  localStorage.setItem('fileBrowser.sort', sorting.value);
  await load();
}
function openOrganize(paths: string[]) {
  chosenPaths.value = [...new Set(paths)];
  organizeDialog.value = true;
}
async function organized() {
  organizeDialog.value = false;
  selectMode.value = false;
  await load();
}
async function initial(paths?: string[]) {
  if (!paths?.length) return;
  const root = browserRootForPath(roots.value, paths[0]!);
  if (!root || root.kind === 'container') {
    error.value = '源路径已不在允许目录内，请检查容器挂载和目录配置。';
    return;
  }
  await load(paths[0]!.substring(0, paths[0]!.lastIndexOf('/')) || '/');
  openOrganize(paths);
}
const operation = ref(''),
  operationPaths = ref<string[]>([]),
  operationName = ref(''),
  operationBusy = ref(false),
  operationError = ref('');
function openOperation(action: string, paths: string[] = []) {
  operation.value = action;
  operationPaths.value = [...paths];
  operationName.value = action === 'rename' ? paths[0]?.split('/').pop() || '' : '';
  operationError.value = '';
}
async function executeOperation() {
  if (operationBusy.value || !canManage.value || !activeRoot.value) return;
  operationBusy.value = true;
  operationError.value = '';
  try {
    // Keep durable validation internal; the original interaction confirms once.
    const preview = await api('/files/operation-preview', {
      action: operation.value,
      root_id: activeRoot.value.id,
      path: currentPath.value,
      paths: operationPaths.value,
      name: operationName.value,
    });
    await api('/files/operation-confirm', { plan_id: preview.id, confirm: true });
    toast(
      operation.value === 'mkdir'
        ? '目录已创建'
        : operation.value === 'rename'
          ? '重命名完成'
          : '删除完成',
    );
    operation.value = '';
    await load();
  } catch (e) {
    operationError.value = errorText(e);
  } finally {
    operationBusy.value = false;
  }
}
const scrapePaths = ref<string[]>([]),
  scrapeOpen = ref(false),
  scrapeBusy = ref(false),
  scrapeError = ref('');
function openScrape(paths: string[]) {
  scrapePaths.value = paths;
  scrapeOpen.value = true;
  scrapeError.value = '';
}
async function scrape() {
  scrapeBusy.value = true;
  scrapeError.value = '';
  try {
    const response = await api('/files/scrape', { paths: scrapePaths.value, confirm: true });
    if (!response.jobs?.length) throw new Error('没有找到可刮削的媒体文件');
    const pending = new Set<string>(
      response.jobs.map((job: any) => (typeof job === 'string' ? job : job.id)),
    );
    while (alive && pending.size) {
      for (const id of [...pending]) {
        const detail = await api('/jobs/' + encodeURIComponent(id)),
          job = detail.job || detail;
        if (['failed', 'cancelled', 'needs_review'].includes(job.state))
          throw new Error(job.error_message || '刮削失败');
        if (job.state === 'succeeded') pending.delete(id);
      }
      if (pending.size) await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    if (!alive) return;
    toast('刮削完成');
    scrapeOpen.value = false;
    await load();
  } catch (e) {
    scrapeError.value = errorText(e);
  } finally {
    scrapeBusy.value = false;
  }
}
const recognitionOpen = ref(false),
  recognitionBusy = ref(false),
  recognitionError = ref(''),
  recognized = ref<any>(null),
  recognizedPath = ref('');
async function recognize(item: BrowserItem) {
  recognitionOpen.value = true;
  recognitionBusy.value = true;
  recognitionError.value = '';
  recognized.value = null;
  recognizedPath.value = item.path;
  try {
    recognized.value = await api('/files/recognize', { path: item.path });
  } catch (e) {
    recognitionError.value = errorText(e);
  } finally {
    recognitionBusy.value = false;
  }
}
function contentURL(item: BrowserItem) {
  return apiURL(
    '/files/content?' +
      new URLSearchParams({
        root_id: browserRootForPath(roots.value, item.path)?.id || '',
        path: item.path,
      }),
  );
}
watch(
  () => props.initialPaths,
  (paths) => {
    if (roots.value.length) void initial(paths);
  },
);
onMounted(async () => {
  try {
    const [rootList, settings] = await Promise.all([
      api<BrowserRoot[]>('/files/roots?scope=container'),
      ui.settings ? Promise.resolve(ui.settings) : api('/settings'),
    ]);
    if (!alive) return;
    roots.value = rootList;
    ui.settings = settings;
    if (props.initialPaths?.length) await initial(props.initialPaths);
    else
      await load(
        commonDownloadPath(
          (settings.directories || [])
            .map((directory: any) => directory.source_path || directory.download_path)
            .filter(Boolean),
        ),
      );
  } catch (e) {
    error.value = errorText(e);
    loading.value = false;
  }
});
onUnmounted(() => {
  alive = false;
  generation++;
});
</script>
<template>
  <section class="file-manager" aria-label="文件管理">
    <header class="file-toolbar">
      <nav aria-label="目录路径">
        <button class="storage-home" @click="load('/')"><FileManagerIcon name="local" />本地</button
        ><template v-for="crumb in crumbs" :key="crumb.path"
          ><span>›</span
          ><button :title="crumb.path" @click="load(crumb.path)">{{ crumb.name }}</button></template
        ><template v-if="detailFile"
          ><span>›</span><span class="file-current">{{ detailFile.name }}</span></template
        >
      </nav>
      <div class="toolbar-actions">
        <button
          class="file-icon-button"
          :aria-label="sorting === 'name' ? '改为按时间排序' : '改为按名称排序'"
          :title="sorting === 'name' ? '按名称排序' : '按时间排序'"
          :disabled="loading"
          @click="toggleSort"
        >
          <FileManagerIcon name="sort" /></button
        ><button
          v-if="currentPath !== '/' || detailFile"
          class="file-icon-button"
          aria-label="上一级"
          title="上一级"
          :disabled="loading"
          @click="load(parentPath)"
        >
          <FileManagerIcon name="up" /></button
        ><button
          class="file-icon-button"
          aria-label="新建目录"
          title="新建目录"
          :disabled="loading || !canManage || !!detailFile"
          @click="openOperation('mkdir')"
        >
          <FileManagerIcon name="mkdir" />
        </button>
      </div>
    </header>
    <div class="file-browser-body">
      <FileDirectoryTree
        v-if="tree"
        :roots="roots"
        :current-path="currentPath"
        :refresh-key="refreshKey"
        @navigate="load"
      />
      <div class="file-panel">
        <div class="file-filterbar">
          <button
            class="file-icon-button tree-button"
            :class="{ active: tree }"
            aria-label="切换目录树"
            title="目录树"
            @click="toggleTree"
          >
            <FileManagerIcon name="tree" /></button
          ><template v-if="!detailFile"
            ><FileManagerIcon name="filter" /><input
              v-model="filter"
              aria-label="过滤文件"
              placeholder="过滤文件，支持 * 和 ?" /><template v-if="!selectMode"
              ><button
                class="file-icon-button case-button"
                :class="{ active: ignoreCase }"
                aria-label="切换大小写匹配"
                :title="ignoreCase ? '忽略大小写' : '区分大小写'"
                @click="ignoreCase = !ignoreCase"
              >
                Aa</button
              ><button
                class="file-icon-button"
                aria-label="刷新文件列表"
                title="刷新"
                :disabled="loading"
                @click="load()"
              >
                <FileManagerIcon name="refresh" /></button></template
            ><span v-if="selected.length" class="batch-actions"
              ><button
                class="file-icon-button"
                :aria-label="'刮削所选 ' + selected.length + ' 项'"
                title="刮削"
                @click="openScrape(selected)"
              >
                <FileManagerIcon name="scrape" /></button
              ><button
                class="file-icon-button"
                :aria-label="'整理所选 ' + selected.length + ' 项'"
                title="整理"
                @click="openOrganize(selected)"
              >
                <FileManagerIcon name="organize" /></button
              ><button
                class="file-icon-button danger-action"
                :aria-label="'删除所选 ' + selected.length + ' 项'"
                title="删除"
                @click="openOperation('delete', selected)"
              >
                <FileManagerIcon name="delete" /></button></span
            ><button
              v-if="activeRoot?.kind !== 'container' || selectMode"
              class="file-icon-button"
              :class="{ active: selectMode }"
              :aria-pressed="selectMode"
              :aria-label="selectMode ? '退出多选' : '多选文件'"
              :title="selectMode ? '退出多选' : '多选'"
              @click="toggleSelection"
            >
              <FileManagerIcon :name="selectMode ? 'select-remove' : 'select'" /></button></template
          ><template v-else-if="canManage"
            ><span class="toolbar-space"></span
            ><button
              class="file-icon-button"
              aria-label="识别文件"
              title="识别"
              @click="recognize(detailFile)"
            >
              <FileManagerIcon name="recognize" /></button
            ><a
              class="file-icon-button"
              :href="contentURL(detailFile)"
              :download="detailFile.name"
              title="下载文件"
              aria-label="下载文件"
              ><FileManagerIcon name="download" /></a
          ></template>
        </div>
        <p v-if="error" class="file-error" role="alert">{{ error }}</p>
        <p v-if="truncated" class="file-warning">当前目录超过 10000 项，请按子目录浏览。</p>
        <div v-if="loading" class="file-loading" role="status">正在读取目录…</div>
        <div v-else-if="detailFile" class="file-detail">
          <img
            v-if="canManage && detailImage"
            class="file-image"
            :src="contentURL(detailFile)"
            :alt="detailFile.name"
          />
          <FileManagerIcon v-else :name="detailFile.video ? 'video' : 'file'" />
          <h2>{{ detailFile.name }}</h2>
          <p>
            大小：{{ bytes(detailFile.size || 0) }}<br />修改时间：{{
              date(detailFile.modified || '')
            }}
          </p>
        </div>
        <div v-else class="file-manager-list" role="list" aria-label="文件列表">
          <VVirtualScroll
            v-if="items.length"
            ref="virtualList"
            :key="currentPath + '|' + filter + '|' + ignoreCase"
            :items="items"
            :item-height="62"
            height="100%"
          >
            <template #default="{ item, index }">
              <div
                :key="item.path"
                class="file-row"
                role="listitem"
                :aria-posinset="index + 1"
                :aria-setsize="items.length"
                :class="{ selected: selectedSet.has(item.path) }"
                @click="openItem(item)"
              >
                <label
                  v-if="canManage && selectMode && isSelectableBrowserItem(item)"
                  class="file-selection-control"
                  @click.stop
                >
                  <input
                    type="checkbox"
                    :aria-label="'选择 ' + item.name"
                    :checked="selectedSet.has(item.path)"
                    @change="select(item.path, ($event.target as HTMLInputElement).checked)"
                  /> </label
                ><FileManagerIcon
                  v-else
                  :name="item.type === 'dir' ? 'folder' : item.video ? 'video' : 'file'"
                /><button
                  class="file-row-name"
                  :title="item.path"
                  :data-file-index="index"
                  @keydown="navigateRows($event, index)"
                  @click.stop="openItem(item)"
                >
                  <strong>{{ item.name }}</strong
                  ><small v-if="item.type === 'file' && item.size">{{ bytes(item.size) }}</small>
                </button>
                <div
                  v-if="
                    canManage && !selectMode && !item.virtual && ['dir', 'file'].includes(item.type)
                  "
                  class="file-row-actions"
                  @click.stop
                >
                  <button
                    class="file-icon-button"
                    :aria-label="'识别 ' + item.name"
                    title="识别"
                    @click="recognize(item)"
                  >
                    <FileManagerIcon name="recognize" /></button
                  ><button
                    class="file-icon-button"
                    :aria-label="'刮削 ' + item.name"
                    title="刮削"
                    @click="openScrape([item.path])"
                  >
                    <FileManagerIcon name="scrape" /></button
                  ><button
                    class="file-icon-button"
                    :aria-label="'重命名 ' + item.name"
                    title="重命名"
                    @click="openOperation('rename', [item.path])"
                  >
                    <FileManagerIcon name="rename" /></button
                  ><button
                    class="file-icon-button"
                    :aria-label="'整理 ' + item.name"
                    title="整理"
                    @click="openOrganize([item.path])"
                  >
                    <FileManagerIcon name="organize" /></button
                  ><button
                    class="file-icon-button danger-action"
                    :aria-label="'删除 ' + item.name"
                    title="删除"
                    @click="openOperation('delete', [item.path])"
                  >
                    <FileManagerIcon name="delete" />
                  </button>
                </div>
                <details
                  v-if="
                    canManage && !selectMode && !item.virtual && ['dir', 'file'].includes(item.type)
                  "
                  class="file-mobile-menu"
                  @click.stop
                >
                  <summary :aria-label="item.name + ' 操作'"><AppIcon name="more" /></summary>
                  <div>
                    <button @click="recognize(item)">识别</button
                    ><button @click="openScrape([item.path])">刮削</button
                    ><button @click="openOperation('rename', [item.path])">重命名</button
                    ><button @click="openOrganize([item.path])">整理</button
                    ><button class="danger-action" @click="openOperation('delete', [item.path])">
                      删除
                    </button>
                  </div>
                </details>
              </div>
            </template>
          </VVirtualScroll>
          <div v-if="!items.length && !error" class="file-empty">
            {{ filter ? '没有符合条件的文件' : '空目录' }}
          </div>
        </div>
      </div>
    </div>
    <ReorganizeDialog
      v-if="organizeDialog"
      :paths="chosenPaths"
      :items="organizeItems"
      @close="organizeDialog = false"
      @done="organized"
    />
    <Teleport to="body">
      <div
        v-if="operation"
        class="file-dialog-backdrop"
        @click.self="!operationBusy && (operation = '')"
      >
        <section
          class="file-action-dialog"
          role="dialog"
          aria-modal="true"
          :aria-label="
            operation === 'mkdir' ? '新建目录' : operation === 'rename' ? '重命名' : '确认删除'
          "
        >
          <header>
            <h2>
              {{ operation === 'mkdir' ? '新建目录' : operation === 'rename' ? '重命名' : '确认' }}
            </h2>
            <button
              class="file-icon-button"
              aria-label="关闭"
              :disabled="operationBusy"
              @click="operation = ''"
            >
              <FileManagerIcon name="close" />
            </button>
          </header>
          <form @submit.prevent="executeOperation">
            <p v-if="operationError" class="error-box" role="alert">{{ operationError }}</p>
            <VTextField
              v-if="operation !== 'delete'"
              v-model="operationName"
              autofocus
              :disabled="operationBusy"
              required
              :label="String(operation === 'mkdir' ? '目录名称' : '新名称').trim()"
            />
            <p v-else>
              确认删除{{
                operationPaths.length === 1
                  ? '「' + operationPaths[0]?.split('/').pop() + '」'
                  : '所选 ' + operationPaths.length + ' 项'
              }}？
            </p>
            <footer>
              <button type="button" :disabled="operationBusy" @click="operation = ''">取消</button
              ><button
                :class="operation === 'delete' ? 'danger' : 'primary'"
                :disabled="operationBusy"
              >
                {{
                  operationBusy
                    ? '处理中…'
                    : operation === 'mkdir'
                      ? '创建'
                      : operation === 'rename'
                        ? '重命名'
                        : '确认'
                }}
              </button>
            </footer>
          </form>
        </section>
      </div>
      <div
        v-if="scrapeOpen"
        class="file-dialog-backdrop"
        @click.self="!scrapeBusy && (scrapeOpen = false)"
      >
        <section class="file-action-dialog" role="dialog" aria-modal="true" aria-label="确认刮削">
          <header>
            <h2>{{ scrapeBusy ? '处理中' : '确认' }}</h2>
            <button
              class="file-icon-button"
              aria-label="关闭"
              :disabled="scrapeBusy"
              @click="scrapeOpen = false"
            >
              <FileManagerIcon name="close" />
            </button>
          </header>
          <p v-if="scrapeError" class="error-box" role="alert">{{ scrapeError }}</p>
          <p>
            {{
              scrapeBusy
                ? '正在刮削 ...'
                : scrapePaths.length === 1
                  ? '确认刮削「' + scrapePaths[0]?.split('/').pop() + '」？'
                  : '确认刮削所选 ' + scrapePaths.length + ' 项？'
            }}
          </p>
          <footer>
            <button :disabled="scrapeBusy" @click="scrapeOpen = false">取消</button
            ><button class="primary" :disabled="scrapeBusy" @click="scrape">确认</button>
          </footer>
        </section>
      </div>
      <div
        v-if="recognitionOpen"
        class="file-dialog-backdrop"
        @click.self="recognitionOpen = false"
      >
        <section
          class="file-action-dialog recognize-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="识别结果"
        >
          <header>
            <h2>识别结果</h2>
            <button
              class="file-icon-button"
              aria-label="关闭识别结果"
              @click="recognitionOpen = false"
            >
              <FileManagerIcon name="close" />
            </button>
          </header>
          <p class="recognize-path">{{ recognizedPath }}</p>
          <p v-if="recognitionBusy">正在识别 ...</p>
          <p v-if="recognitionError" class="error-box" role="alert">{{ recognitionError }}</p>
          <div v-if="recognized" class="recognized-media">
            <img
              v-if="recognized.media?.poster_path"
              :src="image(recognized.media.poster_path, 'w185')"
              alt=""
            />
            <div>
              <h3>{{ recognized.media?.title || recognized.media?.name || recognized.title }}</h3>
              <p>
                {{ label(recognized.media?.media_type || recognized.media_type) }} ·
                {{ recognized.media?.year || recognized.year }}
              </p>
              <p>TMDB {{ recognized.media?.id || recognized.tmdb_id }}</p>
              <p
                v-if="
                  (recognized.media?.media_type || recognized.media_type) === 'tv' &&
                  recognized.season != null
                "
              >
                第 {{ recognized.season }} 季 · {{ (recognized.episodes || []).join(', ') }}
              </p>
              <p>{{ recognized.media?.overview }}</p>
            </div>
          </div>
          <footer><button @click="recognitionOpen = false">关闭</button></footer>
        </section>
      </div>
    </Teleport>
  </section>
</template>
<style scoped>
.file-manager {
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border-radius: 6px;
  overflow: hidden;
  height: calc(
    100dvh - var(--topbar-height, 72px) - var(--content-top, 28px) - var(--content-bottom, 36px)
  );
  box-shadow: var(--shadow);
}
.file-toolbar {
  flex: none;
  min-height: 56px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  border-bottom: 1px solid var(--line);
}
.file-toolbar nav {
  display: flex;
  align-items: center;
  overflow: auto;
  min-width: 0;
  flex: 1;
  gap: 1px;
  scrollbar-width: none;
}
.file-toolbar nav > span {
  color: var(--muted);
}
.file-toolbar nav > button {
  border: 0;
  background: transparent;
  padding: 8px 6px;
  font-size: 14px;
}
.file-toolbar nav .storage-home {
  padding-right: 10px;
  gap: 8px;
  color: var(--text);
}
.file-current {
  font-size: 13px;
  white-space: nowrap;
  margin-left: 7px;
}
.toolbar-actions {
  display: flex;
  flex: none;
  gap: 2px;
}
.file-icon-button {
  width: 38px;
  height: 38px;
  border: 0;
  padding: 7px;
  background: transparent;
  border-radius: 50%;
  color: var(--muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
}
.file-icon-button:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
  border: 0;
}
.file-icon-button.active {
  background: var(--accent-soft);
  color: var(--accent);
}
.danger-action {
  color: var(--danger) !important;
}
.file-browser-body {
  display: flex;
  flex: 1;
  min-height: 0;
}
.file-panel {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  flex: 1;
}
.file-filterbar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 48px;
  background: var(--surface);
}
.file-filterbar > svg {
  width: 18px;
  height: 18px;
  color: var(--muted);
  margin-left: 6px;
}
.file-filterbar input {
  min-width: 60px;
  flex: 1;
  border: 0;
  box-shadow: none;
  padding: 9px 2px;
  background: transparent;
  font-size: 14px;
}
.case-button {
  font-size: 18px;
  color: var(--muted);
}
.case-button.active {
  color: var(--accent);
  background: transparent;
}
.toolbar-space {
  flex: 1;
}
.batch-actions {
  display: flex;
  gap: 2px;
}
.file-manager-list {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.file-row {
  display: flex;
  align-items: center;
  padding: 8px 12px;
  gap: 16px;
  min-height: 64px;
  cursor: pointer;
  transition: background 0.15s;
}
.file-row:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
}
.file-row.selected {
  background: var(--accent-soft);
}
.file-row > svg {
  color: var(--muted);
  width: 24px;
  height: 24px;
  margin-left: 4px;
}
.file-selection-control {
  width: 40px;
  height: 40px;
  margin: 0 -3px 0 -7px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  cursor: pointer;
  flex: none;
}
.file-selection-control:hover,
.file-selection-control:focus-within {
  background: var(--accent-soft);
}
.file-selection-control input {
  width: 22px;
  height: 22px;
  margin: 0;
  padding: 0;
  cursor: pointer;
  accent-color: var(--accent);
  flex: none;
}
.file-row-name {
  display: block;
  min-width: 0;
  flex: 1;
  white-space: normal;
  padding: 6px 0;
  border: 0;
  background: transparent;
  text-align: left;
  border-radius: 0;
}
.file-row-name:hover {
  border: 0;
  background: transparent;
}
.file-row-name strong {
  display: block;
  font-size: 15px;
  line-height: 1.5;
  font-weight: 400;
  overflow-wrap: anywhere;
  color: var(--text);
}
.file-row-name small {
  display: block;
  font-size: 13px;
  color: var(--muted);
  margin-top: 2px;
}
.file-row-actions {
  display: flex;
  visibility: hidden;
  flex: none;
  gap: 2px;
}
.file-row:hover .file-row-actions,
.file-row:focus-within .file-row-actions {
  visibility: visible;
}
.file-row-actions .file-icon-button {
  color: var(--muted);
  width: 38px;
  height: 38px;
  padding: 8px;
}
.file-mobile-menu {
  display: none;
  position: relative;
  flex: none;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
}
.file-mobile-menu summary {
  list-style: none;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  color: var(--muted);
}
.file-mobile-menu[open] summary {
  margin-bottom: 0;
}
.file-mobile-menu[open] > div {
  position: absolute;
  right: 0;
  top: 100%;
  z-index: 3;
  min-width: 130px;
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 6px;
  border-radius: 6px;
  box-shadow: var(--shadow);
}
.file-mobile-menu button {
  width: 100%;
  display: block;
  text-align: left;
  padding: 9px 12px;
  border: 0;
  background: transparent;
}
.file-detail {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 55px 20px;
  text-align: center;
  overflow-wrap: anywhere;
}
.file-image {
  max-width: 100%;
  max-height: 60dvh;
  object-fit: contain;
}
.file-detail > svg {
  width: 68px;
  height: 68px;
  color: var(--muted);
}
.file-detail h2 {
  font-size: 19px;
  font-weight: 600;
  margin: 22px 0 13px;
}
.file-detail p {
  line-height: 1.9;
  color: var(--muted);
  font-size: 13px;
}
.file-loading,
.file-empty {
  padding: 65px 20px;
  text-align: center;
  color: var(--muted);
  font-size: 13px;
}
.file-error {
  margin: 12px 18px;
  border-radius: 7px;
  padding: 12px;
  background: #fff1f1;
  color: var(--danger);
}
.file-warning {
  margin: 12px 18px;
  color: #926b14;
  font-size: 12px;
}
.file-dialog-backdrop {
  position: fixed;
  inset: 0;
  background: color-mix(in srgb, var(--text) 50%, transparent);
  z-index: 120;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.file-action-dialog {
  width: min(500px, 100%);
  max-height: 85dvh;
  overflow: auto;
  padding: 24px;
  border-radius: 6px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.file-action-dialog header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 15px;
  gap: 12px;
}
.file-action-dialog h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 500;
}
.file-action-dialog label {
  display: block;
  font-size: 13px;
}
.file-action-dialog label input {
  margin-top: 8px;
}
.file-action-dialog footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 24px;
}
.file-action-dialog > p,
.file-action-dialog form > p {
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.recognize-dialog {
  width: min(680px, 100%);
}
.recognize-path {
  font-size: 11px;
  color: var(--muted);
}
.recognized-media {
  display: flex;
  gap: 20px;
}
.recognized-media img {
  width: 120px;
  height: 180px;
  border-radius: 7px;
  object-fit: cover;
}
.recognized-media h3 {
  margin-top: 0;
}
.recognized-media p {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
}
@media (max-width: 760px) {
  .file-manager {
    height: calc(
      100dvh - var(--topbar-height, 64px) - var(--content-top, 27px) - var(--content-bottom, 35px)
    );
  }
  .file-toolbar {
    padding: 7px 8px;
    gap: 4px;
  }
  .file-toolbar nav > button:not(.storage-home),
  .file-toolbar nav > span:not(.file-current) {
    display: none;
  }
  .file-toolbar nav .file-current {
    display: none;
  }
  .file-toolbar nav .storage-home {
    font-size: 14px;
  }
  .tree-button {
    display: none;
  }
  .file-filterbar {
    padding: 6px;
    gap: 2px;
  }
  .file-row {
    gap: 12px;
    min-height: 62px;
    padding: 7px 12px;
  }
  .file-row-name strong {
    font-size: 13px;
  }
  .file-row-actions {
    display: none;
  }
  .file-mobile-menu {
    display: block;
  }
  .file-icon-button {
    width: 40px;
    height: 40px;
    padding: 8px;
  }
  .file-mobile-menu summary {
    width: 42px;
    height: 42px;
    border-radius: 9px;
  }
  .file-filterbar > svg {
    display: none;
  }
  .file-mobile-menu button {
    min-height: 42px;
  }
  .file-selection-control {
    width: 44px;
    height: 44px;
  }
  .file-action-dialog {
    padding: 18px;
  }
}
</style>
