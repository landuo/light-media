<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VTabs, VTab } from 'vuetify/components/VTabs';
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
const loading = ref(true),
  saving = ref(false),
  error = ref(''),
  warning = ref('');
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
const saveDisabled = computed(
  () =>
    !loaded.value ||
    loading.value ||
    saving.value ||
    editingIndex.value !== null ||
    (mode.value === 'text'
      ? !parsed.value.repos.length || !!parsed.value.invalid.length
      : !repos.value.length || !!newRepo.value.trim()),
);
function switchMode(value: unknown) {
  if (
    (value !== 'list' && value !== 'text') ||
    value === mode.value ||
    saving.value ||
    !loaded.value ||
    loading.value ||
    editingIndex.value !== null
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
  if (loading.value || saving.value) return;
  const value = newRepo.value.trim();
  if (!validate(value)) return;
  repos.value.push(value);
  newRepo.value = '';
}
function startEdit(index: number) {
  if (loading.value || saving.value) return;
  editingIndex.value = index;
  editingUrl.value = repos.value[index] || '';
}
function cancelEdit() {
  editingIndex.value = null;
  editingUrl.value = '';
}
function saveEdit() {
  if (saving.value || editingIndex.value === null) return;
  const value = editingUrl.value.trim();
  if (!validate(value, editingIndex.value)) return;
  repos.value[editingIndex.value] = value;
  cancelEdit();
}
function removeRepo(index: number) {
  if (saving.value || editingIndex.value !== null) return;
  repos.value.splice(index, 1);
}
function moveRepo(index: number, offset: number) {
  if (
    saving.value ||
    editingIndex.value !== null ||
    index + offset < 0 ||
    index + offset >= repos.value.length
  )
    return;
  const value = repos.value.splice(index, 1)[0];
  if (value) repos.value.splice(index + offset, 0, value);
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
    repos.value = parseRepos(result.plugin_markets.join('\n')).repos;
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
    const values = mode.value === 'text' ? parsed.value.repos : [...repos.value];
    const result = await api('/plugins/markets', { plugin_markets: values }, 'PUT');
    if (result?.success !== true) throw new Error(result?.message || '仓库设置保存失败');
    toast('插件仓库设置已保存；兼容性以发现结果为准');
    emit('saved');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <VDialog
    :model-value="true"
    max-width="760"
    :fullscreen="smAndDown"
    :persistent="saving"
    content-class="plugin-dialog-surface"
    @update:model-value="!saving && emit('close')"
  >
    <VCard title="插件市场设置">
      <PluginDialogCloseBtn
        aria-label="关闭插件市场设置"
        :disabled="saving"
        @click="!saving && emit('close')"
      />
      <VCardText class="plugin-dialog-content">
        <p>
          按顺序填写 Go schema1 单插件索引 URL。保存地址不会安装或执行插件；Python 仓库、GitHub
          项目页面及任意源码仓库不能直接安装。
        </p>
        <p v-if="loading" role="status">正在加载仓库设置…</p>
        <p v-if="error" role="alert" class="error">{{ error }}</p>
        <p v-if="warning" role="status">{{ warning }}</p>
        <VBtn
          v-if="error && !repos.length"
          variant="text"
          :disabled="loading || saving"
          @click="load"
          >重新读取设置</VBtn
        >
        <VTabs
          :model-value="mode"
          :disabled="loading || saving || editingIndex !== null"
          @update:model-value="switchMode"
        >
          <VTab value="list">列表模式</VTab><VTab value="text">文本模式</VTab>
        </VTabs>
        <template v-if="mode === 'list'">
          <Draggable
            v-model="repos"
            :item-key="(repo: string) => repo"
            handle=".repo-drag"
            :disabled="loading || saving || editingIndex !== null"
            class="repo-list"
          >
            <template #item="{ element, index }">
              <div class="repo-row">
                <template v-if="editingIndex === index">
                  <VTextField
                    v-model="editingUrl"
                    label="编辑仓库地址"
                    :disabled="saving"
                    @keyup.enter="saveEdit"
                  />
                  <VBtn :disabled="saving" variant="text" @click="saveEdit">确定</VBtn
                  ><VBtn :disabled="saving" variant="text" @click="cancelEdit">取消</VBtn>
                </template>
                <template v-else>
                  <VIcon icon="mdi-drag" class="repo-drag" aria-hidden="true" />
                  <span class="repo-url">{{ element }}</span>
                  <div class="repo-actions">
                    <VBtn
                      icon="mdi-arrow-up"
                      size="small"
                      variant="text"
                      aria-label="上移仓库"
                      :disabled="saving || index === 0 || editingIndex !== null"
                      @click="moveRepo(index, -1)"
                    />
                    <VBtn
                      icon="mdi-arrow-down"
                      size="small"
                      variant="text"
                      aria-label="下移仓库"
                      :disabled="saving || index === repos.length - 1 || editingIndex !== null"
                      @click="moveRepo(index, 1)"
                    />
                    <VBtn
                      icon="mdi-pencil"
                      size="small"
                      variant="text"
                      aria-label="编辑仓库"
                      :disabled="saving || editingIndex !== null"
                      @click="startEdit(index)"
                    />
                    <VBtn
                      icon="mdi-delete"
                      size="small"
                      variant="text"
                      aria-label="移除仓库"
                      :disabled="saving || editingIndex !== null"
                      @click="removeRepo(index)"
                    />
                  </div>
                </template>
              </div>
            </template>
          </Draggable>
          <div class="repo-row">
            <VTextField
              v-model="newRepo"
              label="Go schema1 索引 URL"
              placeholder="https://example.com/plugin/index.json"
              :disabled="loading || saving || editingIndex !== null"
              @keyup.enter="addRepo"
            />
            <VBtn
              variant="tonal"
              :disabled="loading || saving || !newRepo.trim() || editingIndex !== null"
              @click="addRepo"
              >添加</VBtn
            >
          </div>
        </template>
        <template v-else>
          <VTextarea
            v-model="text"
            label="仓库地址，每行一个或以逗号分隔"
            rows="8"
            :disabled="loading || saving"
          />
          <p v-if="parsed.invalid.length" role="alert" class="error">
            {{ parsed.invalid.length }} 个无效地址，请修改后保存。
          </p>
          <p v-if="parsed.duplicates.length" role="status">
            {{ parsed.duplicates.length }} 个重复地址将按首次出现顺序去除。
          </p>
        </template>
      </VCardText>
      <VCardActions class="repo-footer"
        ><VBtn :disabled="saving" @click="!saving && emit('close')">取消</VBtn
        ><VBtn
          color="primary"
          variant="flat"
          :loading="saving"
          :disabled="saveDisabled"
          @click="save"
          >保存</VBtn
        ></VCardActions
      >
    </VCard>
  </VDialog>
</template>
<style scoped>
.repo-list {
  margin: 16px 0;
}
.repo-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-block: 8px;
  flex-wrap: wrap;
}
.repo-row :deep(.v-input) {
  flex: 1;
  min-width: min(100%, 240px);
}
.repo-url {
  flex: 1;
  min-width: 120px;
  overflow-wrap: anywhere;
}
.repo-actions {
  display: flex;
}
.repo-drag {
  cursor: grab;
}
.repo-footer {
  justify-content: flex-end;
  padding: 16px 24px;
}
</style>
