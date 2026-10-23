<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VChip } from 'vuetify/components/VChip';
import { VIcon } from 'vuetify/components/VIcon';
import { VSelect } from 'vuetify/components/VSelect';
import { VTextField } from 'vuetify/components/VTextField';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { VDivider } from 'vuetify/components/VDivider';
import { VAlert } from 'vuetify/components/VAlert';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
import { api, apiURL, errorText } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import {
  pluginLogLevels,
  pluginLogEntry,
  appendPluginLogs,
  groupPluginLogs,
  filterPluginLogGroups,
  type PluginLogEntry,
} from '../features/plugin-logs';

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: [] }>();
const display = useDisplay();
const entries = ref<PluginLogEntry[]>([]);
const level = ref('ALL');
const levelColors: Record<string, string> = {
  DEBUG: 'secondary',
  INFO: 'success',
  WARNING: 'warning',
  ERROR: 'error',
  CRITICAL: 'error',
};
const query = ref<string | null>('');
const paused = ref(false),
  loading = ref(true),
  connected = ref(false);
const following = ref(true),
  pending = ref(0),
  error = ref(''),
  gap = ref('');
const viewport = ref<HTMLElement>();
const endpoint = '/plugins/' + encodeURIComponent(props.plugin.instance_id) + '/logs';
const levels = computed(() => [
  {
    title: '全部级别',
    value: 'ALL',
    props: { class: 'text-primary font-weight-medium', density: 'compact' },
  },
  ...[...new Set([...pluginLogLevels, ...entries.value.map((item) => item.level)])]
    .filter(Boolean)
    .map((value) => ({
      title: value,
      value,
      props: {
        class: 'text-' + (levelColors[value] || 'secondary') + ' font-weight-medium',
        density: 'compact',
      },
    })),
]);
const groups = computed(() =>
  filterPluginLogGroups(groupPluginLogs(entries.value), level.value, query.value),
);
const lastVisible = computed(() => groups.value.at(-1)?.items.at(-1)?.id ?? '');
let active = true;
let stream: EventSource | null = null;
let lastID = '';
let retries = 0;
let flushTimer: ReturnType<typeof setTimeout> | undefined;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
let hiddenTimer: ReturnType<typeof setTimeout> | undefined;
let initialTimer: ReturnType<typeof setTimeout> | undefined;
const buffer: PluginLogEntry[] = [];

