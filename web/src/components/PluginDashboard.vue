<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue';
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
  dashboardPreferredLayout,
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
const editing = ref(false),
  settings = ref(false),
  loading = ref(true),
  error = ref(''),
  settingItems = ref<DashboardItem[]>([]),
  settingEnabled = ref<Record<string, boolean>>({});
const visible = computed(() =>
  items.value.filter((item) => item.cols && layout.value.enabled?.[dashboardID(item)]),
);
const timers = new Map<string, ReturnType<typeof setTimeout>>(),
  inflight = new Set<string>();
const errors = ref<Record<string, string>>({});
const autoHeights = new Map<string, number>();
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
function saveSetting(key: string, value: unknown) {
  localStorage.setItem(localKey(key), JSON.stringify(value));
  const saved = JSON.parse(JSON.stringify(value));
  saveChain = saveChain
    .catch(() => {})
    .then(async () => {
      await api('/plugins/dashboard/settings/' + key, saved);
    });
  return saveChain;
}
async function loadLayout(key: string) {
  const previous = dashboardLayout(local(key));
  const remote = dashboardLayout(await getSetting(key));
  const selected = dashboardPreferredLayout(previous, remote);
  const result = selected.layout ?? { items: {} };
  const inherited =
    result.enabled === undefined ? dashboardEnabled(await getSetting('enabled')) : undefined;
  if (inherited !== undefined) result.enabled = inherited;
  if (selected.layout !== undefined || inherited !== undefined)
    localStorage.setItem(localKey(key), JSON.stringify(result));
  if (selected.write || inherited !== undefined) await saveSetting(key, result);
  return result;
}
function saveLayout() {
  layout.value = { ...layout.value, updatedAt: Date.now() };
  return saveSetting(profile.value, layout.value);
}
function clearTimer(id: string) {
  clearTimeout(timers.get(id));
  timers.delete(id);
}
function schedule(item: DashboardItem) {
  const id = dashboardID(item);
  clearTimer(id);
  const delay = dashboardRefresh(item, !!layout.value.enabled?.[id], active);
  if (delay !== undefined)
    timers.set(
      id,
      setTimeout(() => loadItem(item), delay),
    );
}
async function loadItem(meta: DashboardMeta) {
  const id = dashboardID(meta);
  if (!active || inflight.has(id)) return;
  inflight.add(id);
  clearTimer(id);
  try {
    const result = await api(
      '/plugins/' + encodeURIComponent(meta.id) + '/dashboard?key=' + encodeURIComponent(meta.key),
    );
    if (!active) return;
    const item = dashboardResponse(meta, result);
    const index = items.value.findIndex((x) => dashboardID(x) === id);
    if (index < 0) items.value.push(item);
    else items.value[index] = item;
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
  const nodes = grid.save(false, false) as GridStackWidget[];
  const next = { ...layout.value.items };
  for (const node of nodes) {
    if (!node.id) continue;
    const previous = next[node.id],
      columns = dashboardColumns(profile.value),
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
    grid.removeAll(false, false);
    const elements = container.value?.querySelectorAll<HTMLElement>('.dashboard-grid-item') || [];
    for (const element of elements) {
      const item = items.value.find((x) => dashboardID(x) === element.getAttribute('gs-id'));
      if (item) {
        grid.makeWidget(
          element,
          dashboardWidget(
            item,
            profile.value,
            layout.value,
            autoHeights.get(profile.value + ':' + dashboardID(item)),
          ),
        );
        if (!editing.value && layout.value.items[dashboardID(item)]?.h === undefined)
          measureHeight(element);
      }
    }
    grid.setStatic(!editing.value);
    observeSizes();
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
  container.value
    ?.querySelectorAll('[data-layout-size-source]')
    .forEach((el) => resizeObserver?.observe(el));
}
async function toggleEdit() {
  if (editing.value) {
    capture();
    try {
      await saveLayout();
      editing.value = false;
    } catch (e) {
      error.value = errorText(e);
    }
  } else editing.value = true;
  grid?.setStatic(!editing.value);
  await syncGrid();
}
async function resetLayout() {
  layout.value = { ...layout.value, items: {} };
  try {
    await saveLayout();
    await syncGrid();
  } catch (e) {
    error.value = errorText(e);
  }
}
function openSettings() {
  settingItems.value = [...items.value];
  settingEnabled.value = { ...layout.value.enabled };
  settings.value = true;
}
async function saveSettings() {
  try {
    const ordered = settingItems.value.map((x) => ({ id: x.id, key: x.key }));
    items.value = [...settingItems.value];
    layout.value = { ...layout.value, enabled: { ...settingEnabled.value } };
    await saveLayout();
    await saveSetting('order', ordered);
    settings.value = false;
    // Source settings save re-fetches declarations once even when automatic
    // refresh is absent. A hidden widget receives no subsequent timer.
    await Promise.all(items.value.map(loadItem));
    await syncGrid();
  } catch (e) {
    error.value = errorText(e);
  }
}
async function switchProfile() {
  const next = dashboardProfile(window.innerWidth);
  if (next === profile.value) return;
  const revision = ++switching;
  if (editing.value) {
    capture();
    try {
      await saveLayout();
    } catch (e) {
      error.value = errorText(e);
      return;
    }
  }
  profile.value = next;
  layout.value = dashboardLayout(local(next)) ?? { items: {} };
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
  resizeObserver = new ResizeObserver((entries) => {
    if (editing.value || syncing) return;
    for (const entry of entries) {
      const el = entry.target.closest<HTMLElement>('.dashboard-grid-item');
      if (el && layout.value.items[el.getAttribute('gs-id') || '']?.h === undefined)
        measureHeight(el);
    }
  });
  grid = GridStack.init(
    {
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
  grid.on('dragstop', () => capture());
  grid.on('resizestop', (_event, el) => capture(el.getAttribute('gs-id') || undefined));
  window.addEventListener('resize', switchProfile);
  layout.value = dashboardLayout(local(profile.value)) ?? { items: {} };
  try {
    const [stored, order, meta] = await Promise.all([
      loadLayout(profile.value),
      getSetting('order'),
      api<DashboardMeta[]>('/plugins/dashboard/meta'),
    ]);
    if (!active) return;
    layout.value = stored;
    const ordering = Array.isArray(order) ? order : local('order');
    if (Array.isArray(ordering))
      meta.sort((a, b) => {
        const index = (item: DashboardMeta) => {
          const i = ordering.findIndex((x) => x.id === item.id && x.key === item.key);
          return i < 0 ? 999 : i;
        };
        return index(a) - index(b);
      });
    // The installed page obtains each plugin's declaration once, including a
    // hidden widget. Only visible widgets receive subsequent refresh timers.
    for (const m of meta) items.value.push(m);
    await Promise.all(meta.map(loadItem));
    await syncGrid();
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
});
watch(() => visible.value.map((x) => dashboardID(x) + JSON.stringify(x.cols)).join('|'), syncGrid);
onUnmounted(() => {
  active = false;
  switching++;
  window.removeEventListener('resize', switchProfile);
  for (const id of timers.keys()) clearTimer(id);
  resizeObserver?.disconnect();
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
            <VAlert v-if="errors[dashboardID(item)]" type="error"
              >{{ errors[dashboardID(item)]
              }}<VBtn variant="text" @click="loadItem(item)">重试</VBtn></VAlert
            >
            <PluginDashboardWidget v-else :item="item" @loaded="syncGrid" />
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
.grid-stack-item-content :deep(.v-card) {
  height: auto;
}
.is-manual-height .grid-stack-item-content :deep(.v-card) {
  height: 100%;
}
.grid-stack-item-content :deep(.v-card-text) {
  overflow: auto;
}
.dashboard-actions {
  position: fixed;
  right: 24px;
  bottom: 32px;
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
