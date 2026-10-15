<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { api, bytes, date, episodeNumbers, errorText, image, label, toast, ui } from '../api';
import MappingRules from './MappingRules.vue';
import {
  confirmOrganizeEntries,
  fillBatchNumbering,
  previewOrganizeEntries,
} from '../features/file-organize';

const props = defineProps<{ initialPaths?: string[] }>();
const emit = defineEmits<{ history: []; job: [id: string] }>();
const roots = ref<any[]>([]),
  rootID = ref(''),
  listing = ref<any>(null),
  loading = ref(false),
  error = ref('');
const sorting = ref('name'),
  order = ref('asc'),
  filter = ref(''),
  selected = ref<string[]>([]);
const activeRoot = computed(() => roots.value.find((root) => root.id === rootID.value));
const items = computed(() =>
  (listing.value?.items || []).filter((item: any) =>
    item.name.toLowerCase().includes(filter.value.toLowerCase()),
  ),
);
const selectedItems = computed(() =>
  (listing.value?.items || []).filter((item: any) => selected.value.includes(item.path)),
);
const crumbs = computed(() => {
  const root = activeRoot.value;
  if (!root || !listing.value) return [];
  const relative = listing.value.path.slice(root.path.length).split('/').filter(Boolean);
  return [
    { name: root.name, path: root.path },
    ...relative.map((name: string, i: number) => ({
      name,
      path: root.path + '/' + relative.slice(0, i + 1).join('/'),
    })),
  ];
});
let generation = 0;
async function load(path?: string) {
  if (!activeRoot.value) return;
  const run = ++generation;
  loading.value = true;
  error.value = '';
  try {
    const params = new URLSearchParams({
      root_id: rootID.value,
      path: path || activeRoot.value.path,
      sort: sorting.value,
      order: order.value,
    });
    const result = await api('/files/list?' + params);
    if (run !== generation) return;
    listing.value = result;
    selected.value = [];
    filter.value = '';
  } catch (e) {
    if (run === generation) error.value = errorText(e);
  } finally {
    if (run === generation) loading.value = false;
  }
}
function select(path: string, checked: boolean) {
  selected.value = checked
    ? [...new Set([...selected.value, path])]
    : selected.value.filter((value) => value !== path);
}
function all(checked: boolean) {
  selected.value = checked
    ? items.value
        .filter((item: any) => ['dir', 'file'].includes(item.type))
        .map((item: any) => item.path)
    : [];
}
function parent(path: string) {
  return path.substring(0, path.lastIndexOf('/')) || '/';
}
async function initial(paths?: string[]) {
  if (!paths?.length) return;
  const path = paths[0]!;
  const root = [...roots.value]
    .filter((r) => path === r.path || path.startsWith(r.path + '/'))
    .sort((a, b) => b.path.length - a.path.length)[0];
  if (!root) {
    error.value = '该记录的源路径已不在允许目录内，请先检查路径配置。';
    return;
  }
  rootID.value = root.id;
  await load(parent(path));
  selected.value = paths;
  await openOrganize(paths);
}

const organize = ref(false),
  scanning = ref(false),
  scanFiles = ref<any[]>([]),
  scanTruncated = ref(false),
  fileSelection = ref<string[]>([]);
const recursive = ref(true),
  chosenPaths = ref<string[]>([]),
  lookup = ref(''),
  results = ref<any[]>([]),
  identity = ref<any>(null),
  targetPath = ref(''),
  mode = ref(''),
  mediaType = ref(''),
  mediaID = ref(''),
  mediaPicker = ref(false);
const renaming = ref<boolean | null>(null),
  scraping = ref<boolean | null>(null),
  libraryTypeFolder = ref<boolean | null>(null);
const organizeError = ref(''),
  organizeBusy = ref(false),
  plan = ref<any>(null),
  planSelection = ref<string[]>([]),
  submitted = ref<any>(null);
const recognitionMode = ref<'auto' | 'same' | 'individual'>('auto'),
  identifyingPath = ref(''),
  mappings = ref<any[]>([]);
const lookupInput = ref<HTMLInputElement | null>(null);
const batchSeason = ref(1),
  batchNumberMode = ref<'keep' | 'offset' | 'sequential'>('keep'),
  batchOffset = ref(0),
  batchStart = ref(1);
const individualEntries = computed(() =>
  scanFiles.value.filter((file) => fileSelection.value.includes(file.path)),
);
const editingEntry = computed(() =>
  scanFiles.value.find((file) => file.path === identifyingPath.value),
);
const activeIdentity = computed(() =>
  recognitionMode.value !== 'individual' ? identity.value : editingEntry.value?.identity,
);
const activeMappings = computed(() =>
  recognitionMode.value === 'same' ? mappings.value : editingEntry.value?.mappings || [],
);
const executableIndividual = computed(() =>
  individualEntries.value.filter(
    (entry) =>
      !entry.job &&
      entry.plan?.id &&
      entry.selectedIDs?.length &&
      ['ready', 'submit_failed'].includes(entry.state),
  ),
);
const submittedIndividual = computed(() => individualEntries.value.filter((entry) => !!entry.job));
const targetPaths = computed(() => [
  ...new Set(
    (ui.settings?.directories || [])
      .map(
        (row: any) =>
          row.library_path ||
          row.target_path ||
          ui.settings?.roots?.find((root: any) => root.id === row.root_id)?.path,
      )
      .filter(Boolean),
  ),
]);
const organizeRule = computed(() =>
  (ui.settings?.directories || []).find((rule: any) => {
    if (targetPath.value)
      return (
        (rule.library_path ||
          ui.settings?.roots?.find((root: any) => root.id === rule.root_id)?.path) ===
        targetPath.value
      );
    const source = rule.source_path || rule.download_path;
    return (
      source &&
      chosenPaths.value.length &&
      chosenPaths.value.every(
        (path) => path === source || path.startsWith(source.replace(/\/$/, '') + '/'),
      ) &&
      (!mediaType.value || rule.media_type === 'mixed' || rule.media_type === mediaType.value)
    );
  }),
);
const directoryPicker = ref(false),
  pickerRoot = ref(''),
  pickerListing = ref<any>(null),
  pickerLoading = ref(false),
  pickerError = ref('');
