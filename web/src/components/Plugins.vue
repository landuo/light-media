<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, defineAsyncComponent } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VMenu } from 'vuetify/components/VMenu';
import { VList, VListItem } from 'vuetify/components/VList';
import { VDialog } from 'vuetify/components/VDialog';
import { api, errorText, toast } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import AppIcon from './AppIcon.vue';
const PluginConfigDialog = defineAsyncComponent(() => import('./PluginConfigDialog.vue'));
const PluginDataDialog = defineAsyncComponent(() => import('./PluginDataDialog.vue'));
const detail = ref<PluginCard>();
function openPlugin(plugin: PluginCard) {
  if (!plugin.installed) return;
  if (plugin.has_page) detail.value = plugin;
  else selected.value = plugin;
}

const tab = ref('installed'),
  query = ref(''),
  items = ref<PluginCard[]>([]),
  market = ref<PluginCard[]>([]);
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
let timer: ReturnType<typeof setInterval>;
async function load() {
  try {
    [items.value, market.value] = await Promise.all([api('/plugins'), api('/plugins/market')]);
    error.value = '';
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function install(plugin: PluginCard) {
  if (busy.value) return;
  busy.value = true;
  try {
    await api('/plugins/install', { plugin_id: plugin.plugin_id });
    toast(plugin.name + '已安装');
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
        <input v-model="query" type="search" aria-label="搜索插件" placeholder="搜索插件" /><VBtn
          variant="text"
          icon
          aria-label="刷新插件"
          @click="load"
          ><AppIcon name="refresh"
        /></VBtn>
      </div>
    </div>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="loading" role="status">正在加载插件…</p>
    <div v-else-if="!visible.length" class="empty">
      {{ tab === 'installed' ? '尚未安装插件' : '没有匹配的可安装插件' }}
    </div>
    <div v-else class="plugin-grid">
      <VCard
        v-for="plugin in visible"
        :key="plugin.instance_id"
        class="plugin-card"
        @click="openPlugin(plugin)"
      >
        <div class="plugin-card-cover">
          <VCardTitle class="plugin-card-title"
            ><span class="plugin-state-dot" :class="{ active: plugin.state }"></span>{{ plugin.name
            }}<span class="plugin-version">v{{ plugin.version }}</span></VCardTitle
          >
          <div class="plugin-card-description">
            <span>{{ plugin.description }}</span>
            <div class="plugin-card-icon"><AppIcon name="plugin" /></div>
          </div>
        </div>
        <VCardText class="plugin-card-footer">
          <a :href="plugin.author_url" target="_blank" rel="noopener noreferrer" @click.stop>{{
            plugin.author
          }}</a>
          <VBtn
            v-if="!plugin.installed"
            size="small"
            variant="text"
            :disabled="busy"
            @click.stop="install(plugin)"
            >安装</VBtn
          >
          <VMenu v-else location="bottom end" :transition="false">
            <template #activator="{ props: menuProps }"
              ><VBtn
                v-bind="menuProps"
                variant="text"
                icon
                size="small"
                :aria-label="plugin.name + '操作'"
                @click.stop
                ><AppIcon name="more" /></VBtn
            ></template>
            <VList class="plugin-menu"
              ><VListItem v-if="plugin.has_page" title="详情" @click="detail = plugin" /><VListItem
                title="设置"
                @click="selected = plugin" /><VListItem
                title="重置"
                @click="confirmation = { plugin, action: 'reset' }" /><VListItem
                title="卸载"
                @click="confirmation = { plugin, action: 'uninstall' }" /><VListItem
                title="执行记录"
                @click="showRuns(plugin)"
            /></VList>
          </VMenu>
        </VCardText>
      </VCard>
    </div>
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
.plugin-card {
  cursor: pointer;
  border: 1px solid var(--surface-border);
  overflow: hidden;
}
.plugin-card-cover {
  background: linear-gradient(rgba(0, 0, 0, 0.6), rgba(0, 0, 0, 0.5)), #28a9e1;
  min-height: 108px;
  color: #fff;
}
.plugin-card-title {
  padding: 12px 16px 4px;
  font-size: 18px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.plugin-state-dot {
  height: 8px;
  width: 8px;
  background: #8a8d93;
  border-radius: 50%;
  flex: none;
}
.plugin-state-dot.active {
  background: #56ca00;
}
.plugin-version {
  font-size: 13px;
  opacity: 0.8;
}
.plugin-card-description {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 16px 12px;
  gap: 12px;
  font-size: 14px;
}
.plugin-card-description > span {
  line-height: 1.5;
  min-height: 42px;
}
.plugin-card-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: #ffffff21;
  flex: none;
}
.plugin-card-icon .app-icon {
  width: 28px;
  height: 28px;
}
.plugin-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 8px 2px 16px !important;
  height: 40px;
}
.plugin-card-footer a {
  font-size: 14px;
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
