<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { VDivider } from 'vuetify/components/VDivider';
import { VSpacer } from 'vuetify/components/VGrid';
import { VTooltip } from 'vuetify/components/VTooltip';
import { VAlert } from 'vuetify/components/VAlert';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VTextField } from 'vuetify/components/VTextField';
import { VTextarea } from 'vuetify/components/VTextarea';
import Draggable from 'vuedraggable';
import { api, errorText, toast } from '../api';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';

const emit = defineEmits<{ close: []; saved: [] }>();
const { smAndDown } = useDisplay();
const mode = ref<'list' | 'text'>('list');
const repos = ref<string[]>([]),
  text = ref(''),
  newRepo = ref('');
const editingIndex = ref<number | null>(null),
  editingUrl = ref('');
const loaded = ref(false);
const marketsLocked = ref(false),
  lockedSources = ref<string[]>([]);
const loading = ref(true),
  saving = ref(false),
  error = ref(''),
  warning = ref('');
const syncingWiki = ref(false);
// The original editor accepts HTTP(S) text; the Go backend additionally rejects
// credentials/fragments. Syntax acceptance is not repository compatibility.
function validURL(value: string) {
  try {
    const url = new URL(value);
    return /^https?:\/\//i.test(value) && !!url.host && !url.username && !url.password && !url.hash;
  } catch {
    return false;
  }
}
function parseRepos(value: string) {
  const valid: string[] = [],
    invalid: string[] = [],
    duplicates: string[] = [];
  const seen = new Set<string>();
  for (const entry of value
    .split(/[\n,，]+/)
    .map((item) => item.trim())
    .filter(Boolean)) {
    if (!validURL(entry)) invalid.push(entry);
    else if (seen.has(entry)) duplicates.push(entry);
    else {
      seen.add(entry);
      valid.push(entry);
    }
  }
  return { repos: valid, invalid, duplicates: [...new Set(duplicates)] };
}
const parsed = computed(() => parseRepos(text.value));
const repoCount = computed(() =>
  marketsLocked.value
    ? lockedSources.value.length
    : mode.value === 'text'
      ? parsed.value.repos.length
      : repos.value.length,
);
const saveDisabled = computed(
  () =>
    !loaded.value ||
    loading.value ||
    saving.value ||
    repoCount.value === 0 ||
    (mode.value === 'text' && !!parsed.value.invalid.length),
);
function formatRepoDisplay(value: string) {
  try {
    const url = new URL(value),
      parts = url.pathname.split('/').filter(Boolean);
    if (
      ['github.com', 'www.github.com', 'raw.githubusercontent.com'].includes(url.hostname) &&
      parts.length >= 2
    )
      return parts[0] + '/' + parts[1]!.replace(/\.git$/, '');
  } catch {
    /* Show the entered URL when it has no GitHub repository name. */
  }
  return value;
}