async function browseTarget(path?: string) {
  pickerLoading.value = true;
  pickerError.value = '';
  try {
    const root = roots.value.find((row) => row.id === pickerRoot.value);
    if (!root) return;
    pickerListing.value = await api(
      '/files/list?' +
        new URLSearchParams({
          root_id: root.id,
          path: path || root.path,
          sort: 'name',
          order: 'asc',
        }),
    );
  } catch (error) {
    pickerError.value = errorText(error);
  } finally {
    pickerLoading.value = false;
  }
}
async function openTargetPicker() {
  const root =
    [...roots.value]
      .filter((row) => targetPath.value === row.path || targetPath.value.startsWith(row.path + '/'))
      .sort((a, b) => b.path.length - a.path.length)[0] || roots.value[0];
  pickerRoot.value = root?.id || '';
  directoryPicker.value = true;
  await browseTarget(root && targetPath.value ? targetPath.value : undefined);
}
async function openOrganize(paths = selected.value) {
  if (!paths.length) return;
  chosenPaths.value = [...paths];
  organize.value = true;
  identity.value = null;
  plan.value = null;
  submitted.value = null;
  results.value = [];
  scanFiles.value = [];
  organizeError.value = '';
  recognitionMode.value = 'auto';
  mediaType.value = '';
  mediaID.value = '';
  mediaPicker.value = false;
  targetPath.value = '';
  mode.value = '';
  renaming.value = scraping.value = libraryTypeFolder.value = null;
  identifyingPath.value = '';
  mappings.value = [];
  await scan();
}
async function scan() {
  scanning.value = true;
  organizeError.value = '';
  plan.value = null;
  identifyingPath.value = '';
  results.value = [];
  try {
    const result = await api('/files/scan', {
      paths: chosenPaths.value,
      recursive: recursive.value,
    });
    scanTruncated.value = result.truncated;
    scanFiles.value = (result.files || []).map((file: any) => ({
      ...file,
      season: file.numbering?.season ?? file.parsed?.original?.season ?? 1,
      episodes: (file.numbering?.episodes || file.parsed?.original?.episodes || []).join(','),
      identity: null,
      targetPath: targetPath.value,
      mappings: [],
      selectedIDs: [],
      state: 'unidentified',
    }));
    fileSelection.value = scanFiles.value.map((file) => file.path);
    if (!scanFiles.value.length) {
      organizeError.value = '所选项目中没有可识别的视频文件。';
      return;
    }
    lookup.value =
      scanFiles.value[0].inferred_media?.title ||
      scanFiles.value[0].parsed?.title ||
      scanFiles.value[0].path
        .split('/')
        .pop()
        .replace(/\.[^.]+$/, '');
    const inferred = scanFiles.value[0].inferred_media;
    if (recognitionMode.value === 'same') {
      if (inferred?.id) await pick(inferred);
      else if (lookup.value) await searchIdentity();
    }
  } catch (e) {
    organizeError.value = errorText(e);
  } finally {
    scanning.value = false;
  }
}
function invalidateEntry(file: any) {
  if (file.job) return;
  file.plan = undefined;
  file.selectedIDs = [];
  file.error = '';
  file.state = file.identity ? 'identified' : 'unidentified';
  plan.value = null;
}
function invalidatePlans() {
  plan.value = null;
  scanFiles.value.forEach(invalidateEntry);
}
async function changeRecognitionMode() {
  results.value = [];
  identifyingPath.value = '';
  plan.value = null;
  organizeError.value = '';
  if (recognitionMode.value === 'same') {
    lookup.value = scanFiles.value[0]?.parsed?.title || '';
    if (lookup.value) await searchIdentity();
  }
}
async function identifyEntry(file: any) {
  identifyingPath.value = file.path;
  lookup.value =
    file.inferred_media?.title ||
    file.parsed?.title ||
    file.path
      .split('/')
      .pop()
      .replace(/\.[^.]+$/, '');
  results.value = [];
  await nextTick();
  lookupInput.value?.focus();
  lookupInput.value?.scrollIntoView({ block: 'center', behavior: 'smooth' });
  await searchIdentity();
}
function fillNumbering() {
  organizeError.value = '';
  try {
    const paths = fileSelection.value.filter(
      (path) =>
        recognitionMode.value === 'same' ||
        scanFiles.value.find((file) => file.path === path)?.identity?.media_type === 'tv',
    );
    scanFiles.value = fillBatchNumbering(scanFiles.value, paths, {
      season: Number(batchSeason.value),
      mode: batchNumberMode.value,
      offset: Number(batchOffset.value),
      start: Number(batchStart.value),
    });
    plan.value = null;
    toast('已填入批量季集，请逐项核对后预览；预览会验证官方季集');
  } catch (e) {
    organizeError.value = errorText(e);
  }
}
async function searchIdentity() {
  if (!lookup.value.trim()) return;
  organizeBusy.value = true;
  organizeError.value = '';
  try {
    const explicit = lookup.value.trim().match(/^(movie|tv):(\d+)$/i);
    if (explicit) {
      await pick({ media_type: explicit[1]!.toLowerCase(), id: Number(explicit[2]) });
      return;
    }
    results.value =
      (
        await api(
          '/catalog/search?' + new URLSearchParams({ q: lookup.value, kind: 'all', page: '1' }),
        )
      ).results || [];
  } catch (e) {
    organizeError.value = errorText(e);
  } finally {
    organizeBusy.value = false;
  }
}
async function pick(media: any) {
  organizeBusy.value = true;
  organizeError.value = '';
  plan.value = null;
  try {
    const result = await api(
      '/catalog/detail?' + new URLSearchParams({ kind: media.media_type, id: String(media.id) }),
    );
    results.value = [];
    if (recognitionMode.value === 'individual') {
      const entry = editingEntry.value;
      if (!entry || entry.job) return;
      entry.identity = result;
      invalidateEntry(entry);
      return;
    }
    identity.value = result;
    mediaType.value = result.media_type;
    mediaID.value = String(result.id);
    recognitionMode.value = 'same';
    mediaPicker.value = false;
  } catch (e) {
    organizeError.value = errorText(e);
  } finally {
    organizeBusy.value = false;
  }
}
async function preview() {
  if (mediaID.value && !identity.value && recognitionMode.value !== 'individual') {
    if (!mediaType.value) {
      organizeError.value = '指定 TMDB ID 时请选择电影或电视剧';
      return;
    }
    await pick({ media_type: mediaType.value, id: Number(mediaID.value) });
    if (!identity.value) return;
  }
  if (recognitionMode.value === 'same' && !identity.value) {
    organizeError.value = '请填写 TMDB ID 或搜索选择影视，也可切换为自动识别';
    return;
  }
  if (recognitionMode.value === 'individual' || recognitionMode.value === 'auto') {
    organizeBusy.value = true;
    organizeError.value = '';
    try {
      for (const entry of individualEntries.value) {
        entry.targetPath = targetPath.value;
        entry.automatic = recognitionMode.value === 'auto';
        entry.mediaType = mediaType.value;
        entry.renaming = renaming.value;
        entry.scraping = scraping.value;
        entry.libraryTypeFolder = libraryTypeFolder.value;
      }
      await previewOrganizeEntries(individualEntries.value, mode.value, (path, body) =>
        api(path, body),
      );
    } finally {
      organizeBusy.value = false;
    }
    return;
  }
  if (!identity.value || !fileSelection.value.length) return;
  organizeBusy.value = true;
  organizeError.value = '';
  submitted.value = null;
  try {
    const files = scanFiles.value
      .filter((file) => fileSelection.value.includes(file.path))
      .map((file) => ({
        path: file.path,
        season: identity.value.media_type === 'tv' ? Number(file.season) : 0,
        episodes: identity.value.media_type === 'tv' ? episodeNumbers(file.episodes) : [],
      }));
    plan.value = await api('/import/preview', {
      media_type: identity.value.media_type,
      tmdb_id: identity.value.id,
      target_path: targetPath.value,
      mode: mode.value,
      renaming: renaming.value,
      scraping: scraping.value,
      library_type_folder: libraryTypeFolder.value,
      files,
      mappings: mappings.value,
    });
    planSelection.value = plan.value.files
      .filter((file: any) => file.status === 'ready' || file.status === 'already_imported')
      .map((file: any) => file.id);
  } catch (e) {
    organizeError.value = errorText(e);
  } finally {
    organizeBusy.value = false;
  }
}
async function execute() {
  if (recognitionMode.value !== 'same') {
    organizeBusy.value = true;
    organizeError.value = '';
    try {
      await confirmOrganizeEntries(individualEntries.value, (path, body) => api(path, body));
      if (submittedIndividual.value.length)
        toast(`已提交 ${submittedIndividual.value.length} 个文件的整理任务，可在整理历史查看`);
    } finally {
      organizeBusy.value = false;
    }
    return;
  }
  organizeBusy.value = true;
  organizeError.value = '';
  try {
    submitted.value = await api('/import/execute', {
      plan_id: plan.value.id,
      file_ids: planSelection.value,
    });
    toast('整理任务已加入队列，可在整理历史查看进度');
  } catch (e) {
    organizeError.value = errorText(e);
  } finally {
    organizeBusy.value = false;
  }
}
async function startOrganize(background = false) {
  if (organizeBusy.value || scanning.value) return;
  if (
    (recognitionMode.value === 'same' && !plan.value) ||
    (recognitionMode.value !== 'same' && !executableIndividual.value.length)
  )
    await preview();
  if (organizeError.value) return;
  if (
    (recognitionMode.value === 'same' && planSelection.value.length) ||
    (recognitionMode.value !== 'same' && executableIndividual.value.length)
  )
    await execute();
  if (background && (submitted.value || submittedIndividual.value.length)) organize.value = false;
}

