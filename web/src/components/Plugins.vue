<script setup lang="ts">
import {
  ref,
  computed,
  watch,
  onMounted,
  onActivated,
  defineAsyncComponent,
} from "vue";
import { VThemeProvider } from "vuetify/components/VThemeProvider";
import { VCard, VCardTitle, VCardText } from "vuetify/components/VCard";
import { VBtn } from "vuetify/components/VBtn";
import { VTabs, VTab } from "vuetify/components/VTabs";
import { VMenu } from "vuetify/components/VMenu";
import { VIcon } from "vuetify/components/VIcon";
import { VAlert } from "vuetify/components/VAlert";
import { VTextField } from "vuetify/components/VTextField";
import { VCombobox } from "vuetify/components/VCombobox";
import { VSelect } from "vuetify/components/VSelect";
import { VInfiniteScroll } from "vuetify/components/VInfiniteScroll";
import { VDivider } from "vuetify/components/VDivider";
import {
  VList,
  VListItem,
  VListItemTitle,
  VListSubheader,
} from "vuetify/components/VList";
import Draggable from "vuedraggable";
import { VDialog } from "vuetify/components/VDialog";
import { api, errorText, toast } from "../api";
import type { PluginCard } from "../features/plugin-forms";
import { pluginMarketList, pluginMarketLabels, pluginMarketRepositoryOptions, type PluginMarketSort } from "../features/plugin-market";
import { openPluginProject } from '../features/plugin-project';
import AppIcon from "./AppIcon.vue";
import PluginManageCard from "./PluginManageCard.vue";
import PluginFolderCard from "./PluginFolderCard.vue";
import {
  organizationGrid,
  normalizePluginFolders,
  pluginFolderSavePayload,
  copyOrganization,
  folderAppearance,
  folderMembers,
  replaceMembers,
  normalizedPluginOrder,
  moveToFolder,
  folderIcons,
  folderColors,
  folderGradients,
  type PluginOrganization,
  type PluginGridItem,
  type FolderConfig,
} from "../features/plugin-organization";
const emit = defineEmits<{ changed: [] }>();
const PluginMarketSettingDialog = defineAsyncComponent(
  () => import("./PluginMarketSettingDialog.vue"),
);
const PluginVersionHistoryDialog = defineAsyncComponent(
  () => import("./PluginVersionHistoryDialog.vue"),
);
const historyPlugin = ref<PluginCard>();
const historyShowUpdate = ref(false);
const PluginSearchDialog = defineAsyncComponent(() => import('./PluginSearchDialog.vue'));
const searchOpen = ref(false), searchLoading = ref(false), searchError = ref('');
const searchMarket = ref<PluginCard[]>([]);
const searchPlugins = computed(() => [...items.value, ...searchMarket.value.filter(card => !items.value.some(item => item.instance_id === card.instance_id))]);
async function openSearch() {
  searchOpen.value = true;
  if (searchLoading.value) return;
  searchLoading.value = true;
  searchError.value = '';
  try {
    const result = await api('/plugins/market?details=true');
    if (!Array.isArray(result?.items)) throw new Error('插件市场响应无效');
    searchMarket.value = result.items.filter((card: PluginCard) => !card.installed);
    if (result.sources?.some((source: { success: boolean }) => !source.success)) searchError.value = '部分仓库读取失败，搜索结果可能不完整';
  } catch (e) { searchError.value = errorText(e); }
  finally { searchLoading.value = false; }
}
async function searchSelected(plugin: PluginCard) { searchOpen.value = false; await cardAction('open', plugin); }
const marketSettings = ref(false);
async function marketSettingsSaved() {
  marketSettings.value = false;
  market.value = [];
  await load(true);
}
const PluginFolderDialog = defineAsyncComponent(
  () => import("./PluginFolderDialog.vue"),
);

const PluginConfigDialog = defineAsyncComponent(
  () => import("./PluginConfigDialog.vue"),
);
const PluginDataDialog = defineAsyncComponent(
  () => import("./PluginDataDialog.vue"),
);
const PluginLogDialog = defineAsyncComponent(
  () => import("./PluginLogDialog.vue"),
);
const PluginCloneDialog = defineAsyncComponent(
  () => import("./PluginCloneDialog.vue"),
);
const detail = ref<PluginCard>();
const logPlugin = ref<PluginCard>();
const cloneSource = ref<PluginCard>();
async function cloneCreated() {
  // The clone transaction already preserves its source folder. Refresh both
  // lists instead of racing it with a second organization write.
  cloneSource.value = undefined;
  installedQuery.value = null;
  runningFilter.value = false;
  updateFilter.value = false;
  toast("插件分身已创建");
  await load();
}
function unavailable(plugin: PluginCard): boolean {
  if (plugin.available !== false) return false;
  error.value = plugin.unavailable_reason || "插件暂不可用";
  return true;
}
async function openPlugin(plugin: PluginCard) {
  if (unavailable(plugin)) return;
  if (!plugin.installed) { await install(plugin); return; }
  if (plugin.has_page) detail.value = plugin;
  else selected.value = plugin;
}

