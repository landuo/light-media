<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardText, VCardTitle } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VBadge } from 'vuetify/components/VBadge';
import { VIcon } from 'vuetify/components/VIcon';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import { pluginIconColor } from '../features/plugin-icon-color';
import { safePluginIcon } from '../features/plugin-clone';
import {
  pluginQuickAccessItems,
  pluginRecentItems,
  pluginRememberRecent,
  pluginPullDistance,
  recentPluginKey,
} from '../features/plugin-quick-access';
import { appInterface } from '../features/interface-mode';
const emit = defineEmits<{
  visibility: [visible: boolean];
  pull: [style: { transform: string; transition: string }];
}>();
const PluginDataDialog = defineAsyncComponent(() => import('./PluginDataDialog.vue'));
const display = useDisplay();
const open = ref(false),
  loading = ref(false),
  error = ref(''),
  distance = ref(0),
  pulling = ref(false),
  dragOffset = ref(0);
const items = ref<PluginCard[]>([]),
  recent = ref<PluginCard[]>([]),
  selected = ref<PluginCard>(),
  iconErrors = ref<Record<string, boolean>>({}),
  iconColors = ref<Record<string, string>>({});
let active = true,
  startY: number | undefined,
  initialScroll = 0,
  dialogOpen = false,
  dialogCheckedAt = 0;