const operation = ref(''),
  operationName = ref(''),
  operationPaths = ref<string[]>([]),
  operationPreview = ref<any>(null),
  operationResult = ref<any>(null),
  operationError = ref(''),
  operationBusy = ref(false);
function openOperation(action: string, paths = selected.value) {
  operation.value = action;
  operationPaths.value = [...paths];
  operationName.value = action === 'rename' ? paths[0]?.split('/').pop() || '' : '';
  operationPreview.value = null;
  operationResult.value = null;
  operationError.value = '';
}
async function previewOperation() {
  operationBusy.value = true;
  operationError.value = '';
  try {
    operationPreview.value = await api('/files/operation-preview', {
      action: operation.value,
      root_id: rootID.value,
      path: listing.value.path,
      paths: operationPaths.value,
      name: operationName.value,
    });
  } catch (e) {
    operationError.value = errorText(e);
  } finally {
    operationBusy.value = false;
  }
}
async function confirmOperation() {
  operationBusy.value = true;
  operationError.value = '';
  try {
    operationResult.value = await api('/files/operation-confirm', {
      plan_id: operationPreview.value.id,
      confirm: true,
    });
    await load(listing.value.path);
    toast('文件操作已完成');
  } catch (e) {
    operationError.value = errorText(e);
  } finally {
    operationBusy.value = false;
  }
}
const scrapeDialog = ref(false),
  scrapePaths = ref<string[]>([]),
  scrapeBusy = ref(false),
  scrapeError = ref('');
function openScrape(paths = selected.value) {
  scrapePaths.value = [...paths];
  scrapeDialog.value = true;
  scrapeError.value = '';
}
async function scrape() {
  scrapeBusy.value = true;
  try {
    const result = await api('/files/scrape', { paths: scrapePaths.value, confirm: true });
    toast(`已提交 ${result.jobs.length} 个刮削任务`);
    scrapeDialog.value = false;
  } catch (e) {
    scrapeError.value = errorText(e);
  } finally {
    scrapeBusy.value = false;
  }
}
const recycleDialog = ref(false),
  recycleItems = ref<any[]>([]),
  recycleError = ref(''),
  recycleBusy = ref(false);
async function openRecycle() {
  recycleDialog.value = true;
  recycleBusy.value = true;
  recycleError.value = '';
  try {
    recycleItems.value =
      (await api('/files/recycle?' + new URLSearchParams({ root_id: rootID.value }))).items || [];
  } catch (e) {
    recycleError.value = errorText(e);
  } finally {
    recycleBusy.value = false;
  }
}
async function restore(item: any) {
  if (!window.confirm('将这次删除的文件恢复到原路径？同名文件不会被覆盖。')) return;
  recycleBusy.value = true;
  recycleError.value = '';
  try {
    await api('/files/operation-restore', { plan_id: item.id, confirm: true });
    toast('文件已恢复到原路径');
    await openRecycle();
    await load(listing.value.path);
  } catch (e) {
    recycleError.value = errorText(e);
  } finally {
    recycleBusy.value = false;
  }
}
watch(
  activeMappings,
  () => {
    if (recognitionMode.value === 'same') plan.value = null;
    else if (editingEntry.value) invalidateEntry(editingEntry.value);
  },
  { deep: true },
);
watch(
  () => props.initialPaths,
  (paths) => {
    if (roots.value.length) void initial(paths);
  },
);
onMounted(async () => {
  try {
    roots.value = await api('/files/roots');
    if (!ui.settings) ui.settings = await api('/settings');
    rootID.value = roots.value[0]?.id || '';
    if (props.initialPaths?.length) await initial(props.initialPaths);
    else await load();
  } catch (e) {
    error.value = errorText(e);
  }
});
</script>