const tab = ref("installed"),
  query = ref(""),
  marketFilterMenu = ref(false),
  items = ref<PluginCard[]>([]),
  market = ref<PluginCard[]>([]);
const marketFailures = ref<{ repo_url: string; message: string }[]>([]);
const marketAuthors = ref<string[]>([]), marketLabels = ref<string[]>([]), marketRepositories = ref<string[]>([]);
const marketSort = ref<PluginMarketSort>('count'), marketStatistics = ref<Record<string, number>>({});
const marketLimit = ref(20), marketListVersion = ref(0);
const marketSortOptions: {title: string; value: PluginMarketSort}[] = [
  {title:'热度',value:'count'},{title:'名称',value:'name'},{title:'作者',value:'author'},
  {title:'仓库',value:'repo_url'},{title:'最新',value:'add_time'},
];
function selectMarketSort(value: PluginMarketSort) {
  marketSort.value = value;
  marketFilterMenu.value = false;
}
function submitMarketFilter(event: KeyboardEvent) {
  if (!event.isComposing) marketFilterMenu.value = false;
}

const authorOptions = ref<string[]>([]), labelOptions = ref<string[]>([]);
const repositoryOptions = computed(() => pluginMarketRepositoryOptions(market.value));
const marketFiltered = computed(() => pluginMarketList(market.value, {
  name: query.value || '', authors: marketAuthors.value, labels: marketLabels.value, repositories: marketRepositories.value,
}, marketSort.value, marketStatistics.value));
watch([query, marketAuthors, marketLabels, marketRepositories, marketSort, market, marketStatistics], () => { marketLimit.value = 20; marketListVersion.value++; }, { deep: true });
function loadMoreMarket({ done }: { done: (status: 'ok' | 'empty') => void }) {
  if (marketLimit.value >= marketFiltered.value.length) { done('empty'); return; }
  marketLimit.value += 20;
  done('ok');
}
const installedQuery = ref<string | null>(null),
  runningFilter = ref(false),
  updateFilter = ref(false),
  filterMenu = ref(false);
const installedFiltering = computed(
  () => !!installedQuery.value || runningFilter.value || updateFilter.value,
);
function toggleInstalledFilter(filter: "running" | "updates") {
  if (filter === "running") runningFilter.value = !runningFilter.value;
  else updateFilter.value = !updateFilter.value;
  filterMenu.value = false;
}
function submitInstalledFilter(event: KeyboardEvent) {
  if (!event.isComposing) filterMenu.value = false;
}
const loading = ref(true),
  error = ref(""),
  busy = ref(false),
  selected = ref<PluginCard>();
const confirmation = ref<{
  plugin: PluginCard;
  action: "reset" | "uninstall";
}>();
const visible = computed(() => marketFiltered.value.slice(0, marketLimit.value));
const organization = ref<PluginOrganization>({
  revision: 0,
  folders: {},
  order: [],
});
const currentFolder = ref(""),
  sortMode = ref(false),
  dragging = ref(false),
  organizing = ref(false);
const grid = ref<PluginGridItem[]>([]);
const folderEditor = ref<{
  mode: "create" | "rename" | "appearance";
  name: string;
  config?: FolderConfig;
}>();
const folderError = ref(""),
  deleteFolder = ref("");
let dragged: PluginGridItem | undefined,
  droppedIntoFolder = false,
  loadSerial = 0;
