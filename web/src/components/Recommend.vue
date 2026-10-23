<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watch,
} from 'vue';
import { useDisplay } from 'vuetify';
import { VTab, VTabs } from 'vuetify/components/VTabs';
import { VBtn } from 'vuetify/components/VBtn';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VDialog } from 'vuetify/components/VDialog';
import { VDivider } from 'vuetify/components/VDivider';
import { VIcon } from 'vuetify/components/VIcon';
import { VProgressCircular } from 'vuetify/components/VProgressCircular';
import { api, errorText } from '../api';
import RecommendShelf from './RecommendShelf.vue';
import {
  recommendCategories,
  recommendEnabled,
  recommendSourceVisible,
  saveRecommendPreferences,
  type RecommendCategory,
  type RecommendSource,
} from '../features/recommend';

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true });
const display = useDisplay();
const sources = ref<RecommendSource[]>([]),
  category = ref<RecommendCategory>('all');
const preferences = reactive({ enabled: {} as Record<string, boolean>, saving: false, error: '' });
const loading = ref(false),
  loaded = ref(false),
  loadError = ref('');
const activated = ref(true),
  mounted = ref(false),
  headerReady = ref(false),
  scrollY = ref(0);
const visible = computed(() => props.active && activated.value);
const shown = computed(() =>
  sources.value.filter((source) =>
    recommendSourceVisible(source, category.value, preferences.enabled),
  ),
);
const settingsDialog = ref(false),
  draft = ref<Record<string, boolean>>({});
let generation = 0,
  lastScroll = 0,
  wasDeactivated = false;

async function loadSettings() {
  if (loading.value) return;
  const token = ++generation;
  loading.value = true;
  loadError.value = '';
  try {
    const [catalog, saved] = await Promise.all([
      api<{ sources: RecommendSource[] }>('/catalog/recommend/sources'),
      api<{ enabled: Record<string, boolean> }>('/catalog/recommend/preferences'),
    ]);
    if (token !== generation) return;
    sources.value = catalog.sources || [];
    preferences.enabled = recommendEnabled(sources.value, saved.enabled);
    loaded.value = true;
  } catch (error) {
    if (token === generation) loadError.value = errorText(error);
  } finally {
    if (token === generation) loading.value = false;
  }
}
function openSettings() {
  draft.value = { ...preferences.enabled };
  preferences.error = '';
  settingsDialog.value = true;
}
function setAll(enabled: boolean) {
  draft.value = Object.fromEntries(sources.value.map((source) => [source.id, enabled]));
}
async function save() {
  const saved = await saveRecommendPreferences(
    preferences,
    sources.value,
    draft.value,
    (enabled) => api('/catalog/recommend/preferences', { enabled }, 'PUT'),
    errorText,
  );
  if (saved) settingsDialog.value = false;
}
function onScroll() {
  if (visible.value) scrollY.value = window.scrollY;
}
function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' });
}
async function locateHeader() {
  await nextTick();
  headerReady.value = Boolean(document.querySelector('#recommend-header'));
}
watch(visible, () => {
  void locateHeader();
  if (!visible.value) settingsDialog.value = false;
});
onMounted(() => {
  mounted.value = true;
  void locateHeader();
  void loadSettings();
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });
});
onActivated(() => {
  activated.value = true;
  void locateHeader();
  if (wasDeactivated) void nextTick(() => window.scrollTo({ top: lastScroll, behavior: 'auto' }));
});
onDeactivated(() => {
  lastScroll = scrollY.value;
  wasDeactivated = true;
  activated.value = false;
});
onUnmounted(() => {
  generation++;
  window.removeEventListener('scroll', onScroll);
});
</script>