let resetTimer: ReturnType<typeof setTimeout> | undefined;
const progress = computed(() => Math.min((distance.value - 60) / 40, 1));
const pullStyle = computed(() => ({
  transform:
    pulling.value && distance.value > 0 ? `translateY(${distance.value * 0.4}px)` : 'translateY(0)',
  transition: pulling.value ? 'none' : 'transform 0.3s cubic-bezier(0.25, 0.46, 0.45, 0.94)',
}));
watch(pullStyle, (value) => emit('pull', value));
function appMode() {
  return display.mdAndDown.value && appInterface.value;
}
watch(open, (value) => emit('visibility', value));
function eligible() {
  return appMode() && navigator.onLine && !document.hidden && !open.value && !selected.value;
}
function hasDialog() {
  try {
    return [
      '.v-overlay--active:not(.v-overlay--scroll-blocked)',
      '.v-dialog--active',
      '.v-menu--active',
      '.v-bottom-sheet--active',
      '.v-snackbar--active',
      '[role="dialog"]:not([style*="display: none"])',
      '.modal:not(.d-none):not([style*="display: none"])',
      '[aria-modal="true"]:not([style*="display: none"])',
    ].some((selector) => document.querySelector(selector));
  } catch {
    return true;
  }
}
async function load() {
  if (loading.value) return;
  loading.value = true;
  try {
    const result = await api<PluginCard[]>('/plugins?state=installed');
    if (!Array.isArray(result)) throw new Error('插件列表返回格式无效');
    if (active) {
      items.value = pluginQuickAccessItems(result);
      error.value = '';
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
function show() {
  open.value = true;
  try {
    recent.value = pluginRecentItems(localStorage.getItem(recentPluginKey));
  } catch {
    recent.value = [];
  }
  void load();
}
async function iconLoaded(event: Event, plugin: PluginCard) {
  const color = await pluginIconColor(event.target as HTMLImageElement);
  if (active) iconColors.value[plugin.instance_id] = color;
}
function iconError(plugin: PluginCard) {
  iconErrors.value[plugin.instance_id] = true;
}
function choose(plugin: PluginCard) {
  open.value = false;
  recent.value = pluginRememberRecent(recent.value, plugin);
  try {
    localStorage.setItem(recentPluginKey, JSON.stringify(recent.value));
  } catch {
    /* storage may be disabled; opening still works */
  }
  selected.value = items.value.find((item) => item.instance_id === plugin.instance_id) || plugin;
}
let closeStart = 0,
  closeLast = 0,
  closeTime = 0,
  closeVelocity = 0,
  closeDragging = false;
function bottomStart(event: TouchEvent) {
  if (!open.value || !event.touches[0]) return;
  event.stopPropagation();
  closeStart = closeLast = event.touches[0].clientY;
  closeTime = Date.now();
  closeVelocity = 0;
  closeDragging = false;
  dragOffset.value = 0;
}
function bottomMove(event: TouchEvent) {
  if (!open.value || !event.touches[0]) return;
  event.preventDefault();
  event.stopPropagation();
  const y = event.touches[0].clientY,
    now = Date.now(),
    delta = closeStart - y;
  if (now > closeTime) closeVelocity = (closeLast - y) / (now - closeTime);
  if (closeDragging) {
    if (delta >= 0) dragOffset.value = Math.min(delta, 1000);
    else {
      closeDragging = false;
      dragOffset.value = 0;
    }
  } else if (delta > 10) {
    closeDragging = true;
    dragOffset.value = Math.min(delta, 1000);
  }
  closeLast = y;
  closeTime = now;
}
function bottomEnd(event: TouchEvent) {
  if (!open.value) return;
  event.stopPropagation();
  if (closeDragging && (dragOffset.value >= 100 || closeVelocity >= 0.8)) open.value = false;
  closeStart = closeLast = closeVelocity = 0;
  closeDragging = false;
  dragOffset.value = 0;
}
function touchStart(event: TouchEvent) {
  startY = undefined;
  if (!eligible() || event.touches.length !== 1) return;
  dialogOpen = hasDialog();
  dialogCheckedAt = Date.now();
  if (dialogOpen) return;
  startY = event.touches[0]!.clientY;
  initialScroll = window.scrollY || document.documentElement.scrollTop || 0;
  pulling.value = false;
  distance.value = 0;
}
function touchMove(event: TouchEvent) {
  if (startY === undefined || !eligible() || event.touches.length !== 1) return;
  const now = Date.now();
  if (now - dialogCheckedAt > 500) {
    dialogOpen = hasDialog();
    dialogCheckedAt = now;
  }
  if (dialogOpen) {
    pulling.value = false;
    distance.value = 0;
    return;
  }
  const y = event.touches[0]!.clientY,
    delta = y - startY;
  if (pulling.value) {
    if (delta > -80) {
      distance.value = Math.max(0, Math.min(delta * 0.75, 200));
      if (event.cancelable) event.preventDefault();
    } else {
      pulling.value = false;
      distance.value = 0;
    }
  } else if (
    delta > 20 &&
    initialScroll <= 100 &&
    (window.scrollY || document.documentElement.scrollTop || 0) <= 100
  ) {
    pulling.value = true;
    distance.value = pluginPullDistance(startY, y);
    if (event.cancelable) event.preventDefault();
  }
}
function touchEnd() {
  if (!eligible()) return;
  dialogOpen = false;
  dialogCheckedAt = 0;
  if (pulling.value && distance.value >= 100) show();
  pulling.value = false;
  resetTimer = setTimeout(() => {
    startY = undefined;
    distance.value = 0;
  }, 300);
}
function touchCancel() {
  startY = undefined;
  pulling.value = false;
  distance.value = 0;
}
onMounted(() => {
  document.addEventListener('touchstart', touchStart, { passive: true });
  document.addEventListener('touchmove', touchMove, { passive: false });
  document.addEventListener('touchend', touchEnd);
  document.addEventListener('touchcancel', touchCancel);
});
onUnmounted(() => {
  active = false;
  emit('visibility', false);
  emit('pull', { transform: 'translateY(0)', transition: 'none' });
  clearTimeout(resetTimer);
  document.removeEventListener('touchstart', touchStart);
  document.removeEventListener('touchmove', touchMove);
  document.removeEventListener('touchend', touchEnd);
  document.removeEventListener('touchcancel', touchCancel);
});
</script>
<template>
  <div
    v-if="pulling && distance >= 60"
    class="plugin-pull-indicator"
    role="status"
    aria-label="下拉插件快捷访问"
    :style="{
      opacity: 0.7 + progress * 0.3,
      transform: `translate(-50%, ${Math.min(60 + distance - 60, 70)}px)`,
    }"
  >
    <VIcon
      icon="mdi-gesture-swipe-down"
      size="24"
      :color="distance >= 100 ? 'success' : 'primary'"
      :style="{
        transform: `scale(${1 + Math.min((distance - 60) / 200, 0.5) * 0.3}) rotate(${progress * 180}deg)`,
      }"
    />
  </div>
  <VDialog
    v-model="open"
    fullscreen
    scrollable
    content-class="plugin-dialog-surface plugin-quick-access"
  >
    <VCard
      :style="{
        transform: `translateY(-${dragOffset}px)`,
        transition: dragOffset ? 'none' : 'transform 0.6s cubic-bezier(0.4, 0, 0.2, 1)',
      }"
    >
      <VCardTitle class="d-flex align-center justify-space-between"
        >插件快捷访问<VBtn
          variant="text"
          icon="mdi-close"
          aria-label="关闭插件快捷访问"
          @click="open = false"
      /></VCardTitle>
      <VCardText class="plugin-quick-scroll">
        <VAlert v-if="error" type="error" variant="tonal"
          >{{ error }}<VBtn variant="text" @click="load">重试</VBtn></VAlert
        >
        <p v-if="loading" role="status">正在加载…</p>
        <template
          v-for="section in [
            { name: '最近访问', cards: recent },
            { name: '全部插件', cards: items },
          ]"
          :key="section.name"
        >
          <template v-if="section.cards.length"
            ><h3 class="text-subtitle-1 my-4">{{ section.name }}</h3>
            <div
              :class="[
                'plugin-quick-grid',
                section.name === '最近访问' ? 'recent-grid' : 'all-grid',
              ]"
            >
              <VBtn
                v-for="plugin in section.cards"
                :key="plugin.instance_id"
                variant="text"
                class="plugin-quick-item"
                @click="choose(plugin)"
              >
                <VBadge dot :color="plugin.state ? 'success' : 'secondary'" location="top end"
                  ><img
                    :src="
                      safePluginIcon(
                        iconErrors[plugin.instance_id] ? 'plugin.png' : plugin.icon || 'plugin.png',
                      )
                    "
                    :style="{ background: iconColors[plugin.instance_id] || '#28A9E1' }"
                    @load="iconLoaded($event, plugin)"
                    @error="iconError(plugin)"
                    alt=""
                    referrerpolicy="no-referrer"
                /></VBadge>
                <span>{{ plugin.name }}</span>
              </VBtn>
            </div>
          </template>
        </template>
        <p v-if="!loading && !error && !items.length">没有提供详情页面的插件</p>
      </VCardText>
      <div
        class="bottom-drag-area"
        role="button"
        tabindex="0"
        aria-label="上滑关闭插件快捷访问"
        @touchstart="bottomStart"
        @touchmove="bottomMove"
        @touchend="bottomEnd"
        @touchcancel="bottomEnd"
        @click="open = false"
        @keydown.enter="open = false"
      >
        <VIcon icon="mdi-chevron-up" size="small" /><span>上滑关闭</span>
      </div>
    </VCard>
  </VDialog>
  <PluginDataDialog
    v-if="selected"
    :key="selected.instance_id"
    :plugin="selected"
    :show-switch="false"
    @close="selected = undefined"
  />