function nearBottom() {
  const el = viewport.value;
  return !el || el.scrollHeight - el.scrollTop - el.clientHeight <= 32;
}
function follow(behavior: ScrollBehavior = 'auto') {
  following.value = true;
  pending.value = 0;
  nextTick(() => {
    const el = viewport.value;
    el?.scrollTo({ top: el.scrollHeight, behavior });
  });
}
function onScroll() {
  following.value = nearBottom();
  if (following.value) pending.value = 0;
}
function flush() {
  clearTimeout(flushTimer);
  flushTimer = undefined;
  if (!buffer.length) return;
  const shouldFollow = nearBottom();
  const result = appendPluginLogs(entries.value, buffer.splice(0));
  entries.value = result.entries;
  following.value = shouldFollow;
  if (shouldFollow) follow();
  else pending.value += result.count;
}
function closeStream() {
  clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
  stream?.close();
  stream = null;
  connected.value = false;
}
function connect() {
  closeStream();
  if (!active || paused.value || document.hidden) return;
  const params = lastID ? '?last_event_id=' + encodeURIComponent(lastID) : '';
  const current = new EventSource(apiURL(endpoint + '/stream' + params), { withCredentials: true });
  stream = current;
  current.onopen = () => {
    if (stream !== current) return;
    connected.value = true;
    retries = 0;
    error.value = '';
  };
  current.onmessage = (event) => {
    if (!active || stream !== current || paused.value) return;
    try {
      if (event.data.length > 20 << 10) return;
      const entry = pluginLogEntry(JSON.parse(event.data), props.plugin.instance_id);
      if (!entry) return;
      lastID = event.lastEventId;
      buffer.push(entry);
      if (buffer.length >= 600) flush();
      if (!flushTimer) flushTimer = setTimeout(flush, 80);
    } catch {
      error.value = '收到无效日志数据，请重新连接';
    }
  };
  current.addEventListener('gap', () => {
    if (stream !== current) return;
    flush();
    closeStream();
    gap.value = '实时日志存在缺口，正在重新读取保留日志';
    lastID = '';
    reloadHistory();
  });
  current.onerror = () => {
    if (stream !== current) return;
    closeStream();
    if (!active || paused.value || document.hidden) return;
    if (++retries <= 3) {
      error.value = '日志连接已断开，正在重新连接';
      reconnectTimer = setTimeout(connect, 3000);
    } else {
      error.value = '日志连接已断开，请点击重试';
    }
  };
}
async function reloadHistory() {
  closeStream();
  clearTimeout(initialTimer);
  loading.value = true;
  try {
    const result = await api(endpoint);
    if (!active) return;
    const incoming = (result.history?.entries ?? [])
      .map((value: any) => pluginLogEntry(value, props.plugin.instance_id))
      .filter((value: PluginLogEntry | null): value is PluginLogEntry => !!value)
      .reverse();
    entries.value = incoming;
    lastID = result.resume_id ?? '';
    gap.value = result.history?.gap ? '日志采集或保留历史存在缺口，可打开完整日志查看现有记录' : '';
    error.value = '';
    retries = 0;
    follow();
    initialTimer = setTimeout(connect, 300);
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
function toggleStream() {
  if (paused.value) {
    paused.value = false;
    retries = 0;
    connect();
  } else {
    flush();
    paused.value = true;
    closeStream();
  }
}
function onVisibility() {
  clearTimeout(hiddenTimer);
  if (document.hidden)
    hiddenTimer = setTimeout(() => {
      flush();
      closeStream();
    }, 5000);
  else {
    retries = 0;
    connect();
  }
}
watch(lastVisible, (current, previous) => {
  if (following.value && current !== previous) follow();
});
onMounted(() => {
  document.addEventListener('visibilitychange', onVisibility);
  reloadHistory();
});
onUnmounted(() => {
  active = false;
  document.removeEventListener('visibilitychange', onVisibility);
  clearTimeout(flushTimer);
  clearTimeout(reconnectTimer);
  clearTimeout(hiddenTimer);
  clearTimeout(initialTimer);
  closeStream();
  buffer.length = 0;
});
</script>

<template>
  <VDialog
    :model-value="true"
    scrollable
    max-width="72rem"
    :fullscreen="!display.mdAndUp.value"
    content-class="plugin-log-surface"
    @update:model-value="emit('close')"
  >
    <VCard class="plugin-log-card">
      <PluginDialogCloseBtn aria-label="关闭插件日志" @click="emit('close')" />
      <VCardItem class="plugin-log-header">
        <VCardTitle class="plugin-log-title">
          <VIcon icon="mdi-file-document" class="me-2" />插件日志
          <span class="plugin-log-links">
            <a :href="apiURL(endpoint + '/download')" target="_blank" rel="noopener noreferrer">
              <VChip size="small" color="grey-darken-1"
                ><VIcon icon="mdi-download" size="small" start />下载</VChip
              >
            </a>
            <a :href="apiURL(endpoint + '/file')" target="_blank" rel="noopener noreferrer">
              <VChip size="small" color="grey-darken-1"
                ><VIcon icon="mdi-open-in-new" size="small" start />新窗口打开</VChip
              >
            </a>
          </span>
        </VCardTitle>
      </VCardItem>
      <VDivider class="plugin-log-divider" />
      <VCardText class="plugin-log-content">
        <VProgressLinear
          v-if="!paused"
          :indeterminate="connected || loading"
          color="primary"
          height="1"
        />
        <div class="logging-view">
          <div class="logging-toolbar">
            <VSelect
              v-model="level"
              :items="levels"
              density="compact"
              variant="plain"
              hide-details
              class="logging-level-select"
              aria-label="日志级别"
              :menu-props="{ width: 'auto' }"
              :style="{
                '--log-level-color': `rgb(var(--v-theme-${level === 'ALL' ? 'primary' : levelColors[level] || 'secondary'}))`,
              }"
            />
            <VTextField
              v-model="query"
              class="logging-search"
              density="compact"
              variant="plain"
              hide-details
              clearable
              prepend-inner-icon="mdi-magnify"
              placeholder="搜索日志内容"
              aria-label="搜索日志内容"
              @click:clear="query = ''"
            />
            <VBtn
              variant="text"
              icon
              class="logging-stream-action"
              :class="{ 'is-live': !paused }"
              :title="paused ? '恢复日志流' : '暂停日志流'"
              :aria-label="paused ? '恢复日志流' : '暂停日志流'"
              @click="toggleStream"
              ><VIcon :icon="paused ? 'mdi-play' : 'mdi-pause'"
            /></VBtn>
          </div>
          <VAlert v-if="error" type="error" variant="tonal" density="compact"
            >{{ error
            }}<VBtn
              variant="text"
              size="small"
              @click="
                retries = 0;
                reloadHistory();
              "
              >重试</VBtn
            ></VAlert
          >
          <VAlert v-if="gap" type="warning" variant="tonal" density="compact">{{ gap }}</VAlert>
          <div ref="viewport" class="logging-shell" @scroll.passive="onScroll">
            <div v-if="loading" class="logging-empty">正在初始化日志…</div>
            <div v-else-if="!groups.length" class="logging-empty">
              <VIcon
                :icon="entries.length ? 'mdi-filter-remove-outline' : 'mdi-console-line'"
                size="20"
              />{{ entries.length ? '没有匹配的数据' : '等待日志输出' }}
            </div>
            <div v-else class="logging-list">
              <div v-for="(group, index) in groups" :key="group.id" class="logging-record">
                <div class="logging-record-time">{{ group.secondDisplay || '...' }}</div>
                <div class="logging-record-panel" :class="index % 2 === 0 ? 'is-even' : 'is-odd'">
                  <div
                    class="logging-record-accent"
                    :class="'level-' + (group.level || 'plain').toLowerCase()"
                  />
                  <div class="logging-record-lines">
                    <div v-for="item in group.items" :key="item.id" class="logging-record-line">
                      <div
                        class="logging-record-level"
                        :class="'level-' + (item.level || 'plain').toLowerCase()"
                      >
                        {{ item.level || 'LOG' }}:
                      </div>
                      <div class="logging-record-body">
                        <span v-if="item.timeDisplay" class="logging-record-inline-time">{{
                          item.timeDisplay
                        }}</span
                        >{{ item.message }}
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <div v-if="pending > 0 && !following" class="logging-latest-action">
              <VBtn
                size="small"
                color="primary"
                variant="elevated"
                prepend-icon="mdi-arrow-down"
                @click="follow('smooth')"
                >查看最新 ({{ pending }})</VBtn
              >
            </div>
          </div>
        </div>
      </VCardText>
    </VCard>
  </VDialog>
</template>

<style scoped>
.plugin-log-card {
  height: calc(100dvh - 3rem);
  min-height: 20rem;
}
.plugin-log-header {
  padding: 16px !important;
  padding-inline-end: 60px !important;
}
.plugin-log-title {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  padding: 0 !important;
  font-size: 20px;
}
.plugin-log-divider {
  flex: 0 0 auto;
  margin: 0 !important;
}
.plugin-log-links {
  display: inline-flex;
  align-items: center;
  margin-inline-start: 1rem;
  gap: 0.25rem;
}
.plugin-log-links a {
  display: inline-flex;
  text-decoration: none;
}
.plugin-log-content {
  padding: 0 !important;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.logging-view {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  color: rgba(var(--v-theme-on-surface), 0.88);
}
.logging-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding: 0 12px;
}
.logging-search {
  flex: 1 1 9rem;
  min-width: 5rem;
}
.logging-level-select {
  flex: 0 0 7rem;
  min-width: 7rem;
}
.logging-toolbar :deep(.v-field) {
  background: transparent !important;
  box-shadow: none !important;
}
.logging-toolbar :deep(.v-field__outline),
.logging-toolbar :deep(.v-field__overlay) {
  display: none;
}
.logging-toolbar :deep(.v-field input) {
  border: 0 !important;
  background: transparent !important;
  box-shadow: none !important;
  height: auto !important;
  margin: 0;
  font: inherit;
  color: inherit;
  padding: var(--v-field-input-padding-top) var(--v-field-padding-end)
    var(--v-field-input-padding-bottom) var(--v-field-padding-start);
}
.logging-search :deep(.v-field__input) {
  padding-inline-start: 0;
}
.logging-level-select :deep(.v-select__selection) {
  color: var(--log-level-color);
  font-weight: 500;
}
.logging-level-select :deep(.v-field__input) {
  padding-inline: 0;
}
.logging-stream-action {
  color: rgba(var(--v-theme-on-surface), 0.56);
}
.logging-stream-action.is-live {
  color: rgb(var(--v-theme-success));
}
.logging-shell {
  position: relative;
  overflow: auto;
  flex: 1 1 auto;
  padding: 0.875rem;
  background: rgba(var(--v-theme-surface), 0.96);
}
.logging-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  min-height: 16rem;
  height: 100%;
  color: rgba(var(--v-theme-on-surface), 0.56);
}
.logging-list {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  min-width: 100%;
}
.logging-record {
  display: grid;
  align-items: start;
  grid-template-columns: 11rem minmax(0, 1fr);
}
.logging-record-time {
  color: rgb(var(--v-theme-primary));
  font:
    600 0.8125rem/1.5 Consolas,
    monospace;
  padding-block: 0.625rem;
  white-space: nowrap;
}
.logging-record-panel {
  display: flex;
  align-items: stretch;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.04);
  border-radius: 6px;
  gap: 0.75rem;
  min-width: 0;
  padding: 0.5rem;
}
.logging-record-panel.is-even {
  background: rgba(var(--v-theme-surface-variant), 0.01);
}
.logging-record-panel.is-odd {
  background: rgba(var(--v-theme-surface-variant), 0.005);
}
.logging-record-accent {
  flex: 0 0 0.5rem;
  align-self: flex-start;
  border-radius: 999px;
  background: rgba(var(--v-theme-on-surface), 0.24);
  height: 0.5rem;
  margin-top: 0.45rem;
}
.logging-record-lines {
  flex: 1;
  min-width: 0;
}
.logging-record-line {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
  min-width: 0;
  font-size: 0.8125rem;
  line-height: 1.6;
}
.logging-record-level {
  flex: 0 0 5.25rem;
  font:
    600 0.75rem/1.75 Consolas,
    monospace;
}
.logging-record-body {
  flex: 1;
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.logging-record-inline-time {
  color: rgba(var(--v-theme-on-surface), 0.56);
  font:
    0.75rem Consolas,
    monospace;
  margin-inline-end: 0.5rem;
}
.logging-record-level.level-info {
  color: rgb(var(--v-theme-success));
}
.logging-record-level.level-warning {
  color: rgb(var(--v-theme-warning));
}
.logging-record-level.level-error,
.logging-record-level.level-critical {
  color: rgb(var(--v-theme-error));
}
.logging-record-accent.level-info {
  background: rgb(var(--v-theme-success));
}
.logging-record-accent.level-warning {
  background: rgb(var(--v-theme-warning));
}
.logging-record-accent.level-error,
.logging-record-accent.level-critical {
  background: rgb(var(--v-theme-error));
}
.logging-latest-action {
  position: sticky;
  bottom: 1rem;
  display: flex;
  justify-content: center;
  pointer-events: none;
}
.logging-latest-action .v-btn {
  pointer-events: auto;
}
@media (max-width: 959px) {
  .plugin-log-card {
    height: 100dvh;
  }
  .plugin-log-title {
    gap: 0.5rem;
    padding: 12px;
    font-size: 18px;
  }
  .plugin-log-links {
    gap: 0.25rem;
  }
  .logging-record {
    grid-template-columns: 1fr;
  }
  .logging-record-time {
    padding-block: 0.35rem;
  }
  .logging-record-level {
    flex-basis: 4.75rem;
  }
}
</style>
