<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VMenu } from 'vuetify/components/VMenu';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
import { VTextField } from 'vuetify/components/VTextField';
import { VCombobox } from 'vuetify/components/VCombobox';
import { VDivider } from 'vuetify/components/VDivider';
import { VList, VListItem, VListItemTitle, VListSubheader } from 'vuetify/components/VList';
import Draggable from 'vuedraggable';
import { VDialog } from 'vuetify/components/VDialog';
import { api, errorText, toast } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import AppIcon from './AppIcon.vue';
import PluginManageCard from './PluginManageCard.vue';
import PluginFolderCard from './PluginFolderCard.vue';
import {
  organizationGrid,
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
} from '../features/plugin-organization';
const emit = defineEmits<{ changed: [] }>();
const PluginFolderDialog = defineAsyncComponent(() => import('./PluginFolderDialog.vue'));

const PluginConfigDialog = defineAsyncComponent(() => import('./PluginConfigDialog.vue'));
const PluginDataDialog = defineAsyncComponent(() => import('./PluginDataDialog.vue'));
const PluginLogDialog = defineAsyncComponent(() => import('./PluginLogDialog.vue'));
const PluginCloneDialog = defineAsyncComponent(() => import('./PluginCloneDialog.vue'));
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
  toast('插件分身已创建，默认暂停');
  await load();
}
function openPlugin(plugin: PluginCard) {
  if (!plugin.installed) return;
  if (plugin.has_page) detail.value = plugin;
  else selected.value = plugin;
}

const tab = ref('installed'),
  query = ref(''),
  marketFilterMenu = ref(false),
  items = ref<PluginCard[]>([]),
  market = ref<PluginCard[]>([]);
const installedQuery = ref<string | null>(null),
  runningFilter = ref(false),
  updateFilter = ref(false),
  filterMenu = ref(false);
const installedFiltering = computed(
  () => !!installedQuery.value || runningFilter.value || updateFilter.value,
);
function toggleInstalledFilter(filter: 'running' | 'updates') {
  if (filter === 'running') runningFilter.value = !runningFilter.value;
  else updateFilter.value = !updateFilter.value;
  filterMenu.value = false;
}
function submitInstalledFilter(event: KeyboardEvent) {
  if (!event.isComposing) filterMenu.value = false;
}
const loading = ref(true),
  error = ref(''),
  busy = ref(false),
  selected = ref<PluginCard>();
const confirmation = ref<{ plugin: PluginCard; action: 'reset' | 'uninstall' | 'update' }>();
const history = ref<any[] | null>(null),
  historyName = ref(''),
  historyInstance = ref(''),
  retryingRun = ref('');
const visible = computed(() =>
  (tab.value === 'installed' ? items.value : market.value).filter((item) =>
    (item.name + ' ' + item.description + ' ' + item.author)
      .toLowerCase()
      .includes((query.value || '').toLowerCase()),
  ),
);
const organization = ref<PluginOrganization>({ revision: 0, folders: {}, order: [] });
const currentFolder = ref(''),
  sortMode = ref(false),
  dragging = ref(false),
  organizing = ref(false);
const grid = ref<PluginGridItem[]>([]);
const folderEditor = ref<{
  mode: 'create' | 'rename' | 'appearance';
  name: string;
  config?: FolderConfig;
}>();
const folderError = ref(''),
  deleteFolder = ref('');
let dragged: PluginGridItem | undefined,
  droppedIntoFolder = false,
  loadSerial = 0;