</template>
<style scoped>
.plugin-pull-indicator {
  position: fixed;
  top: 0;
  left: 50%;
  transform: translateX(-50%);
  z-index: 2000;
  padding: 8px 16px;
  border-radius: 20px;
  background: rgb(var(--v-theme-surface));
  color: rgb(var(--v-theme-primary));
  box-shadow: 0 2px 8px #0002;
}
.bottom-drag-area {
  flex: 0 0 72px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: rgb(var(--v-theme-on-surface-variant));
  padding-bottom: env(safe-area-inset-bottom, 0px);
  touch-action: none;
}
.plugin-quick-scroll {
  overscroll-behavior: contain;
  scrollbar-width: none;
  padding: 24px 20px;
}
.plugin-quick-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(90px, 1fr));
  gap: 16px;
}
.all-grid {
  gap: 4px;
}
.plugin-quick-item {
  height: 120px;
  min-width: 0;
}
.plugin-quick-item :deep(.v-btn__content) {
  flex-direction: column;
  width: 100%;
  gap: 8px;
}
.plugin-quick-item img {
  width: 64px;
  height: 64px;
  padding: 4px;
  border-radius: 16px;
  object-fit: cover;
}
.plugin-quick-item span {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  white-space: normal;
  word-break: break-all;
  font-size: 12px;
  line-height: 1.2;
}
</style>