function switchMode(value: unknown) {
  if (
    (value !== 'list' && value !== 'text') ||
    value === mode.value ||
    saving.value ||
    marketsLocked.value ||
    !loaded.value ||
    loading.value
  )
    return;
  warning.value = '';
  if (mode.value === 'text') {
    const result = parsed.value;
    repos.value = result.repos;
    if (result.invalid.length) warning.value = `已忽略 ${result.invalid.length} 个无效地址`;
    if (result.duplicates.length)
      warning.value += `${warning.value ? '；' : ''}已去除 ${result.duplicates.length} 个重复地址`;
  }
  text.value = repos.value.join('\n');
  mode.value = value;
}
function validate(value: string, index: number | null = null) {
  if (!validURL(value)) {
    error.value = '请输入不含账号密码或片段的 HTTP(S) 地址';
    return false;
  }
  if (repos.value.some((repo, n) => repo === value && n !== index)) {
    error.value = '仓库地址已存在';
    return false;
  }
  error.value = '';
  return true;
}
function addRepo() {
  if (marketsLocked.value || loading.value || saving.value) return;
  const value = newRepo.value.trim();
  if (!validate(value)) return;
  repos.value.push(value);
  newRepo.value = '';
}
function startEdit(index: number) {
  if (marketsLocked.value || loading.value || saving.value) return;
  editingIndex.value = index;
  editingUrl.value = repos.value[index] || '';
}
function cancelEdit() {
  editingIndex.value = null;
  editingUrl.value = '';
}
function saveEdit() {
  if (marketsLocked.value || saving.value || editingIndex.value === null) return;
  const value = editingUrl.value.trim();
  if (!validate(value, editingIndex.value)) return;
  repos.value[editingIndex.value] = value;
  cancelEdit();
}
function removeRepo(index: number) {
  if (marketsLocked.value || saving.value) return;
  repos.value.splice(index, 1);
}

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const result = await api('/plugins/markets');
    if (
      !Array.isArray(result?.plugin_markets) ||
      !result.plugin_markets.every((v: unknown) => typeof v === 'string')
    )
      throw new Error('仓库设置响应无效');
    marketsLocked.value = result.plugin_markets_locked === true;
    lockedSources.value = [...result.plugin_markets];
    repos.value = marketsLocked.value
      ? [...lockedSources.value]
      : parseRepos(result.plugin_markets.join('\n')).repos;
    text.value = repos.value.join('\n');
    loaded.value = true;
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function save() {
  if (saveDisabled.value) return;
  saving.value = true;
  error.value = '';
  try {
    const values = marketsLocked.value
      ? [...lockedSources.value]
      : mode.value === 'text'
        ? parsed.value.repos
        : [...repos.value];
    const result = await api(
      '/plugins/markets',
      {
        plugin_markets: values,
      },
      'PUT',
    );
    if (result?.success !== true) throw new Error(result?.message || '仓库设置保存失败');
    toast('插件仓库设置已保存；兼容性以发现结果为准');
    emit('saved');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    saving.value = false;
  }
}
async function syncWiki() {
  if (marketsLocked.value || !loaded.value || loading.value || saving.value) return;
  saving.value = true;
  syncingWiki.value = true;
  error.value = '';
  try {
    const result = await api('/plugins/markets/sync-wiki', {});
    if (result?.success !== true) throw new Error(result?.message || 'Wiki 同步失败');
    if (
      !Array.isArray(result.repos) ||
      !result.repos.every((value: unknown) => typeof value === 'string' && validURL(value))
    )
      throw new Error('Wiki 同步响应无效，请重新读取已保存设置');
    repos.value = result.repos;
    text.value = repos.value.join('\n');
    newRepo.value = '';
    warning.value = result.message || '仓库清单已保存；兼容性以发现结果为准';
    toast('已从 Wiki 同步并保存仓库清单');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    syncingWiki.value = false;
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <VDialog
    :model-value="true"
    width="56rem"
    :fullscreen="smAndDown"
    :persistent="saving"
    content-class="plugin-dialog-surface"
    @update:model-value="!saving && emit('close')"
  >
    <VCard class="plugin-market-dialog-card">
      <VCardItem class="plugin-market-card-item">
        <div class="plugin-market-header">
          <VCardTitle class="plugin-market-title d-flex align-center pa-0"
            ><VIcon icon="mdi-store-cog" class="me-2" />插件市场设置</VCardTitle
          >
        </div>
        <PluginDialogCloseBtn
          aria-label="关闭插件市场设置"
          :disabled="saving"
          @click="!saving && emit('close')"
        />
      </VCardItem>
      <VDivider />
      <VCardText class="plugin-market-dialog-body pt-4">
        <p v-if="loading" role="status">正在加载仓库设置…</p>
        <VAlert v-if="error" type="error" variant="tonal" density="compact" role="alert"
          >{{ error
          }}<VBtn v-if="!loaded" variant="text" :disabled="loading" @click="load"
            >重新读取设置</VBtn
          ></VAlert
        >
        <VAlert v-if="warning" type="warning" variant="tonal" density="compact">{{
          warning
        }}</VAlert>
        <p v-if="marketsLocked" role="status">仓库由启动环境设置，修改后需重启</p>
        <div class="plugin-market-toolbar">
          <div class="plugin-market-toolbar-hint">
            <VIcon icon="mdi-information-outline" size="18" /><span
              >当前已维护 {{ repoCount }} 个插件仓库地址</span
            >
          </div>
          <div class="plugin-market-mode-switch" role="tablist" aria-label="插件市场设置">
            <VTooltip text="列表维护" location="top"
              ><template #activator="{ props }"
                ><button
                  v-bind="props"
                  type="button"
                  class="plugin-market-mode-button"
                  :class="{ 'is-active': mode === 'list' }"
                  role="tab"
                  aria-label="列表维护"
                  :aria-selected="mode === 'list'"
                  :disabled="marketsLocked || loading || saving"
                  @click="switchMode('list')"
                >
                  <VIcon icon="mdi-format-list-bulleted" size="20" /></button></template
            ></VTooltip>
            <VTooltip text="文本维护" location="top"
              ><template #activator="{ props }"
                ><button
                  v-bind="props"
                  type="button"
                  class="plugin-market-mode-button"
                  :class="{ 'is-active': mode === 'text' }"
                  role="tab"
                  aria-label="文本维护"
                  :aria-selected="mode === 'text'"
                  :disabled="marketsLocked || loading || saving"
                  @click="switchMode('text')"
                >
                  <VIcon icon="mdi-text-box-edit-outline" size="20" /></button></template
            ></VTooltip>
          </div>
        </div>
        <div v-if="mode === 'list'" class="plugin-market-list-panel">
          <div class="plugin-market-input">
            <VTextField
              v-model="newRepo"
              label="插件仓库地址"
              placeholder="输入插件仓库地址"
              density="compact"
              prepend-inner-icon="mdi-link-plus"
              clearable
              hide-details
              :disabled="marketsLocked || loading || saving"
              @keyup.enter="addRepo"
              ><template #append
                ><VBtn
                  icon="mdi-plus"
                  variant="tonal"
                  color="primary"
                  aria-label="添加仓库"
                  :disabled="marketsLocked || loading || saving"
                  @click="addRepo" /></template
            ></VTextField>
          </div>
          <div class="plugin-market-list-wrap">
            <VList v-if="repos.length" class="plugin-market-repo-list px-0">
              <Draggable
                v-model="repos"
                :item-key="(repo: string) => repo"
                handle=".drag-handle"
                animation="200"
                :disabled="marketsLocked || loading || saving || editingIndex !== null"
                class="repo-list"
              >
                <template #item="{ element, index }"
                  ><div>
                    <VListItem class="plugin-market-repo-item py-3">
                      <template #prepend
                        ><VBtn
                          icon="mdi-drag-vertical"
                          size="small"
                          variant="text"
                          color="primary"
                          class="drag-handle me-2"
                          aria-label="拖动仓库"
                          :disabled="marketsLocked || loading || saving || editingIndex !== null"
                      /></template>
                      <template v-if="editingIndex !== index"
                        ><VListItemTitle
                          ><div class="plugin-market-repo-title">
                            <span class="plugin-market-repo-index">{{ index + 1 }}</span
                            ><span class="plugin-market-repo-name" :title="element">{{
                              formatRepoDisplay(element)
                            }}</span>
                          </div></VListItemTitle
                        ><VListItemSubtitle class="plugin-market-repo-url mt-1" :title="element">{{
                          element
                        }}</VListItemSubtitle></template
                      >
                      <VTextField
                        v-else
                        v-model="editingUrl"
                        label="编辑仓库地址"
                        density="compact"
                        variant="outlined"
                        hide-details
                        autofocus
                        :disabled="saving"
                        @keyup.enter="saveEdit"
                        @keyup.esc="cancelEdit"
                      />
                      <template #append
                        ><div v-if="editingIndex !== index" class="d-flex align-center">
                          <VBtn
                            icon="mdi-pencil"
                            size="small"
                            variant="text"
                            aria-label="编辑仓库"
                            :disabled="marketsLocked || loading || saving"
                            @click="startEdit(index)"
                          /><VBtn
                            icon="mdi-delete"
                            size="small"
                            variant="text"
                            color="error"
                            aria-label="移除仓库"
                            :disabled="marketsLocked || loading || saving"
                            @click="removeRepo(index)"
                          />
                        </div>
                        <VBtn
                          v-else
                          icon="mdi-check"
                          size="small"
                          variant="text"
                          color="success"
                          aria-label="确定仓库编辑"
                          :disabled="saving"
                          @click.stop="saveEdit"
                      /></template> </VListItem
                    ><VDivider v-if="index < repos.length - 1" class="mx-4" /></div
                ></template>
              </Draggable>
            </VList>
            <div v-else class="plugin-market-empty text-center text-medium-emphasis">
              <VIcon icon="mdi-source-repository-multiple" size="48" class="mb-2" />
              <div>暂无插件仓库地址</div>
            </div>
          </div>
        </div>
        <div v-else class="plugin-market-text-panel">
          <VTextarea
            v-model="text"
            class="plugin-market-textarea-field"
            label="插件仓库地址"
            rows="12"
            prepend-inner-icon="mdi-text-box-edit-outline"
            :disabled="marketsLocked || loading || saving"
            hide-details
          />
          <div class="plugin-market-text-hint">
            直接粘贴仓库地址串，一行一个或使用英文逗号分隔。
          </div>
          <VAlert
            v-if="parsed.invalid.length"
            type="error"
            variant="tonal"
            density="compact"
            role="alert"
            ><div>文本中有 {{ parsed.invalid.length }} 个无效地址，请修正后保存。</div>
            <div class="text-truncate">{{ parsed.invalid.slice(0, 3).join(', ') }}</div></VAlert
          >
          <VAlert
            v-else-if="parsed.duplicates.length"
            type="warning"
            variant="tonal"
            density="compact"
            >重复地址会在保存时自动去重。</VAlert
          >
        </div>
      </VCardText>
      <VCardActions class="app-dialog-actions"
        ><VBtn
          color="success"
          variant="tonal"
          prepend-icon="mdi-cloud-sync-outline"
          :loading="syncingWiki"
          :disabled="marketsLocked || !loaded || loading || saving"
          @click="syncWiki"
          >同步 Wiki</VBtn
        ><VSpacer /><VBtn
          color="primary"
          variant="flat"
          prepend-icon="mdi-content-save-check"
          class="px-5"
          :disabled="saveDisabled"
          :loading="saving"
          @click="save"
          >保存</VBtn
        ></VCardActions
      >
    </VCard>
  </VDialog>
</template>
<style scoped>
.plugin-market-dialog-card {
  display: flex;
  flex-direction: column;
  block-size: min(82vh, 50rem);
}

.plugin-market-card-item {
  flex: 0 0 auto;
  padding-block: 0.875rem;
}

.plugin-market-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding-inline-end: 2rem;
}

.plugin-market-title {
  min-inline-size: 0;
}

.plugin-market-dialog-body {
  display: flex;
  overflow: hidden;
  flex: 1;
  flex-direction: column;
  gap: 0.875rem;
  min-block-size: 0;
  padding-block: 0.875rem !important;
}

.plugin-market-toolbar {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  min-block-size: 2.25rem;
}

.plugin-market-toolbar-hint {
  display: flex;
  align-items: center;
  border-radius: 0.375rem;
  background: rgba(var(--v-theme-info), 0.08);
  color: rgb(var(--v-theme-info));
  font-size: 0.875rem;
  gap: 0.5rem;
  min-inline-size: 0;
  padding-block: 0.5rem;
  padding-inline: 1rem;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.plugin-market-mode-switch {
  display: inline-flex;
  padding: 0.125rem;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
  border-radius: 0.375rem;
  background: rgba(var(--v-theme-surface), 0.72);
  gap: 0.125rem;
}

.plugin-market-mode-button {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 0.375rem;
  background: transparent;
  block-size: 2.25rem;
  color: rgba(var(--v-theme-on-surface), 0.68);
  cursor: pointer;
  font: inherit;
  inline-size: 2.25rem;
  transition:
    background-color 0.16s ease,
    color 0.16s ease;

  &:hover {
    background: rgba(var(--v-theme-primary), 0.07);
    color: rgb(var(--v-theme-on-surface));
  }

  &:focus-visible {
    outline: 2px solid rgba(var(--v-theme-primary), 0.48);
    outline-offset: 2px;
  }

  &.is-active {
    background: rgba(var(--v-theme-primary), 0.12);
    color: rgb(var(--v-theme-primary));
  }
}

.plugin-market-list-panel,
.plugin-market-text-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 0.5rem;
  min-block-size: 0;
}

.plugin-market-input {
  flex-shrink: 0;
}

.plugin-market-list-wrap {
  flex: 1;
  background: rgba(var(--v-theme-surface), 0.72);
  min-block-size: 0;
  overflow-y: auto;
}

.plugin-market-repo-list {
  background: transparent;
}

.plugin-market-repo-item {
  min-block-size: 4.5rem;
}

.plugin-market-repo-title {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-inline-size: 0;
}

.plugin-market-repo-name,
.plugin-market-repo-url {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  line-break: anywhere;
  -webkit-line-clamp: 2;
  overflow-wrap: anywhere;
  white-space: normal;
  word-break: break-word;
}

.plugin-market-repo-url {
  line-height: 1.4;
}

.plugin-market-repo-index {
  flex: 0 0 auto;
  color: rgba(var(--v-theme-on-surface), 0.48);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
  inline-size: 1.75rem;
}

.plugin-market-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-block-size: 14rem;
}