<template>
  <section class="file-manager">
    <div class="fm-heading">
      <div>
        <span class="eyebrow">FILES</span>
        <h1>文件管理</h1>
        <p>浏览目录，选择文件后识别、整理或刮削。</p>
      </div>
      <button @click="emit('history')">整理历史</button>
    </div>
    <p v-if="error" class="fm-error" role="alert">{{ error }}</p>
    <div v-if="!roots.length && !loading" class="fm-empty">
      尚未配置浏览目录。请在设置中添加允许的源目录或媒体库目录。
    </div>
    <template v-else>
      <div class="fm-toolbar">
        <label
          >目录
          <select v-model="rootID" @change="load()">
            <option v-for="root in roots" :key="root.id" :value="root.id">
              {{ root.name }} · {{ root.path }}
            </option>
          </select></label
        >
        <div class="fm-spacer"></div>
        <button :disabled="!listing?.parent || loading" @click="load(listing.parent)">
          ↑ 上一级</button
        ><button :disabled="loading" @click="load(listing?.path)">刷新</button
        ><button :disabled="!listing || loading" @click="openRecycle">回收区</button
        ><button :disabled="!listing || loading" @click="openOperation('mkdir')">
          ＋ 新建目录
        </button>
      </div>
      <nav class="fm-crumbs" aria-label="目录面包屑">
        <template v-for="(crumb, i) in crumbs" :key="crumb.path"
          ><span v-if="i">/</span
          ><button @click="load(crumb.path)">{{ crumb.name }}</button></template
        >
      </nav>
      <div class="fm-toolbar fm-filter">
        <input v-model="filter" placeholder="筛选当前目录文件" aria-label="筛选文件" /><label
          >排序
          <select v-model="sorting" @change="load(listing?.path)">
            <option value="name">名称</option>
            <option value="time">修改时间</option>
            <option value="size">大小</option>
          </select></label
        ><button
          @click="
            order = order === 'asc' ? 'desc' : 'asc';
            load(listing?.path);
          "
        >
          {{ order === 'asc' ? '升序 ↑' : '降序 ↓' }}</button
        ><span>{{ items.length }} 项 · 已选 {{ selected.length }} 项</span>
      </div>
      <div class="fm-toolbar fm-selection" v-if="selected.length">
        <strong>已选择 {{ selected.length }} 项</strong><button @click="openOrganize()">整理</button
        ><button @click="openScrape()">刮削</button
        ><button v-if="selected.length === 1" @click="openOperation('rename')">重命名</button
        ><button class="fm-danger" @click="openOperation('delete')">删除</button
        ><button @click="selected = []">取消选择</button>
      </div>
      <p v-if="listing?.truncated" class="fm-warning">
        当前目录超过 10000 项，仅显示前 10000 项，请按子目录浏览。
      </p>
      <div class="fm-table-wrap" :aria-busy="loading">
        <table class="fm-table fm-list-table">
          <thead>
            <tr>
              <th>
                <input
                  type="checkbox"
                  aria-label="选择全部文件"
                  :checked="
                    items.length > 0 &&
                    selected.length ===
                      items.filter((item: any) => ['dir', 'file'].includes(item.type)).length
                  "
                  @change="all(($event.target as HTMLInputElement).checked)"
                />
              </th>
              <th>名称</th>
              <th>大小</th>
              <th>修改时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="item in items"
              :key="item.path"
              :class="{ 'fm-selected': selected.includes(item.path) }"
              @dblclick="
                item.type === 'dir' ? load(item.path) : item.video && openOrganize([item.path])
              "
            >
              <td>
                <input
                  v-if="['dir', 'file'].includes(item.type)"
                  type="checkbox"
                  :aria-label="'选择 ' + item.name"
                  :checked="selected.includes(item.path)"
                  @change="select(item.path, ($event.target as HTMLInputElement).checked)"
                />
              </td>
              <td>
                <button v-if="item.type === 'dir'" class="fm-filename" @click="load(item.path)">
                  <svg
                    class="fm-file-icon folder"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.6"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M3 6h7l2 3h9v11H3V6Z" />
                    <path d="M3 9h18" /></svg
                  >{{ item.name }}</button
                ><span v-else class="fm-file-label"
                  ><svg
                    class="fm-file-icon"
                    :class="{ video: item.video }"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.6"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path
                      v-if="item.video"
                      d="M3 5h18v14H3V5Zm6 0v14m6-14v14M3 10h6m-6 4h6m6-4h6m-6 4h6"
                    />
                    <path v-else d="M5 3h9l5 5v13H5V3Zm9 0v6h5" /></svg
                  >{{ item.name
                  }}<span v-if="item.type === 'symlink'" aria-label="符号链接">↗</span></span
                ><small v-if="item.protected" class="fm-protected">{{ item.protected }}</small>
              </td>
              <td>{{ item.type === 'dir' ? '—' : bytes(item.size) }}</td>
              <td>{{ date(item.modified) }}</td>
              <td>
                <details v-if="['dir', 'file'].includes(item.type)" class="fm-menu">
                  <summary aria-label="文件操作">•••</summary>
                  <div>
                    <button @click="openOrganize([item.path])">整理</button
                    ><button @click="openScrape([item.path])">刮削</button
                    ><button
                      :disabled="!!item.protected"
                      @click="openOperation('rename', [item.path])"
                    >
                      重命名</button
                    ><button
                      class="fm-danger"
                      :disabled="!!item.protected"
                      @click="openOperation('delete', [item.path])"
                    >
                      删除
                    </button>
                  </div>
                </details>
              </td>
            </tr>
            <tr v-if="!items.length">
              <td colspan="5" class="fm-empty">
                {{ loading ? '正在读取目录…' : '当前目录为空或没有匹配文件' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="fm-note">
        媒体库、下载器目录、正在使用的文件及硬链接受到保护。删除经预览确认后移入原目录内的隐藏回收区，可从回收区恢复，保留原始数据。
      </p>
    </template>

    <div v-if="organize" class="fm-overlay" @click.self="organize = false">
      <section
        class="fm-dialog fm-manual"
        :class="{
          'fm-wide': !!plan || individualEntries.some((entry) => entry.plan || entry.error),
        }"
        role="dialog"
        aria-modal="true"
        aria-label="手动整理"
      >
        <header>
          <h2>手动整理</h2>
          <button @click="organize = false" aria-label="关闭手动整理">✕</button>
        </header>
        <div class="fm-dialog-body">
          <p class="fm-note fm-path">{{ chosenPaths.join('、') }}</p>
          <p v-if="organizeError" class="fm-error" role="alert">{{ organizeError }}</p>
          <div v-if="submitted" class="fm-success">
            整理任务已提交。<button @click="emit('job', submitted.id)">查看任务</button
            ><button
              @click="
                organize = false;
                emit('history');
              "
            >
              查看整理历史
            </button>
          </div>
          <template v-else>
            <div class="form-grid fm-manual-fields">
              <label>目的存储<input value="本地" readonly /></label>
              <label
                >整理方式<select v-model="mode" :disabled="organizeBusy" @change="invalidatePlans">
                  <option value="">自动</option>
                  <option value="hardlink">硬链接</option>
                  <option value="copy">复制</option>
                  <option value="auto">硬链接，跨卷时复制</option>
                </select></label
              >
              <label class="fm-span"
                >目的目录
                <div class="fm-path-input">
                  <input
                    v-model.trim="targetPath"
                    list="manual-target-paths"
                    placeholder="自动匹配目录设置"
                    :disabled="organizeBusy"
                    @input="invalidatePlans"
                  />
                  <button type="button" @click="openTargetPicker" :disabled="organizeBusy">
                    浏览
                  </button>
                </div>
                <datalist id="manual-target-paths">
                  <option value="">自动</option>
                  <option
                    v-for="path in targetPaths"
                    :key="String(path)"
                    :value="String(path)"
                  /></datalist
              ></label>
              <label
                >媒体类型<select
                  v-model="mediaType"
                  :disabled="organizeBusy"
                  @change="
                    mediaID = '';
                    identity = null;
                    recognitionMode = 'auto';
                    invalidatePlans();
                  "
                >
                  <option value="">自动</option>
                  <option value="movie">电影</option>
                  <option value="tv">电视剧</option>
                </select></label
              >
              <label
                >TMDB ID
                <div class="fm-path-input">
                  <input
                    v-model.trim="mediaID"
                    inputmode="numeric"
                    :disabled="organizeBusy || !mediaType"
                    placeholder="留空自动识别"
                    @input="
                      identity = null;
                      recognitionMode = mediaID ? 'same' : 'auto';
                      invalidatePlans();
                    "
                  />
                  <button
                    type="button"
                    :disabled="organizeBusy"
                    @click="mediaPicker = !mediaPicker"
                  >
                    搜索
                  </button>
                </div></label
              >
            </div>
            <form v-if="mediaPicker" class="fm-toolbar" @submit.prevent="searchIdentity">
              <input
                ref="lookupInput"
                v-model="lookup"
                placeholder="搜索影视名称"
                aria-label="整理影视名称"
              />
              <button :disabled="organizeBusy || !lookup.trim()">搜索影视</button>
            </form>
            <div v-if="results.length && mediaPicker" class="fm-results">
              <button
                v-for="media in results"
                :key="media.media_type + media.id"
                :disabled="organizeBusy"
                @click="pick(media)"
              >
                <img v-if="media.poster_path" :src="image(media.poster_path, 'w92')" alt="" />
                <span
                  >{{ media.title
                  }}<small>{{ media.year || '' }} · {{ label(media.media_type) }}</small></span
                >
              </button>
            </div>
            <p v-if="identity" class="fm-identity">
              <strong>{{ identity.title }}（{{ identity.year || '—' }}）</strong>
              <span>{{ label(identity.media_type) }} · TMDB {{ identity.id }}</span>
            </p>
            <div class="fm-toolbar fm-organize-switches">
              <label
                ><input
                  type="checkbox"
                  :checked="renaming ?? organizeRule?.renaming ?? true"
                  :disabled="organizeBusy"
                  @change="
                    renaming = ($event.target as HTMLInputElement).checked;
                    invalidatePlans();
                  "
                />智能重命名</label
              >
              <label
                ><input
                  type="checkbox"
                  :checked="scraping ?? organizeRule?.scraping ?? true"
                  :disabled="organizeBusy"
                  @change="
                    scraping = ($event.target as HTMLInputElement).checked;
                    invalidatePlans();
                  "
                />刮削元数据</label
              >
              <label
                ><input
                  type="checkbox"
                  :checked="libraryTypeFolder ?? organizeRule?.library_type_folder ?? false"
                  :disabled="organizeBusy"
                  @change="
                    libraryTypeFolder = ($event.target as HTMLInputElement).checked;
                    invalidatePlans();
                  "
                />按媒体类型建立子目录</label
              >
            </div>
            <details class="fm-preview">
              <summary>
                文件与识别选项 · {{ scanning ? '正在读取…' : scanFiles.length + ' 个视频' }}
              </summary>
              <div class="fm-toolbar">
                <label><input type="checkbox" v-model="recursive" />包含子目录</label
                ><button
                  :disabled="scanning || organizeBusy || submittedIndividual.length > 0"
                  @click="scan"
                >
                  重新扫描选中项目</button
                ><span>{{ scanning ? '正在识别…' : `找到 ${scanFiles.length} 个视频` }}</span>
              </div>
              <p v-if="scanTruncated" class="fm-warning">
                扫描达到范围上限，请缩小选择后重试，避免遗漏文件。
              </p>
              <div class="fm-toolbar">
                <label
                  >识别模式
                  <select
                    v-model="recognitionMode"
                    :disabled="organizeBusy || submittedIndividual.length > 0"
                    @change="changeRecognitionMode"
                  >
                    <option value="auto">自动识别</option>
                    <option value="same">指定同一影视</option>
                    <option value="individual">逐项识别：多部电影 / 不同影视</option>
                  </select></label
                >
              </div>
              <p v-if="recognitionMode === 'individual' && identifyingPath" class="fm-note fm-path">
                正在选择身份：{{ identifyingPath }}
              </p>
              <form
                v-if="recognitionMode === 'individual' && identifyingPath"
                class="fm-toolbar"
                @submit.prevent="searchIdentity"
              >
                <input
                  ref="lookupInput"
                  v-model="lookup"
                  placeholder="影视名称，或 movie:278 / tv:1418"
                  aria-label="整理影视名称"
                /><button :disabled="organizeBusy || !lookup.trim()">搜索影视</button>
              </form>
              <div v-if="results.length && recognitionMode === 'individual'" class="fm-results">
                <button
                  v-for="media in results"
                  :key="media.media_type + media.id"
                  :disabled="organizeBusy"
                  @click="pick(media)"
                >
                  <img
                    v-if="media.poster_path"
                    :src="image(media.poster_path, 'w92')"
                    alt=""
                  /><span
                    >{{ media.title }}
                    <small>{{ media.year || '' }} · {{ label(media.media_type) }}</small></span
                  >
                </button>
              </div>
              <details
                v-if="
                  (recognitionMode === 'same' && identity?.media_type === 'tv') ||
                  (recognitionMode === 'individual' &&
                    scanFiles.some((file) => file.identity?.media_type === 'tv'))
                "
                class="fm-preview"
              >
                <summary>批量季集设置</summary>
                <div class="fm-toolbar">
                  <label
                    >统一季号
                    <input
                      class="fm-number"
                      type="number"
                      v-model.number="batchSeason"
                      min="1"
                      max="1000" /></label
                  ><label
                    >集号方式
                    <select v-model="batchNumberMode">
                      <option value="keep">保留现有集号</option>
                      <option value="offset">原始解析集号 + 偏移</option>
                      <option value="sequential">按文件名自然排序连续编号</option>
                    </select></label
                  ><label v-if="batchNumberMode === 'offset'"
                    >偏移量
                    <input class="fm-number" type="number" v-model.number="batchOffset" /></label
                  ><label v-if="batchNumberMode === 'sequential'"
                    >起始集
                    <input
                      class="fm-number"
                      type="number"
                      v-model.number="batchStart"
                      min="1" /></label
                  ><button :disabled="organizeBusy" @click="fillNumbering">
                    应用到已选剧集文件
                  </button>
                </div>
                <p class="fm-note">
                  原始偏移始终使用文件解析出的集号，不会重复叠加。连续编号按自然名称排序，例如 2 在
                  10 之前。填入后仍可逐文件修改，预览时逐集核对官方元数据。
                </p>
              </details>
              <details v-if="activeIdentity?.media_type === 'tv'" class="fm-preview">
                <summary>高级：原始集号到标准季集映射</summary>
                <fieldset :disabled="organizeBusy" class="fm-fields">
                  <MappingRules
                    :model="activeMappings"
                    :media-id="'tv:' + activeIdentity.id"
                    :season="batchSeason"
                  />
                </fieldset>
                <p class="fm-note">
                  可直接逐文件填标准集号；留空时，只使用已明确确认的映射。不会执行自定义脚本。
                </p>
              </details>
              <div class="fm-table-wrap">
                <table class="fm-table">
                  <thead>
                    <tr>
                      <th>选择</th>
                      <th>源文件</th>
                      <th v-if="recognitionMode === 'individual'">影视身份</th>
                      <th
                        v-if="recognitionMode === 'individual' || identity?.media_type !== 'movie'"
                      >
                        季号
                      </th>
                      <th
                        v-if="recognitionMode === 'individual' || identity?.media_type !== 'movie'"
                      >
                        集号
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="file in scanFiles" :key="file.path">
                      <td>
                        <input
                          type="checkbox"
                          v-model="fileSelection"
                          :value="file.path"
                          :disabled="organizeBusy || !!file.job"
                          @change="plan = null"
                        />
                      </td>
                      <td class="fm-path">
                        {{ file.path
                        }}<small v-if="file.inferred_media"
                          >NFO：{{ file.inferred_media.title }} · TMDB
                          {{ file.inferred_media.id }}</small
                        >
                      </td>
                      <td v-if="recognitionMode === 'individual'">
                        <strong v-if="file.identity"
                          >{{ file.identity.title }} · {{ label(file.identity.media_type) }}</strong
                        ><small v-else>未确认身份</small>
                        <div class="fm-toolbar">
                          <button
                            :disabled="organizeBusy || !!file.job"
                            @click="identifyEntry(file)"
                          >
                            {{ file.identity ? '重新选择' : '识别影视' }}</button
                          ><button
                            v-if="file.inferred_media && !file.job"
                            :disabled="organizeBusy"
                            @click="
                              identifyingPath = file.path;
                              pick(file.inferred_media);
                            "
                          >
                            采用 NFO 身份
                          </button>
                        </div>
                        <p v-if="file.error" class="fm-error">{{ file.error }}</p>
                        <small v-if="file.job" class="fm-success">已提交 · {{ file.job.id }}</small>
                      </td>
                      <td
                        v-if="recognitionMode === 'individual' || identity?.media_type !== 'movie'"
                      >
                        <input
                          v-if="recognitionMode === 'same' || file.identity?.media_type !== 'movie'"
                          class="fm-number"
                          type="number"
                          v-model="file.season"
                          min="1"
                          max="1000"
                          :disabled="organizeBusy || !!file.job"
                          @input="invalidateEntry(file)"
                          :aria-label="file.path + ' 季号'"
                        />
                      </td>
                      <td
                        v-if="recognitionMode === 'individual' || identity?.media_type !== 'movie'"
                      >
                        <input
                          v-if="recognitionMode === 'same' || file.identity?.media_type !== 'movie'"
                          class="fm-episodes"
                          v-model="file.episodes"
                          placeholder="例如 12"
                          :disabled="organizeBusy || !!file.job"
                          @input="invalidateEntry(file)"
                          :aria-label="file.path + ' 集号'"
                        />
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </details>
            <div v-if="plan && recognitionMode === 'same'" class="fm-preview">
              <h3>整理预览</h3>
              <p>
                预估所需 {{ bytes(plan.space_needed) }} · 目标可用 {{ bytes(plan.space_available) }}
              </p>
              <p v-for="warning in plan.warnings" :key="warning" class="fm-warning">
                {{ warning }}
              </p>
              <div v-for="file in plan.files" :key="file.id" class="fm-preview-file">
                <input
                  type="checkbox"
                  v-model="planSelection"
                  :value="file.id"
                  :disabled="file.status === 'blocked'"
                />
                <div>
                  <div class="fm-path">{{ file.source }}</div>
                  <div class="fm-path">→ {{ file.target || '无法生成目标路径' }}</div>
                  <small :class="file.reason ? 'fm-error' : ''">{{
                    file.reason || label(file.method || mode)
                  }}</small>
                </div>
              </div>
            </div>
            <div
              v-if="
                recognitionMode !== 'same' &&
                individualEntries.some((entry) => entry.plan || entry.job || entry.error)
              "
              class="fm-preview"
            >
              <h3>整理预览</h3>
              <p class="fm-note">
                已提交 {{ submittedIndividual.length }} 项 · 可确认
                {{ executableIndividual.length }}
                项。每项独立生成计划；失败项保留原因，已提交项不会再次提交。
              </p>
              <article v-for="entry in individualEntries" :key="entry.path" class="fm-preview-file">
                <div>
                  <strong>{{
                    entry.plan?.media?.title || entry.identity?.title || '自动识别'
                  }}</strong>
                  <p class="fm-path">{{ entry.path }}</p>
                  <p v-for="file in entry.plan?.files || []" :key="file.id" class="fm-path">
                    → {{ file.target || '未生成目标路径' }} ·
                    {{ file.reason || label(file.method || mode) }}
                  </p>
                  <small v-if="entry.plan" class="fm-note"
                    >预估所需 {{ bytes(entry.plan.space_needed) }} · 目标可用
                    {{ bytes(entry.plan.space_available) }}</small
                  >
                  <p
                    v-for="warning in entry.plan?.warnings || []"
                    :key="warning"
                    class="fm-warning"
                  >
                    {{ warning }}
                  </p>
                  <p v-if="entry.error" class="fm-error">{{ entry.error }}</p>
                  <span v-if="entry.job" class="fm-success"
                    >已提交整理任务
                    <button @click="emit('job', entry.job.id)">查看任务</button></span
                  ><span v-else>{{
                    entry.state === 'ready'
                      ? '已通过预览，等待确认'
                      : entry.state === 'previewing'
                        ? '正在预览…'
                        : entry.state === 'submitting'
                          ? '正在提交…'
                          : '需要核对'
                  }}</span>
                </div>
              </article>
            </div>
          </template>
        </div>
        <footer v-if="!submitted">
          <button @click="organize = false">取消</button
          ><button
            :disabled="
              organizeBusy ||
              scanning ||
              (recognitionMode === 'individual' &&
                !individualEntries.some((entry) => entry.identity && !entry.job)) ||
              !fileSelection.length ||
              scanTruncated
            "
            @click="preview"
          >
            {{ organizeBusy ? '处理中…' : '预览结果' }}</button
          ><button
            class="primary"
            :disabled="organizeBusy || scanning || !fileSelection.length || scanTruncated"
            @click="startOrganize(false)"
          >
            {{ organizeBusy ? '处理中…' : '开始整理' }}
          </button>
          <button
            :disabled="organizeBusy || scanning || !fileSelection.length || scanTruncated"
            @click="startOrganize(true)"
          >
            加入整理队列
          </button>
          <button
            v-if="submittedIndividual.length"
            @click="
              organize = false;
              emit('history');
            "
          >
            查看整理历史
          </button>
        </footer>
      </section>
    </div>

    <div v-if="directoryPicker" class="fm-overlay" @click.self="directoryPicker = false">
      <section class="fm-dialog" role="dialog" aria-modal="true" aria-label="选择目的目录">
        <header>
          <h2>选择目的目录</h2>
          <button @click="directoryPicker = false">✕</button>
        </header>
        <div class="fm-dialog-body">
          <select v-model="pickerRoot" @change="browseTarget()" aria-label="浏览目录">
            <option v-for="root in roots" :key="root.id" :value="root.id">{{ root.path }}</option>
          </select>
          <p class="fm-path">{{ pickerListing?.path }}</p>
          <p v-if="pickerError" class="fm-error">{{ pickerError }}</p>
          <div class="fm-target-browser">
            <button v-if="pickerListing?.parent" @click="browseTarget(pickerListing.parent)">
              ↑ 上级目录
            </button>
            <button
              v-for="item in (pickerListing?.items || []).filter((row: any) => row.type === 'dir')"
              :key="item.path"
              @click="browseTarget(item.path)"
            >
              ▸ {{ item.name }}
            </button>
          </div>
          <p v-if="pickerLoading" class="fm-note">正在读取目录…</p>
        </div>
        <footer>
          <button @click="directoryPicker = false">取消</button
          ><button
            class="primary"
            :disabled="pickerLoading || !pickerListing?.path"
            @click="
              targetPath = pickerListing.path;
              invalidatePlans();
              directoryPicker = false;
            "
          >
            选择此目录
          </button>
        </footer>
      </section>
    </div>

    <div v-if="operation" class="fm-overlay" @click.self="operation = ''">
      <section
        class="fm-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="
          operation === 'mkdir' ? '新建目录' : operation === 'rename' ? '重命名' : '删除文件'
        "
      >
        <header>
          <h2>
            {{
              operation === 'mkdir' ? '新建目录' : operation === 'rename' ? '重命名' : '删除文件'
            }}
          </h2>
          <button @click="operation = ''">✕</button>
        </header>
        <div class="fm-dialog-body">
          <p v-if="operationError" class="fm-error" role="alert">{{ operationError }}</p>
          <template v-if="!operationResult"
            ><label v-if="operation !== 'delete'"
              >{{ operation === 'mkdir' ? '目录名称' : '新名称'
              }}<input v-model="operationName" @input="operationPreview = null" autofocus
            /></label>
            <p v-for="path in operationPaths" :key="path" class="fm-path">{{ path }}</p>
            <div v-if="operationPreview">
              <p v-for="warning in operationPreview.warnings" :key="warning" class="fm-warning">
                {{ warning }}
              </p>
              <div v-for="item in operationPreview.items" :key="item.target">
                <p class="fm-path">{{ item.source || '新建目录' }} → {{ item.target }}</p>
                <small v-if="item.manifest?.length"
                  >共 {{ item.manifest.length }} 个文件和目录</small
                >
              </div>
            </div></template
          >
          <div v-else class="fm-success">
            操作已完成。
            <p v-if="operation === 'delete'">数据保留在以下回收路径：</p>
            <p v-for="item in operationResult.items" :key="item.target" class="fm-path">
              {{ item.target }}
            </p>
          </div>
        </div>
        <footer>
          <button @click="operation = ''">{{ operationResult ? '关闭' : '取消' }}</button
          ><button v-if="!operationResult" :disabled="operationBusy" @click="previewOperation">
            预览</button
          ><button
            v-if="operationPreview && !operationResult"
            :disabled="operationBusy"
            :class="operation === 'delete' ? 'fm-danger' : 'primary'"
            @click="confirmOperation"
          >
            确认{{
              operation === 'delete' ? '移入回收区' : operation === 'mkdir' ? '创建' : '重命名'
            }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="scrapeDialog" class="fm-overlay" @click.self="scrapeDialog = false">
      <section class="fm-dialog" role="dialog" aria-modal="true" aria-label="刮削文件">
        <header>
          <h2>刮削元数据</h2>
          <button @click="scrapeDialog = false">✕</button>
        </header>
        <div class="fm-dialog-body">
          <p>
            为所选范围内已登记的媒体补齐
            NFO、海报和背景图。已有非本应用生成的旁车文件遇到冲突时会停止并提示；原视频不改动。
          </p>
          <p v-for="path in scrapePaths" :key="path" class="fm-path">{{ path }}</p>
          <p v-if="scrapeError" class="fm-error" role="alert">{{ scrapeError }}</p>
        </div>
        <footer>
          <button @click="scrapeDialog = false">取消</button
          ><button class="primary" :disabled="scrapeBusy" @click="scrape">确认刮削</button>
        </footer>
      </section>
    </div>
    <div v-if="recycleDialog" class="fm-overlay" @click.self="recycleDialog = false">
      <section class="fm-dialog fm-wide" role="dialog" aria-modal="true" aria-label="文件回收区">
        <header>
          <h2>回收区</h2>
          <button @click="recycleDialog = false">✕</button>
        </header>
        <div class="fm-dialog-body">
          <p class="fm-note">
            显示当前允许根目录最近 100
            次删除记录。文件保留在原目录隐藏回收路径中，恢复时核对完整文件快照；不覆盖现有文件。
          </p>
          <p v-if="recycleError" class="fm-error" role="alert">{{ recycleError }}</p>
          <p v-if="!recycleItems.length">{{ recycleBusy ? '正在加载…' : '回收区为空' }}</p>
          <article v-for="item in recycleItems" :key="item.id" class="fm-preview">
            <div class="fm-toolbar">
              <strong>{{ date(item.created_at) }} · {{ item.items.length }} 项</strong>
              <div class="fm-spacer"></div>
              <button :disabled="recycleBusy" @click="restore(item)">恢复到原路径</button>
            </div>
            <div v-for="file in item.items" :key="file.source">
              <p class="fm-path">{{ file.source }}</p>
              <small class="fm-path">回收位置：{{ file.target }}</small>
            </div>
          </article>
        </div>
        <footer><button @click="recycleDialog = false">关闭</button></footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.fm-manual-fields {
  margin-bottom: 18px;
}
.fm-manual {
  width: 720px;
}
.fm-manual.fm-wide {
  width: 1100px;
}
.fm-span {
  grid-column: 1 / -1;
}
.fm-path-input {
  display: flex;
  gap: 8px;
}
.fm-path-input input {
  min-width: 0;
  flex: 1;
}
.fm-target-browser {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 12px 0;
}
.fm-target-browser button {
  text-align: left;
}
.file-manager {
  max-width: 1500px;
  margin: auto;
}
.fm-heading,
.fm-toolbar,
.fm-dialog header,
.fm-dialog footer,
.fm-identity {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.fm-heading {
  justify-content: space-between;
  margin-bottom: 24px;
}
.fm-heading h1 {
  margin: 5px 0;
  font-size: 28px;
}
.fm-heading p,
.fm-note {
  color: var(--muted, #8c9aac);
}
.fm-toolbar {
  padding: 12px 0;
}
.fm-toolbar input:not([type='checkbox']) {
  min-width: 200px;
}
.fm-toolbar label {
  display: flex;
  gap: 8px;
  align-items: center;
}
.fm-spacer {
  flex: 1;
}
.fm-crumbs {
  display: flex;
  gap: 5px;
  align-items: center;
  flex-wrap: wrap;
  padding: 10px;
  background: var(--panel, #171f2b);
  border: 1px solid var(--line, #2c3545);
  border-radius: 8px;
}
.fm-crumbs button {
  background: none;
  border: 0;
  padding: 4px 8px;
}
.fm-filter {
  border-bottom: 1px solid var(--line, #2c3545);
}
.fm-filter > input {
  flex: 1;
}
.fm-selection {
  padding: 10px 14px;
  margin: 10px 0;
  border-radius: 8px;
  background: var(--accent-soft);
}
.fm-table-wrap {
  overflow: auto;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 10px;
  margin-top: 14px;
}
.fm-table {
  width: 100%;
  border-collapse: collapse;
}
.fm-table th,
.fm-table td {
  padding: 13px 10px;
  text-align: left;
  border-bottom: 1px solid var(--line, #2c3545);
  vertical-align: middle;
}
.fm-table th {
  font-size: 12px;
  color: var(--muted, #9ca8b8);
  white-space: nowrap;
}
.fm-table td:nth-last-child(2) {
  white-space: nowrap;
}
.fm-table th:first-child {
  width: 36px;
}
.fm-table th:last-child {
  width: 80px;
}
.fm-filename {
  border: 0;
  background: none;
  text-align: left;
  padding: 0;
  gap: 10px;
}
.fm-file-label {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}
.fm-file-icon {
  width: 23px;
  height: 23px;
  flex-shrink: 0;
  color: #8894a8;
}
.fm-file-icon.folder {
  color: #b98b3d;
  fill: #fff3d9;
}
.fm-file-icon.video {
  color: #7768bc;
  fill: #f2eeff;
}
.fm-selected {
  background: #f1f0ff;
}
.fm-protected {
  display: block;
  font-size: 11px;
  color: var(--muted, #9aa7ba);
  margin-top: 4px;
}
.fm-empty {
  text-align: center !important;
  padding: 50px !important;
  color: var(--muted, #9aa7ba);
}
.fm-menu {
  position: relative;
  border: 0;
  padding: 0;
  margin: 0;
  background: none;
}
.fm-menu summary {
  cursor: pointer;
  list-style: none;
  padding: 5px 12px;
}
.fm-menu > div {
  position: absolute;
  right: 0;
  top: 30px;
  width: 170px;
  padding: 6px;
  z-index: 8;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: var(--panel, #1c2635);
  box-shadow: 0 8px 24px #16244320;
}
.fm-menu button {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  background: none;
}
.fm-danger {
  color: var(--danger) !important;
}
.fm-error {
  color: var(--danger);
  white-space: pre-wrap;
}
.fm-warning {
  color: #936320;
  font-size: 13px;
}
.fm-success {
  padding: 15px;
  background: #eaf7ef;
  border-radius: 8px;
  color: #23754f;
}
.fm-overlay {
  position: fixed;
  inset: 0;
  background: #17223b77;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.fm-dialog {
  display: flex;
  flex-direction: column;
  background: var(--panel, #182230);
  border: 1px solid var(--line);
  border-radius: 14px;
  width: 620px;
  max-width: 100%;
  max-height: 92dvh;
  box-shadow: 0 20px 80px #16244324;
}
.fm-wide {
  width: 1100px;
}
.fm-dialog header {
  justify-content: space-between;
  padding: 16px 22px;
  border-bottom: 1px solid var(--line);
}
.fm-dialog header h2 {
  margin: 0;
  font-size: 20px;
}
.fm-dialog-body {
  padding: 20px 22px;
  overflow: auto;
}
.fm-dialog footer {
  justify-content: flex-end;
  padding: 14px 22px;
  border-top: 1px solid var(--line);
}
.fm-dialog-body > label {
  display: grid;
  gap: 10px;
}
.fm-results {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 8px;
  max-height: 300px;
  overflow: auto;
}
.fm-results button {
  display: flex;
  align-items: center;
  gap: 10px;
  text-align: left;
}
.fm-results img {
  width: 42px;
  border-radius: 4px;
}
.fm-results small,
.fm-path small {
  display: block;
  color: var(--muted, #9ba9ba);
}
.fm-identity {
  background: #f3f4fb;
  padding: 14px;
  border-radius: 8px;
  margin: 10px 0;
}
.fm-identity span {
  flex: 1;
  color: #5c6880;
  font-size: 13px;
}
.fm-path {
  overflow-wrap: anywhere;
  font-size: 12px;
}
.fm-fields {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.fm-number {
  width: 65px !important;
  min-width: 0 !important;
}
.fm-episodes {
  width: 100px !important;
  min-width: 0 !important;
}
.fm-preview {
  border: 1px solid var(--line);
  border-radius: 9px;
  padding: 15px;
  margin-top: 15px;
}
.fm-preview-file {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 0;
  border-top: 1px solid var(--line);
}
.fm-note {
  font-size: 12px;
  line-height: 1.6;
}
.eyebrow {
  font-size: 11px;
  letter-spacing: 2px;
  color: var(--muted, #8d9cae);
}
@media (max-width: 700px) {
  .fm-overlay {
    padding: 6px;
  }
  .fm-dialog {
    max-height: 97dvh;
  }
  .fm-dialog-body {
    padding: 12px;
  }
  .fm-list-table th:nth-child(4),
  .fm-list-table td:nth-child(4) {
    display: none;
  }
  .fm-toolbar {
    gap: 7px;
  }
  .fm-toolbar select {
    max-width: 280px;
  }
  .fm-heading p {
    font-size: 12px;
  }
}
</style>
