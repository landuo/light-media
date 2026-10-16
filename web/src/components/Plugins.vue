<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VMenu } from 'vuetify/components/VMenu';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
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
const PluginFolderDialog = defineAsyncComponent(() => import('./PluginFolderDialog.vue'));

const PluginConfigDialog = defineAsyncComponent(() => import('./PluginConfigDialog.vue'));
const PluginDataDialog = defineAsyncComponent(() => import('./PluginDataDialog.vue'));
const PluginLogDialog = defineAsyncComponent(() => import('./PluginLogDialog.vue'));
const detail = ref<PluginCard>();
const logPlugin = ref<PluginCard>();
function openPlugin(plugin: PluginCard) {
  if (!plugin.installed) return;
  if (plugin.has_page) detail.value = plugin;
  else selected.value = plugin;
}

const tab = ref('installed'),
  query = ref(''),
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
const confirmation = ref<{ plugin: PluginCard; action: 'reset' | 'uninstall' }>();
const history = ref<any[] | null>(null),
  historyName = ref('');
const visible = computed(() =>
  (tab.value === 'installed' ? items.value : market.value).filter((item) =>
    (item.name + ' ' + item.description + ' ' + item.author)
      .toLowerCase()
      .includes(query.value.toLowerCase()),
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
  if (sortMode.value || organizing.value) return;
  if (action === 'open') openPlugin(plugin);
  else if (action === 'detail') detail.value = plugin;
  else if (action === 'settings') selected.value = plugin;
  else if (action === 'reset' || action === 'uninstall') confirmation.value = { plugin, action };
  else if (action === 'runs') await showRuns(plugin);
  else if (action === 'logs') logPlugin.value = plugin;
  else if (action === 'install') await install(plugin);
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
async function install(plugin: PluginCard) {
  if (busy.value) return;
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
    await api(
      '/plugins/' + encodeURIComponent(choice.plugin.instance_id) + '/' + choice.action,
      {},
    );
    toast(choice.action === 'reset' ? '插件配置及数据已重置' : '插件已卸载，配置及数据保留');
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
  } catch (e) {
    error.value = errorText(e);
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
        ><VTab value="installed">我的插件</VTab><VTab value="market">插件市场</VTab></VTabs
      >
      <div class="plugin-toolbar-actions">
        <input
          v-if="tab === 'market'"
          v-model="query"
          type="search"
          aria-label="搜索插件"
          placeholder="搜索插件"
        /><VBtn
          v-if="tab === 'installed'"
          variant="text"
          icon
          aria-label="筛选已安装插件"
          data-menu-activator="installed-filter-btn"
          :color="installedFiltering ? 'primary' : undefined"
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
          @click="sortMode = !sortMode"
          ><VIcon icon="mdi-sort"
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
    <PluginDataDialog
      v-if="detail"
      :key="detail.instance_id"
      :plugin="detail"
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
      @close="selected = undefined"
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
          >确认{{ confirmation.action === 'reset' ? '重置' : '卸载' }}</VCardTitle
        ><VCardText class="plugin-dialog-content">{{
          confirmation.action === 'reset'
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
            </li>
          </ul></VCardText
        ></VCard
      ></VDialog
    >
  </VThemeProvider>
</template>
<style scoped>
.plugin-surface {
  background: transparent !important;
  min-height: 320px;
}
.plugin-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 24px;
  gap: 16px;
}
.plugin-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.plugin-toolbar-actions input {
  max-width: 220px;
}
.plugin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
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
    align-items: flex-start;
    flex-direction: column;
  }
  .plugin-toolbar-actions {
    width: 100%;
  }
  .plugin-toolbar-actions input {
    max-width: none;
  }
}
</style>