.plugin-market-textarea-field {
  flex: 1;
  min-block-size: 0;
  align-self: stretch;
}
.plugin-market-textarea-field :deep(.v-input__control),
.plugin-market-textarea-field :deep(.v-field) {
  min-block-size: 0;
  block-size: 100%;
}
.plugin-market-textarea-field :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace;
  line-height: 1.6;
}
.plugin-market-text-hint {
  flex: 0 0 auto;
  color: rgba(var(--v-theme-on-surface), 0.62);
  font-size: 0.8125rem;
  line-height: 1.4;
  padding-inline: 1rem;
}

.plugin-market-invalid-alert {
  :deep(.v-alert__content) {
    min-inline-size: 0;
  }
}

@media (width <= 600px) {
  .plugin-market-dialog-card {
    block-size: 100dvh;
  }

  .plugin-market-card-item {
    padding-block: 0.75rem 0.625rem;
    padding-inline: 1rem;
  }

  .plugin-market-header {
    align-items: center;
    gap: 0.5rem;
    padding-inline-end: 2.25rem;
  }

  .plugin-market-header :deep(.v-card-title) {
    font-size: 1.125rem;
    line-height: 1.35;
  }

  .plugin-market-dialog-body {
    gap: 0.625rem;
    padding-block: 0.75rem !important;
    padding-inline: 1rem !important;
  }

  .plugin-market-toolbar {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
  }

  .plugin-market-mode-switch {
    flex: 0 0 auto;
  }

  .plugin-market-toolbar-hint {
    flex: 1 1 auto;
  }

  .plugin-market-list-panel,
  .plugin-market-text-panel {
    gap: 0.625rem;
  }

  .plugin-market-list-wrap {
    min-block-size: 0;
  }

  .plugin-market-empty {
    min-block-size: 10rem;
  }
}
</style>
