<script setup lang="ts">
import {
  ref,
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  onActivated,
  onDeactivated,
  watch,
} from 'vue';
import { GridStack, type GridStackWidget } from 'gridstack';
import 'gridstack/dist/gridstack.css';
import { useDisplay } from 'vuetify';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText } from '../api';
import {
  dashboardID,
  dashboardEnabled,
  dashboardResponse,
  dashboardProfile,
  dashboardColumns,
  dashboardLayout,
  dashboardWidget,
  dashboardRefresh,
  dashboardInt,
  type DashboardItem,
  type DashboardMeta,
  type DashboardLayout,
} from '../features/plugin-dashboard';
import PluginDashboardWidget from './PluginDashboardWidget.vue';
const display = useDisplay();
const container = ref<HTMLElement>(),
  items = ref<DashboardItem[]>([]),
  layout = ref<DashboardLayout>({ items: {} }),
  profile = ref(dashboardProfile(window.innerWidth));
const requesting = ref(true),
  editing = ref(false),
  settings = ref(false),
  loading = ref(true),
  error = ref(''),
  metadata = ref<DashboardMeta[]>([]),
  order = ref<DashboardMeta[]>([]),
  settingItems = ref<DashboardItem[]>([]),
  settingEnabled = ref<Record<string, boolean>>({});
const visible = computed(() =>
  items.value.filter((item) => item.cols && layout.value.enabled?.[dashboardID(item)]),
);
const timers = new Map<string, ReturnType<typeof setTimeout>>(),
  inflight = new Set<string>();
const errors = ref<Record<string, string>>({});
const autoHeights = new Map<string, number>(),
  resizeStartHeights = new Map<string, number | undefined>(),
  observedHeights = new Map<string, number>(),
  pendingResize = new Set<HTMLElement>();
let resizeFrame: number | undefined, notificationFrame: number | undefined;
let active = true,
  grid: GridStack | undefined,
  resizeObserver: ResizeObserver | undefined,
  syncing = false,
  switching = 0,
  saveChain = Promise.resolve();