function rebuildGrid() {
  if (!dragging.value)
    grid.value = organizationGrid(
      organization.value,
      items.value,
      currentFolder.value,
      installedQuery.value || "",
      { running: runningFilter.value, updates: updateFilter.value },
    );
}
watch(
  [
    organization,
    items,
    currentFolder,
    installedQuery,
    runningFilter,
    updateFilter,
  ],
  rebuildGrid,
  {
    deep: true,
  },
);
watch(tab, () => {
  sortMode.value = false;
  currentFolder.value = "";
  filterMenu.value = false;
  marketFilterMenu.value = false;
  load();
});
let loadInFlight: Promise<void> | undefined;
let reloadRequested = false;
let marketRefreshRequested = false;
function load(forceMarket = false): Promise<void> {
  marketRefreshRequested ||= forceMarket;
  if (loadInFlight) {
    // Explicit actions (including saved sources) require one fresh read after
    // the current request. Discard its old results, but do not amplify requests.
    reloadRequested = true;
    ++loadSerial;
    return loadInFlight;
  }
  loadInFlight = (async () => {
    try {
      do {
        reloadRequested = false;
        const refresh = marketRefreshRequested;
        marketRefreshRequested = false;
        await loadOnce(refresh);
      } while (reloadRequested);
    } finally {
      loadInFlight = undefined;
    }
  })();
  return loadInFlight;
}
async function loadOnce(forceMarket = false) {
  if (organizing.value || sortMode.value || dragging.value) return;
  const serial = ++loadSerial;
  try {
    const installed = await api("/plugins?state=installed");
    if (
      serial !== loadSerial ||
      organizing.value ||
      sortMode.value ||
      dragging.value
    )
      return;
    items.value = installed;
    if (selected.value) {
      const current = installed.find(
        (plugin: PluginCard) =>
          plugin.instance_id === selected.value?.instance_id,
      );
      if (current) selected.value = current;
    }
    {
      // Fixed refreshData reads local installed classes first, then the market.
      const discovered = await api(
        "/plugins/market?details=true" + (forceMarket ? "&force=true" : ""),
      );
      if (serial !== loadSerial) return;
      if (!Array.isArray(discovered?.items) || !Array.isArray(discovered?.sources))
        throw new Error("插件市场响应无效");
      const updates = new Map<string, PluginCard>(
        discovered.items
          .filter((plugin: PluginCard) => plugin.installed && plugin.has_update)
          .map((plugin: PluginCard) => [plugin.instance_id, plugin]),
      );
      // Original fetchUninstalledPlugins transfers upgrade metadata to the
      // installed cards and excludes those cards from the marketplace grid.
      items.value = items.value.map((plugin) => {
        const candidate = updates.get(plugin.instance_id);
        return {
          ...plugin,
          has_update: !!candidate,
          update_blocked_reason: candidate?.update_blocked_reason,
          ...(candidate
            ? {
                repo_url: candidate.repo_url,
                repository_version: candidate.repository_version,
              }
            : {}),
        };
      });
      market.value = discovered.items.filter(
        (plugin: PluginCard) => !(plugin.installed && plugin.has_update),
      );
      // Fixed initOptions retains author/label choices across refreshes;
      // only repository choices rebuild from the current market list.
      for (const card of market.value) {
        if (card.author && !authorOptions.value.includes(card.author)) authorOptions.value.push(card.author);
        for (const label of pluginMarketLabels(card)) if (!labelOptions.value.includes(label)) labelOptions.value.push(label);
      }
      marketFailures.value = discovered.sources
        .filter((source: { success: boolean }) => source.success !== true)
        .map((source: { repo_url: string; message?: string }) => ({
          repo_url: source.repo_url,
          message: source.message || "仓库读取失败，请刷新重试",
        }));
    }
    loading.value = false;
    error.value = '';
      try {
        const statistics = await api('/plugins/statistic');
        if (serial !== loadSerial) return;
        if (!statistics || typeof statistics !== 'object' || Array.isArray(statistics)) throw new Error('插件统计响应无效');
        marketStatistics.value = statistics;
      } catch (e) {
        if (serial !== loadSerial) return;
        // Fixed getPluginStatistics logs failure and retains the last value.
        console.error(e);
      }
    const groups = await api("/plugins/organization");
    if (serial !== loadSerial) return;
    if (groups.revision >= organization.value.revision)
      organization.value = { ...groups, folders: normalizePluginFolders(groups.folders) };
    if (
      currentFolder.value &&
      !Object.hasOwn(groups.folders, currentFolder.value)
    )
      currentFolder.value = "";
    error.value = "";
    emit("changed");
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function persistOrganization(next: PluginOrganization, rollbackOnFailure = false, forceOrder = false): Promise<boolean> {
  if (organizing.value) return false;
  organizing.value = true;
  folderError.value = "";
  error.value = "";
  ++loadSerial;
  const previous = organization.value;
  const orderChanged = forceOrder || JSON.stringify(next.order) !== JSON.stringify(previous.order);
  const foldersChanged = forceOrder || JSON.stringify(next.folders) !== JSON.stringify(previous.folders);
  // The source view mutates before saving. Create/rename/delete roll back;
  // sorting, movement and appearance retain the local mutation on failure.
  organization.value = next;
  try {
    if (orderChanged) {
      const saved = await api("/plugins/organization", { order: next.order }, "PUT");
      organization.value.revision = saved.revision;
    }
    if (foldersChanged) {
      const saved = await api("/plugins/organization", { folders: pluginFolderSavePayload(next) }, "PUT");
      organization.value.revision = saved.revision;
    }
    return true;
  } catch (e) {
    if (rollbackOnFailure) organization.value = { ...previous, revision: organization.value.revision };
    error.value = folderError.value = errorText(e);
    return false;
  } finally {
    organizing.value = false;
    rebuildGrid();
  }
}
function editFolder(mode: "create" | "rename" | "appearance", name = "") {
  folderError.value = "";
  folderEditor.value = {
    mode,
    name,
    config: folderAppearance(organization.value.folders[name]),
  };
}
async function saveFolder(value: { name: string; config: FolderConfig }) {
  const editor = folderEditor.value;
  if (!editor || organizing.value) return;
  const next = copyOrganization(organization.value);
  if (editor.mode !== "appearance") {
    if (!value.name.trim()) {
      folderError.value = "文件夹名称不能为空";
      return;
    }
    if (value.name !== editor.name && Object.hasOwn(next.folders, value.name)) {
      folderError.value = "文件夹已存在";
      return;
    }
    if (editor.mode === "rename" && value.name === editor.name) {
      folderEditor.value = undefined;
      return;
    }
    const folder =
      editor.mode === "create"
        ? {
            plugins: [],
            order: Object.keys(next.folders).length,
            icon: folderIcons[0],
            color: folderColors[0],
            gradient: folderGradients[0],
            background: "",
            showIcon: true,
          }
        : next.folders[editor.name];
    if (editor.mode === "rename") delete next.folders[editor.name];
    next.folders = { ...next.folders, [value.name]: folder };
  } else
    next.folders = {
      ...next.folders,
      [editor.name]: {
        ...folderAppearance(next.folders[editor.name]),
        ...value.config,
        plugins: [...folderMembers(next.folders[editor.name])],
      },
    };
  if (await persistOrganization(next, editor.mode !== "appearance")) {
    if (currentFolder.value === editor.name && editor.mode === "rename")
      currentFolder.value = value.name;
    folderEditor.value = undefined;
    toast(
      editor.mode === "create"
        ? "文件夹创建成功"
        : editor.mode === "rename"
          ? "文件夹重命名成功"
          : "文件夹设置已保存",
    );
  }
}
async function confirmDeleteFolder() {
  if (!deleteFolder.value || organizing.value) return;
  const name = deleteFolder.value,
    next = copyOrganization(organization.value);
  delete next.folders[name];

  if (currentFolder.value === name) currentFolder.value = "";
  if (await persistOrganization(next, true)) {
    deleteFolder.value = "";
    toast("文件夹已删除");
  }
}
function startSort(event: { oldIndex?: number }) {
  dragging.value = true;
  droppedIntoFolder = false;
  dragged = grid.value[event.oldIndex ?? -1];
}
async function endSort() {
  dragging.value = false;
  if (droppedIntoFolder) {
    dragged = undefined;
    rebuildGrid();
    return;
  }
  const next = copyOrganization(organization.value);
  if (currentFolder.value) {
    const sorted = grid.value
      .filter((item) => item.type === "plugin")
      .map((item) => item.id);
    next.folders[currentFolder.value] = replaceMembers(
      next.folders[currentFolder.value],
      sorted,
    );
    const order = normalizedPluginOrder(next);
    const folderOrder =
      order.find(
        (item) => item.type === "folder" && item.id === currentFolder.value,
      )?.order ?? 999;
    sorted.forEach((id, index) => {
      const item = order.find(
        (item) => item.type === "plugin" && item.id === id,
      );
      if (item) item.order = folderOrder + 0.1 + index * 0.01;
      else
        order.push({
          id,
          type: "plugin",
          order: folderOrder + 0.1 + index * 0.01,
        });
    });
    next.order = order;
  } else {
    const sorted = grid.value.map((item, index) => ({
      type: item.type,
      id: item.id,
      order: index,
    }));
    next.order = sorted;
    sorted.forEach((item) => {
      if (item.type === "folder")
        next.folders[item.id] = {
          ...folderAppearance(next.folders[item.id]),
          plugins: [...folderMembers(next.folders[item.id])],
          order: item.order,
        };
    });
  }
  dragged = undefined;
  await persistOrganization(next, false, true);
}
async function dropToFolder(name: string) {
  if (!sortMode.value || dragged?.type !== "plugin" || organizing.value) return;
  droppedIntoFolder = true;
  try {
    const next = moveToFolder(organization.value, dragged.id, name);
    if (await persistOrganization(next)) toast("插件已移动到文件夹");
  } catch (e) {
    error.value = errorText(e);
  }
}
async function cardAction(action: string, plugin: PluginCard) {
  if (sortMode.value || organizing.value || busy.value) return;
  if (
    [
      "open",
      "settings",
      "install",
      "clone",
      "reset",
      "repository-update",
    ].includes(action) &&
    unavailable(plugin)
  )
    return;
  if (action === "open") await openPlugin(plugin);
  else if (action === "detail") detail.value = plugin;
  else if (action === "settings") selected.value = plugin;
  else if (action === "clone" && plugin.installed) cloneSource.value = plugin;
  else if (
    action === "repository-update" &&
    plugin.installed &&
    plugin.repo_url &&
    plugin.repository_version &&
    plugin.has_update &&
    !plugin.update_blocked_reason
  ) {
    error.value = '';
    historyShowUpdate.value = true;
    historyPlugin.value = plugin;
  }
  else if (action === "reset" || action === "uninstall")
    confirmation.value = { plugin, action };
  else if (action === "logs") logPlugin.value = plugin;
  else if (action === "history") {
    error.value = '';
    historyShowUpdate.value = false;
    historyPlugin.value = plugin;
  }
  else if (action === 'project') {
    busy.value = true;
    try { await openPluginProject(plugin, api); }
    catch (e) { error.value = errorText(e); }
    finally { busy.value = false; }
  }
  else if (action === "install") await install(plugin);
  else if (action === "remove-folder" && currentFolder.value) {
    const next = copyOrganization(organization.value),
      name = currentFolder.value;
    next.folders[name] = replaceMembers(
      next.folders[name],
      folderMembers(next.folders[name]).filter(
        (id, index, members) => index !== members.indexOf(plugin.instance_id),
      ),
    );
    if (await persistOrganization(next)) toast("插件已移出文件夹");
  }
}
async function install(plugin: PluginCard) {
  if (unavailable(plugin)) return;
  if (busy.value) return;
  busy.value = true;
  try {
    const result = await api("/plugins/install", {
      plugin_id: plugin.plugin_id,
      ...(plugin.repo_url ? { repo_url: plugin.repo_url } : {}),
    });
    if (result?.success !== true)
      throw new Error(result?.message || "插件安装失败");
    toast(plugin.name + "已安装");
    installedQuery.value = null;
    runningFilter.value = false;
    updateFilter.value = false;
    tab.value = "installed";
    await load();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function updatePlugin(plugin: PluginCard) {
  if (busy.value || unavailable(plugin)) return;
  if (!plugin.installed || !plugin.repo_url || !plugin.repository_version || !plugin.has_update || plugin.update_blocked_reason) return;
  busy.value = true;
  error.value = '';
  try {
    // Fixed PluginCard.updatePlugin asks the repository for its current latest
    // release. An explicit historical release_version belongs to the API only.
    const result = await api('/plugins/install', { plugin_id: plugin.instance_id, repo_url: plugin.repo_url, force: true });
    if (result?.success !== true) throw new Error(result?.message || '插件更新失败');
    toast(plugin.name + '已更新');
    historyPlugin.value = undefined;
    await load();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function confirmAction() {
  const choice = confirmation.value;
  if (!choice || busy.value) return;
  if (choice.action !== "uninstall" && unavailable(choice.plugin)) return;
  busy.value = true;
  error.value = "";
  try {
    const instancePath =
      "/plugins/" + encodeURIComponent(choice.plugin.instance_id);
    const result =
      choice.action === "uninstall"
        ? await api(instancePath, undefined, "DELETE")
        : await api(instancePath + "/" + choice.action, {});
    if (result?.success === false)
      throw new Error(
        result.message ||
          (choice.action === "reset" ? "插件重置失败" : "插件卸载失败"),
      );
    toast(
      choice.action === "reset"
        ? "插件配置及数据已重置"
        : result?.data_retained === true
          ? "插件已卸载，配置及数据保留"
          : result?.data_retained === false
            ? "插件已卸载，配置及数据已清理"
            : "插件已卸载",
    );
    confirmation.value = undefined;
    await load();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  load();
});
let activatedCount = 0;
onActivated(() => {
  // Fixed useKeepAliveRefresh skips the initial activation and refreshes the
  // current view on later returns while retaining filters, tab and folder.
  if (++activatedCount > 1) load();
});
</script>
<template>
  <VThemeProvider theme="light" with-background class="plugin-surface">
    <div class="plugin-toolbar">
      <VTabs v-model="tab" color="primary"
        ><VTab value="installed" prepend-icon="mdi-apps">我的插件</VTab
        ><VTab value="market" prepend-icon="mdi-shopping">插件市场</VTab></VTabs
      >
      <div class="plugin-toolbar-actions">
        <VBtn variant="text" icon="mdi-magnify" aria-label="搜索插件" :disabled="busy || organizing" @click="openSearch" />
        <VBtn
          v-if="tab === 'market'"
          variant="text"
          icon
          aria-label="插件市场设置"
          :disabled="busy || organizing"
          @click="marketSettings = true"
          ><VIcon icon="mdi-store-cog"
        /></VBtn>
        <VMenu
          v-if="tab === 'market'"
          v-model="marketFilterMenu"
          location="bottom end"
          :close-on-content-click="false"
          content-class="plugin-dialog-surface"
        >
          <template #activator="{ props: menuProps }">
            <VBtn
              v-bind="menuProps"
              variant="text"
              icon
              aria-label="筛选插件市场"
              :color="query ? 'primary' : 'secondary'"
              ><VIcon icon="mdi-filter-multiple-outline"
            /></VBtn>
          </template>
          <VCard min-width="260" max-width="320"
            ><div class="pa-3">
              <VTextField
                v-model="query"
                aria-label="搜索插件"
                placeholder="名称"
                prepend-inner-icon="mdi-magnify"
                density="compact"
                hide-details
                clearable
                @keyup.enter="submitMarketFilter"
              />
              </div>
              <VDivider class="mt-2" />
              <VList density="compact" class="px-2 py-1">
                <VListSubheader>排序</VListSubheader>
                <VListItem v-for="option in marketSortOptions" :key="option.value" :active="marketSort === option.value" @click="selectMarketSort(option.value)" density="compact">
                  <VListItemTitle>{{ option.title }}</VListItemTitle>
                  <template #append><VIcon v-if="marketSort === option.value" icon="mdi-check" color="primary" size="small" /></template>
                </VListItem>
              </VList>
              <VDivider />
              <div class="px-3 py-2 d-flex flex-column ga-2">
                <VSelect v-if="authorOptions.length" v-model="marketAuthors" :items="authorOptions" label="作者" multiple chips closable-chips clearable density="compact" variant="outlined" hide-details />
                <VSelect v-if="labelOptions.length" v-model="marketLabels" :items="labelOptions" label="标签" multiple chips closable-chips clearable density="compact" variant="outlined" hide-details />
                <VSelect v-if="repositoryOptions.length" v-model="marketRepositories" :items="repositoryOptions" label="仓库" multiple chips closable-chips clearable density="compact" variant="outlined" hide-details />
              </div
          ></VCard>
        </VMenu>
        <VBtn
          v-if="tab === 'installed'"
          variant="text"
          icon
          aria-label="筛选已安装插件"
          data-menu-activator="installed-filter-btn"
          :color="installedFiltering ? 'primary' : 'secondary'"
          @click="filterMenu = true"
          ><VIcon icon="mdi-filter-multiple-outline"
        /></VBtn>
        <VBtn
          v-if="tab === 'market'"
          variant="text"
          icon
          aria-label="刷新插件"
          :disabled="sortMode || organizing"
          @click="load(true)"
          ><AppIcon name="refresh"
        /></VBtn>
        <VBtn
          v-if="tab === 'installed'"
          variant="text"
          icon
          aria-label="排序插件"
          :disabled="organizing"
          :active="sortMode"
          :color="sortMode ? 'warning' : 'secondary'"
          @click="sortMode = !sortMode"
          ><VIcon icon="mdi-sort-variant"
        /></VBtn>
      </div>
    </div>
    <VMenu
      v-if="tab === 'installed' && filterMenu"
      v-model="filterMenu"
      :close-on-content-click="false"
      activator="[data-menu-activator=installed-filter-btn]"
      location="bottom end"
      content-class="plugin-dialog-surface"
      ><VCard min-width="220"
        ><div class="pa-3">
          <VCombobox
            v-model="installedQuery"
            :items="items.map((plugin) => plugin.name)"
            aria-label="插件名称"
            placeholder="名称"
            prepend-inner-icon="mdi-magnify"
            density="compact"
            variant="outlined"
            hide-details
            clearable
            @keyup.enter="submitInstalledFilter"
          />
        </div>
        <VDivider class="mt-2" />
        <VList density="compact" class="px-2 py-1"
          ><VListSubheader>筛选</VListSubheader>
          <VListItem
            :active="runningFilter"
            aria-label="运行中"
            density="compact"
            @click="toggleInstalledFilter('running')"
            ><template #prepend
              ><VIcon
                icon="mdi-play-circle"
                color="success"
                size="small" /></template
            ><VListItemTitle>运行中</VListItemTitle
            ><template #append
              ><VIcon
                v-if="runningFilter"
                icon="mdi-check"
                color="primary"
                size="small" /></template
          ></VListItem>
          <VListItem
            :active="updateFilter"
            aria-label="有新版本"
            density="compact"
            @click="toggleInstalledFilter('updates')"
            ><template #prepend
              ><VIcon
                icon="mdi-arrow-up-circle"
                color="info"
                size="small" /></template
            ><VListItemTitle>有新版本</VListItemTitle
            ><template #append
              ><VIcon
                v-if="updateFilter"
                icon="mdi-check"
                color="primary"
                size="small" /></template></VListItem></VList
      ></VCard>
    </VMenu>
    <div
      v-if="tab === 'installed' && currentFolder"
      class="plugin-folder-breadcrumb"
    >
      <VBtn
        variant="text"
        prepend-icon="mdi-arrow-left"
        :disabled="organizing || dragging"
        @click="currentFolder = ''"
        >返回</VBtn
      ><span>{{ currentFolder }}</span>
    </div>
    <VAlert
      v-if="sortMode && tab === 'installed'"
      color="warning"
      variant="tonal"
      class="plugin-sort-notice"
      ><span>拖动卡片调整顺序，可将插件拖入文件夹</span
      ><VBtn
        variant="tonal"
        color="error"
        :disabled="organizing || dragging"
        @click="sortMode = false"
        >退出</VBtn
      ></VAlert
    >
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <VAlert
      v-if="tab === 'market' && marketFailures.length"
      type="warning"
      variant="tonal"
      class="mb-4"
      role="alert"
    >
      {{ marketFailures.length }} 个仓库读取失败，当前列表可能不完整。
      <div v-for="source in marketFailures" :key="source.repo_url">
        {{ source.repo_url }}：{{ source.message }}
      </div>
      <VBtn variant="text" :disabled="busy || organizing" @click="load(true)">
        重新读取仓库
      </VBtn>
    </VAlert>
    <p v-if="loading" role="status">正在加载插件…</p>
    <template v-else-if="tab === 'installed'">
      <div v-if="!grid.length" class="empty">
        {{
          installedFiltering
            ? "没有匹配的插件"
            : currentFolder
              ? "文件夹中没有插件"
              : "尚未安装插件"
        }}
      </div>
      <Draggable
        v-else
        v-model="grid"
        :item-key="(item: PluginGridItem) => item.type + ':' + item.id"
        tag="div"
        class="plugin-grid"
        :disabled="!sortMode || organizing"
        :animation="150"
        group="plugins"
        @start="startSort"
        @end="endSort"
      >
        <template #item="{ element }"
          ><div
            :data-plugin-id="element.id"
            :data-folder-id="element.type === 'folder' ? element.id : undefined"
            class="plugin-grid-item"
            @dragover="
              sortMode && element.type === 'folder' && $event.preventDefault()
            "
            @drop="element.type === 'folder' && dropToFolder(element.id)"
          >
            <PluginFolderCard
              v-if="element.type === 'folder'"
              :name="element.id"
              :count="element.count"
              :config="element.config"
              :sortable="sortMode"
              @open="currentFolder = element.id"
              @rename="editFolder('rename', element.id)"
              @appearance="editFolder('appearance', element.id)"
              @delete="deleteFolder = element.id"
            />
            <PluginManageCard
              v-else
              :plugin="element.plugin"
              :count="marketStatistics[element.plugin.plugin_id]"
              :busy="busy || organizing"
              :sortable="sortMode"
              :in-folder="!!currentFolder"
              @action="cardAction"
            /></div
        ></template>
      </Draggable>
    </template>
    <div v-else-if="!visible.length" class="empty">没有匹配的可安装插件</div>
    <VInfiniteScroll v-else :key="marketListVersion" :items="visible" @load="loadMoreMarket">
    <div class="plugin-grid">
      <PluginManageCard
        v-for="plugin in visible"
        :key="plugin.instance_id"
        :plugin="plugin"
        :count="marketStatistics[plugin.plugin_id]"
        :busy="busy"
        @action="cardAction"
      />
    </div>
    <template #empty><span /></template>
    </VInfiniteScroll>
    <div
      v-if="tab === 'installed' && !currentFolder && !sortMode"
      class="plugin-folder-fab"
    >
      <VBtn
        icon
        color="success"
        variant="tonal"
        aria-label="新建文件夹"
        :disabled="organizing || busy || loading"
        @click="editFolder('create')"
        ><VIcon icon="mdi-folder-plus"
      /></VBtn>
    </div>
    <PluginMarketSettingDialog
      v-if="marketSettings"
      @close="marketSettings = false"
      @saved="marketSettingsSaved"
    />
    <PluginSearchDialog v-if="searchOpen" :plugins="searchPlugins" :loading="searchLoading" :error="searchError" @close="searchOpen = false" @open="searchSelected" />
    <PluginVersionHistoryDialog
      v-if="historyPlugin"
      :key="historyPlugin.instance_id"
      :plugin="historyPlugin"
      :show-update-action="historyShowUpdate"
      :updating="busy"
      :update-error="error"
      @close="historyPlugin = undefined"
      @update="updatePlugin(historyPlugin)"
    />
    <PluginFolderDialog
      v-if="folderEditor"
      :key="folderEditor.mode + folderEditor.name"
      v-bind="folderEditor"
      :busy="organizing"
      :error="folderError"
      @close="folderEditor = undefined"
      @save="saveFolder"
    />
    <VDialog
      :model-value="!!deleteFolder"
      max-width="460"
      content-class="plugin-dialog-surface"
      :persistent="organizing"
      @update:model-value="!organizing && (deleteFolder = '')"
      ><VCard
        ><VCardTitle class="plugin-dialog-title">确认删除文件夹</VCardTitle
        ><VCardText class="plugin-dialog-content"
          >确定删除文件夹“{{
            deleteFolder
          }}”？其中的插件将返回主列表。</VCardText
        >
        <div class="plugin-confirm-actions">
          <VBtn variant="text" :disabled="organizing" @click="deleteFolder = ''"
            >取消</VBtn
          ><VBtn
            color="error"
            variant="flat"
            :loading="organizing"
            @click="confirmDeleteFolder"
            >确认</VBtn
          >
        </div></VCard
      ></VDialog
    >
    <PluginLogDialog
      v-if="logPlugin"
      :key="logPlugin.instance_id"
      :plugin="logPlugin"
      @close="logPlugin = undefined"
    />
    <PluginCloneDialog
      v-if="cloneSource"
      :key="cloneSource.instance_id"
      :plugin="cloneSource"
      @close="cloneSource = undefined"
      @created="cloneCreated"
    />
    <PluginDataDialog
      v-if="detail"
      :key="detail.instance_id"
      :plugin="detail"
      @changed="load"
      @close="detail = undefined"
      @switch="
        if (detail && !unavailable(detail)) selected = detail;
        detail = undefined;
      "
    />
    <PluginConfigDialog
      v-if="selected"
      :key="selected.instance_id"
      :plugin="selected"
      @changed="load"
      @close="selected = undefined"
      @switch="
        detail = selected;
        selected = undefined;
      "
      @saved="
        selected = undefined;
        load();
      "
    />
    <VDialog
      :model-value="!!confirmation"
      max-width="460"
      content-class="plugin-dialog-surface"
      :persistent="busy"
      @update:model-value="!busy && (confirmation = undefined)"
      ><VCard v-if="confirmation"
        ><VCardTitle class="plugin-dialog-title"
          >确认{{
            confirmation.action === "reset"
                ? "重置"
                : "卸载"
          }}</VCardTitle
        ><VCardText class="plugin-dialog-content"
          >{{
            confirmation.action === "reset"
                ? "重置将清除该插件的配置及运行数据，是否继续？"
                : "卸载此插件？普通插件的配置及数据通常保留，已加载分身的配置及数据会清理；实际结果以服务端返回为准。"
          }}
          <p v-if="error" role="alert" class="error">{{ error }}</p></VCardText
        >
        <div class="plugin-confirm-actions">
          <VBtn
            variant="text"
            :disabled="busy"
            @click="confirmation = undefined"
            >取消</VBtn
          ><VBtn variant="flat" :loading="busy" @click="confirmAction"
            >确认</VBtn
          >
        </div></VCard
      ></VDialog
    >
  </VThemeProvider>
</template>
<style scoped>
.plugin-run-recovery {
  flex-basis: 100%;
  border-inline-start: 3px solid rgb(var(--v-theme-warning));
  padding: 10px 12px;
  background: rgba(var(--v-theme-warning), 0.06);
  border-radius: 4px;
}
.plugin-run-recovery p {
  margin: 4px 0 10px;
  line-height: 1.6;
}
.plugin-run-abandon-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}
.plugin-surface {
  background: transparent !important;
  min-height: 320px;
}
.plugin-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  gap: 16px;
}
.plugin-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.plugin-toolbar :deep(.v-tabs) {
  min-width: 0;
}
.plugin-toolbar-actions {
  flex-shrink: 0;
}
.plugin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 256px), 1fr));
  gap: 16px;
}
.plugin-grid-item {
  height: 100%;
  min-width: 0;
}
.plugin-folder-breadcrumb {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.plugin-sort-notice {
  margin-bottom: 16px;
}
.plugin-sort-notice :deep(.v-alert__content) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.plugin-folder-fab {
  position: fixed;
  inset-inline-end: 24px;
  inset-block-end: 24px;
  z-index: 10;
}
.sortable-chosen {
  opacity: 0.5;
}
.sortable-ghost {
  border: 2px dashed #2196f3;
  border-radius: 8px;
  background: #2196f31a;
  opacity: 0.3;
}
.plugin-confirm-actions {
  display: flex;
  justify-content: flex-end;
  padding: 16px 24px;
  gap: 8px;
}
.plugin-run-list {
  list-style: none;
  padding: 0;
  margin: 0;
}
.plugin-run-list li {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
}
@media (max-width: 640px) {
  .plugin-toolbar {
    gap: 4px;
  }
  .plugin-toolbar-actions {
    gap: 0;
  }
  .plugin-toolbar :deep(.v-tab) {
    min-width: 0;
    padding-inline: 10px;
  }
}
</style>