<template>
  <div class="recommend-page">
    <Teleport v-if="mounted && visible" to="#recommend-header" :disabled="!headerReady">
      <VTabs v-model="category" class="recommend-tabs" color="primary" height="48">
        <VTab v-for="item in recommendCategories" :key="item.id" :value="item.id"
          ><VIcon :icon="item.icon" size="16" class="category-icon" />{{ item.title }}</VTab
        >
      </VTabs>
    </Teleport>
    <div v-if="loading && !loaded" class="recommend-loading" role="status" aria-label="正在加载">
      <VProgressCircular indeterminate color="primary" />
    </div>
    <div v-if="loadError" class="recommend-error" role="alert">
      <span>{{ loadError }}</span
      ><VBtn variant="text" size="small" :loading="loading" @click="loadSettings">重试</VBtn>
    </div>
    <div class="recommend-content">
      <RecommendShelf
        v-for="source in sources"
        v-show="recommendSourceVisible(source, category, preferences.enabled)"
        :key="source.id"
        :source="source"
        :active="visible && recommendSourceVisible(source, category, preferences.enabled)"
      />
      <div v-if="loaded && !shown.length" class="empty-category">
        <VIcon icon="mdi-alert-circle-outline" size="large" class="empty-icon" />
        <p>当前分类下没有可显示的内容</p>
        <VBtn color="primary" variant="tonal" size="small" @click="openSettings">设置显示内容</VBtn>
      </div>
    </div>
    <VDialog
      v-model="settingsDialog"
      width="35rem"
      :fullscreen="!display.mdAndUp.value"
      :persistent="preferences.saving"
      scrollable
    >
      <VCard class="recommend-settings">
        <div class="settings-card-header">
          <VCardTitle
            ><VIcon
              icon="mdi-tune"
              size="small"
              class="settings-title-icon"
            />自定义推荐</VCardTitle
          ><VBtn
            icon="mdi-close"
            variant="text"
            color="secondary"
            aria-label="关闭"
            :disabled="preferences.saving"
            @click="settingsDialog = false"
          />
        </div>
        <VDivider />
        <VCardText>
          <p class="settings-hint">选择您想在页面显示的内容</p>
          <div class="settings-grid">
            <button
              v-for="source in sources"
              :key="source.id"
              type="button"
              class="setting-item"
              :class="{ enabled: draft[source.id] }"
              role="checkbox"
              :aria-checked="draft[source.id]"
              :disabled="preferences.saving"
              @click="draft[source.id] = !draft[source.id]"
            >
              <VIcon
                :icon="draft[source.id] ? 'mdi-check-circle' : 'mdi-circle-outline'"
                :color="draft[source.id] ? 'primary' : undefined"
                size="small"
              /><span class="setting-label">{{ source.title }}</span>
            </button>
          </div>
          <p v-if="preferences.error" class="settings-error" role="alert">
            {{ preferences.error }}
          </p>
        </VCardText>
        <VCardActions class="settings-actions"
          ><VBtn
            color="success"
            variant="tonal"
            :disabled="preferences.saving"
            @click="setAll(true)"
            >全选</VBtn
          ><VBtn
            color="warning"
            variant="tonal"
            :disabled="preferences.saving"
            @click="setAll(false)"
            >全不选</VBtn
          ><span class="settings-spacer"></span
          ><VBtn
            color="primary"
            variant="flat"
            prepend-icon="mdi-content-save"
            :loading="preferences.saving"
            @click="save"
            >保存</VBtn
          ></VCardActions
        >
      </VCard>
    </VDialog>
    <Teleport to="body">
      <div v-if="visible && loaded" class="recommend-fab-stack">
        <button
          v-if="scrollY > 200 && display.smAndUp.value"
          class="recommend-scroll-top"
          aria-label="返回顶部"
          @click="scrollToTop"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path
              d="M7 14L12 9L17 14"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
        <VBtn
          icon="mdi-tune"
          color="primary"
          class="recommend-tune"
          aria-label="自定义推荐"
          @click="openSettings"
        />
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.recommend-page {
  position: relative;
  padding: 4px 0 0;
  max-width: 100%;
}
.recommend-content {
  padding-block: 0;
}
.recommend-tabs {
  width: 100%;
  min-width: 0;
}
.recommend-tabs :deep(.v-tab.v-btn) {
  min-width: 0;
  margin-right: 12px;
  padding: 6px 14px;
  gap: 0;
  font-size: 0.9rem;
  font-weight: 600;
  letter-spacing: normal;
  text-indent: 0;
  text-transform: none;
}
.recommend-tabs :deep(.v-tab__slider) {
  width: 70%;
  height: 3px;
  margin-inline: auto;
  border-radius: 4px;
}
.category-icon {
  margin-right: 8px;
}
.recommend-tabs :deep(.v-tab .v-btn__overlay) {
  opacity: 0;
}
.recommend-tabs :deep(.v-tab:hover) {
  background: rgba(var(--v-theme-primary), 0.05);
}
.recommend-tabs :deep(.v-slide-group__prev),
.recommend-tabs :deep(.v-slide-group__next) {
  flex-basis: 28px;
  min-width: 28px;
}
.recommend-loading {
  display: flex;
  justify-content: center;
  margin-top: 48px;
}
.recommend-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--danger);
  padding: 32px;
}
.empty-category {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
  color: rgba(var(--v-theme-on-surface), 0.6);
  text-align: center;
}
.empty-icon {
  margin-bottom: 16px;
  opacity: 0.5;
}
.empty-category p {
  margin: 0 0 16px;
  font-size: 1rem;
}
.settings-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: none;
  height: 64px;
  padding: 16px;
}
.settings-card-header > .v-btn {
  width: 32px;
  height: 32px;
}
.recommend-settings > .v-divider {
  width: 100%;
  margin: 0;
}
.recommend-settings > .v-card-text {
  padding: 20px !important;
}
.recommend-settings {
  font-family: ui-sans-serif, system-ui, sans-serif;
  letter-spacing: 0.15px;
}
.settings-card-header .v-card-title {
  padding: 0;
}
.settings-title-icon {
  margin-right: 8px;
}
.settings-hint {
  color: rgba(var(--v-theme-on-surface), 0.7);
  font-size: 0.9rem;
  line-height: 1.5;
  margin: 0 0 16px;
}
.settings-grid {
  display: grid;
  gap: 10px;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
}
.setting-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  overflow: hidden;
  border: 1px solid transparent;
  border-radius: var(--radius);
  background: rgba(var(--v-theme-surface-variant), 0.3);
  cursor: pointer;
  padding: 10px 12px;
  transition: all 0.2s ease;
  text-align: left;
  white-space: normal;
  font-family: inherit;
  letter-spacing: inherit;
}
.setting-item::before {
  position: absolute;
  background: linear-gradient(90deg, rgb(var(--v-theme-primary)) 0%, transparent 100%);
  content: '';
  width: 3px;
  inset-block: 0;
  left: 0;
  opacity: 0.3;
  transition: opacity 0.2s ease;
}
.setting-item.enabled {
  border-color: rgba(var(--v-theme-primary), 0.4);
  background: rgba(var(--v-theme-primary), 0.08);
}
.setting-item.enabled::before {
  opacity: 1;
}
.setting-item .v-icon {
  flex: none;
  width: 20px;
  height: 20px;
  font-size: 20px;
}
.setting-label {
  flex: 1;
  color: rgba(var(--v-theme-on-surface), 0.8);
  font-size: 0.9rem;
  font-weight: 550;
  line-height: 1.35;
}
.settings-error {
  margin: 16px 0 0;
  color: var(--danger);
}
.settings-actions {
  padding: 12px 24px 16px;
  gap: 8px;
}
.settings-actions > .v-btn {
  margin: 0;
}
.settings-spacer {
  flex: 1;
}
.recommend-fab-stack {
  position: fixed;
  z-index: 100;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  bottom: max(1rem, calc(env(safe-area-inset-bottom) + 1rem));
  right: max(1rem, calc(env(safe-area-inset-right) + 1rem));
}
.recommend-tune {
  width: 48px;
  height: 48px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
  border-radius: 50%;
  backdrop-filter: blur(14px);
  box-shadow: 0 6px 18px rgba(var(--v-theme-primary), 0.3);
}
.recommend-tune :deep(.v-icon) {
  font-size: 28px;
}
.recommend-scroll-top {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  padding: 0;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.05);
  border-radius: 50%;
  background: rgba(var(--v-theme-background), 0.8);
  color: rgb(var(--v-theme-on-surface));
  box-shadow: 0 5px 15px rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(10px);
  transition: all 0.3s ease;
}
.recommend-scroll-top:hover {
  background: rgba(var(--v-theme-background), 0.95);
  color: rgb(var(--v-theme-primary));
  transform: translateY(-4px);
}
@media (max-width: 768px) {
  .recommend-fab-stack {
    bottom: max(0.875rem, calc(env(safe-area-inset-bottom) + 0.875rem));
    right: max(0.875rem, calc(env(safe-area-inset-right) + 0.875rem));
  }
  .recommend-tune {
    width: 56px;
    height: 56px;
  }
}
</style>