const localKey = (key: string) => 'LIGHT_MEDIA_PLUGIN_DASHBOARD_' + key;
function local(key: string) {
  try {
    return JSON.parse(localStorage.getItem(localKey(key)) || 'null');
  } catch {
    return null;
  }
}
async function getSetting(key: string) {
  return (await api('/plugins/dashboard/settings/' + key))?.value;
}
function rememberSetting(key: string, value: unknown) {
  try {
    localStorage.setItem(localKey(key), JSON.stringify(value));
  } catch {
    /* Disabled storage does not prevent the server request. */
  }
}
function saveSetting(key: string, value: unknown) {
  const saved = JSON.parse(JSON.stringify(value));
  saveChain = saveChain
    .catch(() => {})
    .then(async () => {
      const result = await api('/plugins/dashboard/settings/' + key, saved);
      if (result?.saved !== true) throw new Error(result?.message || '仪表板设置保存失败');
    });
  return saveChain;
}
async function loadShared<T>(key: string, normalize: (value: unknown) => T | undefined) {
  const previous = normalize(local(key));
  try {
    const remote = normalize(await getSetting(key));
    if (remote !== undefined) {
      rememberSetting(key, remote);
      return remote;
    }
    if (previous !== undefined) await saveSetting(key, previous);
  } catch (e) {
    if (active) error.value = errorText(e);
  }
  return previous;
}
async function loadLayout(key: string) {
  const result = (await loadShared(key, dashboardLayout)) ?? { items: {} };
  // Display choices are shared across profiles; geometry is device specific.
  return { ...result, enabled: layout.value.enabled };
}
function saveLayout() {
  const { enabled: _display, ...geometry } = layout.value;
  rememberSetting(profile.value, geometry);
  return saveSetting(profile.value, geometry).catch((e) => {
    if (active) error.value = errorText(e);
  });
}
function sortItems() {
  const index = (item: DashboardMeta) => {
    const i = order.value.findIndex((x) => x.id === item.id && x.key === item.key);
    return i < 0 ? 999 : i;
  };
  items.value.sort((a, b) => index(a) - index(b));
}
function discoverMetadata(next: DashboardMeta[]) {
  metadata.value = next;
  const previous = new Map(items.value.map((item) => [dashboardID(item), item]));
  const ids = new Set(next.map(dashboardID));
  for (const id of timers.keys()) if (!ids.has(id)) clearTimer(id);
  // Metadata is sufficient for discovery and settings. Hidden widgets do not
  // need expensive data requests just to display their name in the picker.
  items.value = next.map((meta) => ({ ...previous.get(dashboardID(meta)), ...meta }));
  sortItems();
}
async function loadVisibleItems() {
  await Promise.all(
    metadata.value.filter((meta) => layout.value.enabled?.[dashboardID(meta)]).map(loadItem),
  );
  await syncGrid();
}
async function loadMetadata() {
  try {
    const next = await api<DashboardMeta[]>('/plugins/dashboard/meta');
    if (!active) return;
    discoverMetadata(next ?? []);
    await loadVisibleItems();
  } catch (e) {
    if (active) error.value = errorText(e);
  }
}
function clearTimer(id: string) {
  clearTimeout(timers.get(id));
  timers.delete(id);
}
function schedule(item: DashboardItem) {
  const id = dashboardID(item);
  clearTimer(id);
  const delay = dashboardRefresh(item, !!layout.value.enabled?.[id], active && requesting.value);
  if (delay !== undefined)
    timers.set(
      id,
      setTimeout(() => loadItem(item), delay),
    );
}
async function loadItem(meta: DashboardMeta) {
  const id = dashboardID(meta);
  if (!active || !requesting.value || !layout.value.enabled?.[id] || inflight.has(id)) return;
  inflight.add(id);
  clearTimer(id);
  try {
    const result = await api(
      '/plugins/' +
        encodeURIComponent(meta.id) +
        '/dashboard?key=' +
        encodeURIComponent(meta.key || ''),
    );
    if (!active || !metadata.value.some((entry) => dashboardID(entry) === id)) return;
    const item = dashboardResponse(meta, result);
    if (item === null) return;
    const index = items.value.findIndex((x) => dashboardID(x) === id);
    if (index < 0) {
      items.value.push(item);
      sortItems();
    } else items.value[index] = item;
    delete errors.value[id];
    schedule(item);
  } catch (e) {
    if (active) errors.value[id] = errorText(e);
  } finally {
    inflight.delete(id);
  }
}
function capture(manualHeight?: string) {
  if (!grid || syncing) return;
  const columns = grid.getColumn();
  const nodes = grid.save(false, false, undefined, columns) as GridStackWidget[];
  const next = { ...layout.value.items };
  for (const node of nodes) {
    if (!node.id) continue;
    const previous = next[node.id],
      w = dashboardInt(node.w, 1, columns, columns);
    next[node.id] = {
      x: dashboardInt(node.x, 0, columns - w, 0),
      y: dashboardInt(node.y, 0, 999, 0),
      w,
      ...(previous?.h !== undefined || manualHeight === node.id
        ? { h: dashboardInt(node.h, 1, 96, 4) }
        : {}),
    };
  }
  layout.value = { ...layout.value, items: next };
}
async function syncGrid() {
  if (!active || !grid || syncing) return;
  syncing = true;
  await nextTick();
  try {
    const visibleIDs = new Set(visible.value.map(dashboardID));
    grid.batchUpdate();
    for (const node of [...grid.engine.nodes])
      if (node.el && !visibleIDs.has(String(node.id ?? node.el.getAttribute('gs-id') ?? '')))
        grid.removeWidget(node.el, false, false);
    const elements = container.value?.querySelectorAll<HTMLElement>('.dashboard-grid-item') || [];
    for (const element of elements) {
      const item = items.value.find((x) => dashboardID(x) === element.getAttribute('gs-id'));
      if (!item) continue;
      const id = dashboardID(item),
        node = (element as HTMLElement & { gridstackNode?: GridStackWidget }).gridstackNode;
      const widget: GridStackWidget = dashboardWidget(
        item,
        profile.value,
        layout.value,
        autoHeights.get(profile.value + ':' + id),
      );
      if (node && !layout.value.items[id]) {
        delete widget.autoPosition;
        delete widget.x;
        delete widget.y;
      }
      if (node && layout.value.items[id]?.h === undefined) widget.h = node.h;
      if (node) grid.update(element, widget);
      else grid.makeWidget(element, widget);
    }
    grid.batchUpdate(false);
    grid.setStatic(!editing.value);
    observeSizes();
    await nextTick();
    for (const element of elements)
      if (
        !editing.value &&
        layout.value.items[element.getAttribute('gs-id') || '']?.h === undefined
      )
        measureHeight(element);
  } finally {
    syncing = false;
  }
}
function rememberHeight(element: HTMLElement) {
  const id = element.getAttribute('gs-id') || '',
    h = Number(element.getAttribute('gs-h'));
  if (id && layout.value.items[id]?.h === undefined && h > 0)
    autoHeights.set(profile.value + ':' + id, h);
}
function measureHeight(element: HTMLElement) {
  grid?.resizeToContent(element);
  rememberHeight(element);
}
function observeSizes() {
  resizeObserver?.disconnect();
  observedHeights.clear();
  container.value
    ?.querySelectorAll('[data-layout-size-source]')
    .forEach((el) => resizeObserver?.observe(el));
}
function resizedContent(entries: ResizeObserverEntry[]) {
  if (editing.value || syncing) return;
  for (const entry of entries) {
    const element = entry.target.closest<HTMLElement>('.dashboard-grid-item');
    if (!element) continue;
    const id = element.getAttribute('gs-id') || '',
      before = observedHeights.get(id),
      height = entry.contentRect.height;
    observedHeights.set(id, height);
    if (
      layout.value.items[id]?.h === undefined &&
      (before === undefined || Math.abs(before - height) >= 4)
    )
      pendingResize.add(element);
  }
  if (!pendingResize.size || resizeFrame !== undefined) return;
  resizeFrame = window.requestAnimationFrame(() => {
    resizeFrame = undefined;
    for (const element of pendingResize) measureHeight(element);
    pendingResize.clear();
  });
}
async function compactAndSave(manualHeight?: string) {
  if (!grid || syncing) return;
  grid.compact('compact');
  await nextTick();
  capture(manualHeight);
  void saveLayout();
  await nextTick();
  for (const element of container.value?.querySelectorAll<HTMLElement>('.dashboard-grid-item') ||
    [])
    if (!editing.value && layout.value.items[element.getAttribute('gs-id') || '']?.h === undefined)
      measureHeight(element);
}
function dragStopped() {
  void compactAndSave();
}
function notifyContentResize() {
  if (notificationFrame !== undefined) return;
  notificationFrame = window.requestAnimationFrame(() => {
    notificationFrame = undefined;
    window.dispatchEvent(new Event('resize'));
  });
}
function resizeStarted(_event: Event, element: HTMLElement & { gridstackNode?: GridStackWidget }) {
  resizeStartHeights.set(element.getAttribute('gs-id') || '', element.gridstackNode?.h);
  notifyContentResize();
}
function resizeStopped(_event: Event, element: HTMLElement & { gridstackNode?: GridStackWidget }) {
  const id = element.getAttribute('gs-id') || '',
    before = resizeStartHeights.get(id),
    after = element.gridstackNode?.h;
  resizeStartHeights.delete(id);
  notifyContentResize();
  void compactAndSave(
    before !== undefined && after !== undefined && before !== after ? id : undefined,
  );
}
async function toggleEdit() {
  error.value = '';
  if (editing.value) {
    void compactAndSave();
    editing.value = false;
  } else editing.value = true;
  grid?.setStatic(!editing.value);
  await syncGrid();
}
async function resetLayout() {
  error.value = '';
  layout.value = { ...layout.value, items: {} };
  void saveLayout();
  grid?.removeAll(false, false);
  await syncGrid();
  editing.value = false;
  grid?.setStatic(true);
}
function openSettings() {
  settingItems.value = [...items.value];
  settingEnabled.value = { ...layout.value.enabled };
  settings.value = true;
}
async function saveSettings() {
  error.value = '';
  const ordered = settingItems.value.map((x) => ({ id: x.id, key: x.key }));
  items.value = [...settingItems.value];
  order.value = ordered as DashboardMeta[];
  const enabled = { ...settingEnabled.value };
  layout.value = { ...layout.value, enabled };
  for (const item of items.value) schedule(item);
  // Start newly enabled widgets immediately, independently of persistence.
  void loadVisibleItems();
  // Source writes both local snapshots before attempting sequential requests.
  rememberSetting('enabled', enabled);
  rememberSetting('order', ordered);
  try {
    await saveSetting('enabled', enabled);
    await saveSetting('order', ordered);
  } catch (e) {
    if (active) error.value = errorText(e);
  }
  // Source closes and rediscovers metadata even after persistence fails.
  void loadMetadata();
  settings.value = false;
}
async function switchProfile() {
  const next = dashboardProfile(window.innerWidth);
  if (next === profile.value) return;
  const revision = ++switching;
  if (grid) {
    capture();
    void saveLayout();
  }
  profile.value = next;
  layout.value = {
    ...(dashboardLayout(local(next)) ?? { items: {} }),
    enabled: layout.value.enabled,
  };
  grid?.column(dashboardColumns(next), next === 'mobile' ? 'list' : 'moveScale');
  await syncGrid();
  try {
    const remote = await loadLayout(next);
    if (active && revision === switching && remote) {
      layout.value = remote;
      await syncGrid();
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  }
  for (const item of items.value) schedule(item);
}
onMounted(async () => {
  resizeObserver = new ResizeObserver(resizedContent);
  grid = GridStack.init(
    {
      animate: true,
      cellHeight: 16,
      column: dashboardColumns(profile.value),
      draggable: {
        handle: '.dashboard-grid-drag-handle',
        cancel: 'input,textarea,button,select,option,a',
      },
      float: false,
      margin: 8,
      resizable: { handles: 'e,s,se' },
      staticGrid: true,
    },
    container.value,
  );
  grid.on('dragstop', dragStopped);
  grid.on('resizestart', resizeStarted);
  grid.on('resize', notifyContentResize);
  grid.on('resizestop', resizeStopped);
  window.addEventListener('resize', switchProfile);
  layout.value = dashboardLayout(local(profile.value)) ?? { items: {} };
  try {
    const initialProfile = profile.value;
    const initialSwitch = switching;
    const [enabled, storedOrder, geometry, nextMetadata] = await Promise.all([
      loadShared('enabled', dashboardEnabled),
      loadShared('order', (value) =>
        Array.isArray(value)
          ? value
              .filter((x) => x && typeof x.id === 'string' && x.id)
              .map((x) => ({ id: x.id, key: typeof x.key === 'string' ? x.key : '' }))
          : undefined,
      ),
      loadShared(initialProfile, dashboardLayout),
      api<DashboardMeta[]>('/plugins/dashboard/meta'),
    ]);
    if (!active) return;
    order.value = (storedOrder ?? []) as DashboardMeta[];
    layout.value = {
      ...(initialSwitch === switching ? (geometry ?? layout.value) : layout.value),
      enabled: enabled ?? layout.value.enabled,
    };
    discoverMetadata(nextMetadata ?? []);
    await loadVisibleItems();
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
});
onActivated(() => {
  active = true;
  requesting.value = true;
  for (const meta of metadata.value)
    if (layout.value.enabled?.[dashboardID(meta)]) void loadItem(meta);
  void nextTick(syncGrid);
});
onDeactivated(() => {
  requesting.value = false;
  for (const id of timers.keys()) clearTimer(id);
});
watch(() => visible.value.map((x) => dashboardID(x) + JSON.stringify(x.cols)).join('|'), syncGrid);
onUnmounted(() => {
  active = false;
  switching++;
  window.removeEventListener('resize', switchProfile);
  for (const id of timers.keys()) clearTimer(id);
  resizeObserver?.disconnect();
  if (resizeFrame !== undefined) window.cancelAnimationFrame(resizeFrame);
  if (notificationFrame !== undefined) window.cancelAnimationFrame(notificationFrame);
  pendingResize.clear();
  grid?.destroy(false);
  grid = undefined;
});
</script>
<template>
  <VThemeProvider theme="light" with-background>
    <section class="plugin-surface plugin-dashboard-page">
      <VAlert v-if="error" type="error" class="mb-4">{{ error }}</VAlert>
      <p v-if="loading" role="status">正在加载…</p>
      <VAlert v-else-if="!items.length" type="info" variant="tonal"
        >没有已启用的插件仪表板组件。</VAlert
      >
      <VAlert v-else-if="!visible.length" type="info" variant="tonal"
        >请在仪表板设置中选择显示内容。</VAlert
      >
      <VAlert
        v-for="meta in metadata.filter(
          (entry) =>
            errors[dashboardID(entry)] &&
            layout.enabled?.[dashboardID(entry)] &&
            !items.find((item) => dashboardID(item) === dashboardID(entry))?.cols,
        )"
        :key="dashboardID(meta)"
        type="error"
      >
        {{ meta.name }}：{{ errors[dashboardID(meta)] }}
        <VBtn variant="text" @click="loadItem(meta)">重试</VBtn>
      </VAlert>
      <div
        ref="container"
        class="grid-stack plugin-dashboard-grid"
        :class="{ 'is-editing': editing }"
      >
        <div
          v-for="item in visible"
          :key="dashboardID(item)"
          class="grid-stack-item dashboard-grid-item"
          :class="{ 'is-manual-height': layout.items[dashboardID(item)]?.h !== undefined }"
          :gs-id="dashboardID(item)"
        >
          <div class="grid-stack-item-content">
            <div class="dashboard-grid-auto-size" data-layout-size-source>
              <div class="dashboard-grid-content-measure">
                <VAlert v-if="errors[dashboardID(item)]" type="error"
                  >{{ errors[dashboardID(item)]
                  }}<VBtn variant="text" @click="loadItem(item)">重试</VBtn></VAlert
                >
                <PluginDashboardWidget
                  :item="item"
                  :allow-refresh="requesting"
                  @loaded="syncGrid"
                />
              </div>
              <span
                v-if="editing"
                class="dashboard-grid-drag-handle"
                role="button"
                tabindex="0"
                aria-label="拖动组件"
                ><VIcon icon="mdi-drag" size="small"
              /></span>
            </div>
          </div>
        </div>
      </div>
      <div class="dashboard-actions">
        <VBtn
          icon="mdi-tune"
          color="info"
          variant="tonal"
          aria-label="仪表板设置"
          @click="openSettings"
        />
        <VBtn
          v-if="editing"
          icon="mdi-restore"
          color="warning"
          variant="tonal"
          aria-label="恢复布局"
          @click="resetLayout"
        />
        <VBtn
          :icon="editing ? 'mdi-check' : 'mdi-view-dashboard-edit'"
          :aria-label="editing ? '完成布局编辑' : '编辑布局'"
          @click="toggleEdit"
        />
      </div>
      <VDialog
        v-model="settings"
        width="35rem"
        :fullscreen="!display.mdAndUp.value"
        scrollable
        content-class="plugin-dialog-surface"
      >
        <VCard class="dashboard-settings-card">
          <VCardTitle
            ><VIcon icon="mdi-tune" size="small" class="me-2" />仪表板设置<PluginDialogCloseBtn
              :model-value="true"
              @update:model-value="settings = false"
          /></VCardTitle>
          <VCardText
            ><p class="mb-4">选择仪表板显示内容</p>
            <div class="dashboard-settings-grid">
              <button
                v-for="item in settingItems"
                :key="dashboardID(item)"
                type="button"
                class="dashboard-setting-item"
                :class="{ enabled: settingEnabled[dashboardID(item)] }"
                :aria-pressed="Boolean(settingEnabled[dashboardID(item)])"
                @click="settingEnabled[dashboardID(item)] = !settingEnabled[dashboardID(item)]"
              >
                <VIcon
                  :icon="
                    settingEnabled[dashboardID(item)] ? 'mdi-check-circle' : 'mdi-circle-outline'
                  "
                  :color="settingEnabled[dashboardID(item)] ? 'primary' : undefined"
                  size="small"
                />
                <span>{{ item.attrs?.title ?? item.name }}</span>
              </button>
            </div> </VCardText
          ><VCardActions
            ><VBtn variant="flat" prepend-icon="mdi-content-save" @click="saveSettings"
              >保存</VBtn
            ></VCardActions
          >
        </VCard>
      </VDialog>
    </section>
  </VThemeProvider>
</template>
<style scoped>
.plugin-dashboard-page {
  min-height: calc(100vh - 150px);
  padding-bottom: 80px;
  background: transparent;
}
.plugin-dashboard-grid {
  margin: -8px;
}
.grid-stack-item-content {
  overflow: visible !important;
  position: relative;
}
.dashboard-grid-auto-size {
  position: relative;
  width: 100%;
}
.is-manual-height .dashboard-grid-auto-size,
.is-manual-height .dashboard-grid-content-measure,
.is-editing .dashboard-grid-auto-size,
.is-editing .dashboard-grid-content-measure {
  height: 100%;
}
.grid-stack-item-content :deep(.v-card) {
  height: auto;
}
.is-manual-height .dashboard-grid-auto-size {
  position: relative;
  width: 100%;
}
.is-manual-height .dashboard-grid-auto-size,
.is-manual-height .dashboard-grid-content-measure,
.is-editing .dashboard-grid-auto-size,
.is-editing .dashboard-grid-content-measure {
  height: 100%;
}
.grid-stack-item-content :deep(.v-card) {
  height: 100%;
}
.grid-stack-item-content :deep(.v-card-text) {
  overflow: auto;
}
.dashboard-actions {
  position: fixed;
  right: 24px;
  bottom: max(32px, var(--app-floating-bottom, 0px));
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 10;
}
.dashboard-grid-drag-handle {
  position: absolute;
  right: 12px;
  top: 12px;
  cursor: move;
  color: rgb(var(--v-theme-primary));
  z-index: 2;
}
.is-editing .grid-stack-item-content {
  outline: 1px dashed rgb(var(--v-theme-primary));
  border-radius: 8px;
}
:deep(.v-card-actions) {
  justify-content: flex-end;
}
.dashboard-settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.dashboard-setting-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  border-radius: 8px;
  text-align: left;
  color: rgba(var(--v-theme-on-surface), 0.87);
  background: transparent;
  font: inherit;
  cursor: pointer;
}
.dashboard-setting-item.enabled {
  border-color: rgb(var(--v-theme-primary));
  background: rgba(var(--v-theme-primary), 0.08);
}
.dashboard-settings-card .v-card-title {
  position: relative;
  padding: 20px 24px;
}
.dashboard-settings-card .v-card-text {
  padding: 24px;
}
</style>