let timer: ReturnType<typeof setInterval>;
function rebuildGrid() {
  if (!dragging.value)
    grid.value = organizationGrid(
      organization.value,
      items.value,
      currentFolder.value,
      installedQuery.value || '',
      { running: runningFilter.value, updates: updateFilter.value },
    );
}
watch(
  [organization, items, currentFolder, installedQuery, runningFilter, updateFilter],
  rebuildGrid,
  {
    deep: true,
  },
);
watch(tab, () => {
  sortMode.value = false;
  currentFolder.value = '';
  filterMenu.value = false;
  marketFilterMenu.value = false;
  load();
});
async function load() {
  if (organizing.value || sortMode.value || dragging.value) return;
  const serial = ++loadSerial;
  try {
    const [installed, groups] = await Promise.all([api('/plugins'), api('/plugins/organization')]);
    if (serial !== loadSerial || organizing.value || sortMode.value || dragging.value) return;
    items.value = installed;
    if (groups.revision >= organization.value.revision) organization.value = groups;
    if (currentFolder.value && !Object.hasOwn(groups.folders, currentFolder.value))
      currentFolder.value = '';
    if (tab.value === 'market') market.value = await api('/plugins/market');
    error.value = '';
    emit('changed');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function persistOrganization(next: PluginOrganization): Promise<boolean> {
  if (organizing.value) return false;
  organizing.value = true;
  folderError.value = '';
  error.value = '';
  ++loadSerial;
  try {
    organization.value = await api('/plugins/organization', next, 'PUT');
    return true;
  } catch (e) {
    error.value = folderError.value = errorText(e);
    return false;
  } finally {
    organizing.value = false;
    rebuildGrid();
  }
}
function editFolder(mode: 'create' | 'rename' | 'appearance', name = '') {
  folderError.value = '';
  folderEditor.value = { mode, name, config: folderAppearance(organization.value.folders[name]) };
}
async function saveFolder(value: { name: string; config: FolderConfig }) {
  const editor = folderEditor.value;
  if (!editor || organizing.value) return;
  const next = copyOrganization(organization.value);
  if (editor.mode !== 'appearance') {
    if (!value.name.trim()) {
      folderError.value = '文件夹名称不能为空';
      return;
    }
    if (value.name !== editor.name && Object.hasOwn(next.folders, value.name)) {
      folderError.value = '文件夹已存在';
      return;
    }
    if (editor.mode === 'rename' && value.name === editor.name) {
      folderEditor.value = undefined;
      return;
    }
    const folder =
      editor.mode === 'create'
        ? {
            plugins: [],
            order: Object.keys(next.folders).length,
            icon: folderIcons[0],
            color: folderColors[0],
            gradient: folderGradients[0],
            background: '',
            showIcon: true,
          }
        : next.folders[editor.name];
    if (editor.mode === 'rename') delete next.folders[editor.name];
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
  if (await persistOrganization(next)) {
    if (currentFolder.value === editor.name && editor.mode === 'rename')
      currentFolder.value = value.name;
    folderEditor.value = undefined;
    toast(
      editor.mode === 'create'
        ? '文件夹创建成功'
        : editor.mode === 'rename'
          ? '文件夹重命名成功'
          : '文件夹设置已保存',
    );
  }
}
async function confirmDeleteFolder() {
  if (!deleteFolder.value || organizing.value) return;
  const name = deleteFolder.value,
    next = copyOrganization(organization.value);
  delete next.folders[name];

  if (await persistOrganization(next)) {
    if (currentFolder.value === name) currentFolder.value = '';
    deleteFolder.value = '';
    toast('文件夹已删除');
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
    const current = folderMembers(next.folders[currentFolder.value]);
    const sorted = grid.value.filter((item) => item.type === 'plugin').map((item) => item.id);
    next.folders[currentFolder.value] = replaceMembers(next.folders[currentFolder.value], [
      ...sorted,
      ...current.filter((id) => !sorted.includes(id)),
    ]);
    const order = normalizedPluginOrder(next);
    const folderOrder =
      order.find((item) => item.type === 'folder' && item.id === currentFolder.value)?.order ?? 999;
    sorted.forEach((id, index) => {
      const item = order.find((item) => item.type === 'plugin' && item.id === id);
      if (item) item.order = folderOrder + 0.1 + index * 0.01;
      else order.push({ id, type: 'plugin', order: folderOrder + 0.1 + index * 0.01 });
    });
    next.order = order;
  } else {
    const sorted = grid.value.map((item, index) => ({
      type: item.type,
      id: item.id,
      order: index,
    }));
    const keys = new Set(sorted.map((item) => item.type + ':' + item.id));
    next.order = [
      ...sorted,
      ...normalizedPluginOrder(next)
        .filter((item) => !keys.has(item.type + ':' + item.id))
        .map((item, index) => ({ ...item, order: sorted.length + index })),
    ];
    sorted.forEach((item) => {
      if (item.type === 'folder')
        next.folders[item.id] = {
          ...folderAppearance(next.folders[item.id]),
          plugins: [...folderMembers(next.folders[item.id])],
          order: item.order,
        };
    });
  }
  dragged = undefined;
  await persistOrganization(next);
}
async function dropToFolder(name: string) {
  if (!sortMode.value || dragged?.type !== 'plugin' || organizing.value) return;
  droppedIntoFolder = true;
  try {
    const next = moveToFolder(organization.value, dragged.id, name);
    if (await persistOrganization(next)) toast('插件已移动到文件夹');
  } catch (e) {
    error.value = errorText(e);
  }
}
async function cardAction(action: string, plugin: PluginCard) {
  if (sortMode.value || organizing.value || busy.value) return;
  if (action === 'open') openPlugin(plugin);
  else if (action === 'detail') detail.value = plugin;
  else if (action === 'settings') selected.value = plugin;
  else if (action === 'clone' && plugin.installed) cloneSource.value = plugin;
  else if (action === 'update' && plugin.update && plugin.has_update)
    confirmation.value = { plugin, action };
  else if (action === 'reset' || action === 'uninstall') confirmation.value = { plugin, action };
  else if (action === 'runs') await showRuns(plugin);
  else if (action === 'logs') logPlugin.value = plugin;
  else if (action === 'install') await install(plugin);
  else if (action === 'restore-reviewed' && plugin.restore_review) await restoreReviewed(plugin);
  else if (action === 'remove-folder' && currentFolder.value) {
    const next = copyOrganization(organization.value),
      name = currentFolder.value;
    next.folders[name] = replaceMembers(
      next.folders[name],
      folderMembers(next.folders[name]).filter((id) => id !== plugin.instance_id),
    );
    if (await persistOrganization(next)) toast('插件已移出文件夹');
  }
}
async function restoreReviewed(plugin: PluginCard) {
  if (
    busy.value ||
    plugin.installed ||
    !plugin.restore_review ||
    plugin.restore_review_blocked_reason
  )
    return;
  busy.value = true;
  try {
    await api(
      '/plugins/' + encodeURIComponent(plugin.instance_id) + '/restore-reviewed',
      plugin.restore_review,
    );
    toast(plugin.name + '已恢复为待确认，尚未执行；请打开设置核对并保存');
    installedQuery.value = null;
    runningFilter.value = false;
    updateFilter.value = false;
    tab.value = 'installed';
    await load();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function install(plugin: PluginCard) {
  if (
    busy.value ||
    plugin.update_review_pending ||
    plugin.restore_review ||
    plugin.restore_review_blocked_reason
  )
    return;
  busy.value = true;
  try {
    await api('/plugins/install', { plugin_id: plugin.plugin_id });
    toast(plugin.name + '已安装');
    installedQuery.value = null;
    runningFilter.value = false;
    updateFilter.value = false;
    tab.value = 'installed';
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
  busy.value = true;
  try {
    if (choice.action === 'update' && !choice.plugin.update) return;
    const result = await api(
      '/plugins/' + encodeURIComponent(choice.plugin.instance_id) + '/' + choice.action,
      choice.action === 'update' ? choice.plugin.update : {},
    );
    toast(
      choice.action === 'update'
        ? result.review_pending
          ? '版本已迁移，尚未执行；请打开当前实例设置核对并保存'
          : result.initialization
            ? '内置版本迁移已提交，初始化结果请查看执行记录'
            : '内置版本已确认，配置和数据保留'
        : choice.action === 'reset'
          ? '插件配置及数据已重置'
          : '插件已卸载，配置及数据保留',
    );
    confirmation.value = undefined;
    await load();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function showRuns(plugin: PluginCard) {
  try {
    history.value = await api('/plugins/' + encodeURIComponent(plugin.instance_id) + '/runs');
    historyName.value = plugin.name;
    historyInstance.value = plugin.instance_id;
  } catch (e) {
    error.value = errorText(e);
  }
}

async function retryRun(runId: string) {
  if (!historyInstance.value || retryingRun.value) return;
  retryingRun.value = runId;
  try {
    await api('/plugins/' + encodeURIComponent(historyInstance.value) + '/retry', {
      run_id: runId,
    });
    history.value = await api('/plugins/' + encodeURIComponent(historyInstance.value) + '/runs');
    toast('已重试该执行');
  } catch (e) {
    toast(errorText(e));
  } finally {
    retryingRun.value = '';
  }
}
const abandoningRun = ref('');
const abandonChoice = ref<any>();
async function abandonUnknownRun() {
  const run = abandonChoice.value;
  if (!run || !historyInstance.value || abandoningRun.value) return;
  abandoningRun.value = run.id;
  try {
    await api('/plugins/' + encodeURIComponent(historyInstance.value) + '/abandon-unknown', {
      run_id: run.id,
      confirm_abandon: true,
    });
    history.value = await api('/plugins/' + encodeURIComponent(historyInstance.value) + '/runs');
    abandonChoice.value = undefined;
    toast('已放弃旧执行并保留未知证据，旧动作不会重放');
  } catch (e) {
    toast(errorText(e));
  } finally {
    abandoningRun.value = '';
  }
}
const states: Record<string, string> = {
  pending: '等待执行',
  running: '执行中',
  succeeded: '已完成',
  failed: '执行失败',
  canceled: '已取消',
  reconcile_pending: '待核对',
};
const actions: Record<string, string> = {
  api: '接口调用',
  init: '应用配置',
  reload: '重新加载',
  service: '定时执行',
  event: '事件处理',
  once: '立即运行一次',
  run: '手动执行',
};
onMounted(() => {
  load();
  timer = setInterval(load, 5000);
});
onUnmounted(() => clearInterval(timer));
</script>
<template>
  <VThemeProvider theme="light" with-background class="plugin-surface">
    <div class="plugin-toolbar">
      <VTabs v-model="tab" color="primary"
        ><VTab value="installed" prepend-icon="mdi-apps">我的插件</VTab
        ><VTab value="market" prepend-icon="mdi-shopping">插件市场</VTab></VTabs
      >
      <div class="plugin-toolbar-actions">
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
                @keyup.enter="marketFilterMenu = false"
              /></div
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
          @click="load"
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
              ><VIcon icon="mdi-play-circle" color="success" size="small" /></template
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
              ><VIcon icon="mdi-arrow-up-circle" color="info" size="small" /></template
            ><VListItemTitle>有新版本</VListItemTitle
            ><template #append
              ><VIcon
                v-if="updateFilter"
                icon="mdi-check"
                color="primary"
                size="small" /></template></VListItem></VList
      ></VCard>
    </VMenu>
    <div v-if="tab === 'installed' && currentFolder" class="plugin-folder-breadcrumb">
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
    <p v-if="loading" role="status">正在加载插件…</p>
    <template v-else-if="tab === 'installed'">
      <div v-if="!grid.length" class="empty">
        {{
          installedFiltering
            ? '没有匹配的插件'
            : currentFolder
              ? '文件夹中没有插件'
              : '尚未安装插件'
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
            @dragover="sortMode && element.type === 'folder' && $event.preventDefault()"
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
              :busy="busy || organizing"
              :sortable="sortMode"
              :in-folder="!!currentFolder"
              @action="cardAction"
            /></div
        ></template>
      </Draggable>
    </template>
    <div v-else-if="!visible.length" class="empty">没有匹配的可安装插件</div>
    <div v-else class="plugin-grid">
      <PluginManageCard
        v-for="plugin in visible"
        :key="plugin.instance_id"
        :plugin="plugin"
        :busy="busy"
        @action="cardAction"
      />
    </div>
    <div v-if="tab === 'installed' && !currentFolder && !sortMode" class="plugin-folder-fab">
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
          >确定删除文件夹“{{ deleteFolder }}”？其中的插件将返回主列表。</VCardText
        >
        <div class="plugin-confirm-actions">
          <VBtn variant="text" :disabled="organizing" @click="deleteFolder = ''">取消</VBtn
          ><VBtn color="error" variant="flat" :loading="organizing" @click="confirmDeleteFolder"
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
        selected = detail;
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
            confirmation.action === 'update'
              ? '更新'
              : confirmation.action === 'reset'
                ? '重置'
                : '卸载'
          }}</VCardTitle
        ><VCardText class="plugin-dialog-content">{{
          confirmation.action === 'update'
            ? '更新到当前已编译版本，保留该实例配置、数据和分身信息。旧一次性执行意图不会自动重放，需要时请打开设置重新确认。迁移提交与初始化执行结果分别记录，是否继续？'
            : confirmation.action === 'reset'
              ? '重置将清除该插件的配置及运行数据，是否继续？'
              : '卸载此插件？配置及运行数据会保留。'
        }}</VCardText>
        <div class="plugin-confirm-actions">
          <VBtn variant="text" :disabled="busy" @click="confirmation = undefined">取消</VBtn
          ><VBtn variant="flat" :loading="busy" @click="confirmAction">确认</VBtn>
        </div></VCard
      ></VDialog
    >
    <VDialog
      :model-value="history !== null"
      max-width="720"
      scrollable
      content-class="plugin-dialog-surface"
      @update:model-value="history = null"
      ><VCard
        ><VCardTitle class="plugin-dialog-title"
          >{{ historyName }} - 执行记录<VBtn
            variant="text"
            icon
            aria-label="关闭执行记录"
            @click="history = null"
            ><AppIcon name="close" /></VBtn></VCardTitle
        ><VCardText class="plugin-dialog-content"
          ><p v-if="!history?.length">暂无执行记录</p>
          <ul v-else class="plugin-run-list">
            <li v-for="run in history" :key="run.id">
              <span>{{ actions[run.action] || '插件执行' }}</span
              ><span>{{ states[run.state] || run.state }}</span
              ><time>{{ new Date(run.created_at).toLocaleString() }}</time
              ><code v-if="run.error_code">{{ run.error_code }}</code>
              <div v-if="run.recovery" class="plugin-run-recovery" role="status">
                <strong>{{ run.recovery.stage }}</strong>
                <p>{{ run.recovery.message }}</p>
                <VBtn
                  v-if="run.recovery.abandon_allowed"
                  size="small"
                  color="warning"
                  variant="tonal"
                  :disabled="!!abandoningRun"
                  @click="abandonChoice = run"
                  >核对后放弃旧执行</VBtn
                >
              </div>
              <VBtn
                v-if="run.retryable"
                size="small"
                variant="text"
                :loading="retryingRun === run.id"
                :disabled="!!retryingRun"
                @click="retryRun(run.id)"
                >重试</VBtn
              >
            </li>
          </ul></VCardText
        ></VCard
      ></VDialog
    >
    <VDialog
      :model-value="!!abandonChoice"
      max-width="500"
      content-class="plugin-dialog-surface"
      @update:model-value="
        (value) => {
          if (!value && !abandoningRun) abandonChoice = undefined;
        }
      "
    >
      <VCard
        ><VCardTitle>确认放弃旧执行</VCardTitle
        ><VCardText>
          <p>
            请先核对备份目录及相关 WebDAV 文件。当前待核对阶段：{{
              abandonChoice?.recovery?.stage
            }}。
          </p>
          <p>
            此操作保留全部未知副作用证据，取消旧任务及其定时触发，不回滚文件，不重试旧动作。完成后可修改配置或发起新备份。
          </p>
          <div class="plugin-run-abandon-actions">
            <VBtn variant="text" :disabled="!!abandoningRun" @click="abandonChoice = undefined"
              >返回核对</VBtn
            >
            <VBtn color="warning" :loading="!!abandoningRun" @click="abandonUnknownRun"
              >已核对，确认放弃</VBtn
            >
          </div>
        </VCardText></VCard
      >
    </VDialog>
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
