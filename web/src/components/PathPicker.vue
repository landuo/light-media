<script setup lang="ts">
import { computed, ref, useId } from 'vue';
import { VTextField } from 'vuetify/components/VTextField';
import { api, errorText } from '../api';
import FileManagerIcon from './FileManagerIcon.vue';
const props = defineProps<{
  modelValue: string;
  label: string;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
}>();
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
const inputID = 'path-picker-' + useId();
const opened = ref(false),
  loading = ref(false),
  error = ref(''),
  roots = ref<any[]>([]),
  activeRoot = ref<any>(null),
  listing = ref<any>(null);
let generation = 0;
const folders = computed(() =>
  (listing.value?.items || []).filter((item: any) => item.type === 'dir'),
);
const crumbs = computed(() => {
  if (!activeRoot.value || !listing.value) return [];
  const base = activeRoot.value.path;
  const parts = listing.value.path.slice(base.length).split('/').filter(Boolean);
  return [
    { name: base, path: base },
    ...parts.map((name: string, index: number) => ({
      name,
      path: base.replace(/\/$/, '') + '/' + parts.slice(0, index + 1).join('/'),
    })),
  ];
});
async function load(root: any, path: string) {
  const run = ++generation;
  loading.value = true;
  error.value = '';
  activeRoot.value = root;
  try {
    const result = await api(
      '/files/list?' + new URLSearchParams({ root_id: root.id, path, sort: 'name', order: 'asc' }),
    );
    if (run === generation) listing.value = result;
  } catch (e) {
    if (run === generation) error.value = errorText(e);
  } finally {
    if (run === generation) loading.value = false;
  }
}
function top() {
  generation++;
  activeRoot.value = null;
  listing.value = null;
  loading.value = false;
  error.value = '';
}
async function open() {
  opened.value = true;
  top();
  loading.value = true;
  const run = generation;
  try {
    const result = await api('/files/roots');
    if (run !== generation) return;
    roots.value = result || [];
    loading.value = false;
    const path = props.modelValue?.replace(/\/+$/, '') || '';
    const root = [...roots.value]
      .filter((item) => path === item.path || path.startsWith(item.path.replace(/\/$/, '') + '/'))
      .sort((a, b) => b.path.length - a.path.length)[0];
    if (root) await load(root, path);
  } catch (e) {
    error.value = errorText(e);
    loading.value = false;
  }
}
function close() {
  opened.value = false;
  generation++;
}
function choose() {
  if (listing.value?.path && !loading.value && !error.value) {
    emit('update:modelValue', listing.value.path);
    close();
  }
}
</script>
<template>
  <div class="path-picker">
    <VTextField
      :id="inputID"
      :model-value="modelValue"
      :label="label"
      :placeholder="placeholder"
      :required="required"
      :disabled="disabled"
      @update:model-value="emit('update:modelValue', $event ?? '')"
    >
      <template #append-inner>
        <button
          type="button"
          class="icon-button"
          :disabled="disabled"
          :aria-label="'浏览 ' + label"
          title="浏览目录"
          @click.stop="open"
        >
          <FileManagerIcon name="folder" />
        </button>
      </template>
    </VTextField>
    <Teleport to="body"
      ><div v-if="opened" class="modal-backdrop" @click.self="close">
        <section class="modal wide" role="dialog" aria-modal="true" :aria-label="'选择 ' + label">
          <header class="section-heading">
            <h2>选择目录</h2>
            <button type="button" class="icon-button" aria-label="关闭目录选择" @click="close">
              <FileManagerIcon name="close" />
            </button>
          </header>
          <nav class="path-crumbs" aria-label="目录路径">
            <button type="button" @click="top">/</button>
            <template v-for="crumb in crumbs" :key="crumb.path"
              ><span>›</span
              ><button type="button" @click="load(activeRoot, crumb.path)">
                {{ crumb.name }}
              </button></template
            >
          </nav>
          <p v-if="error" class="error-box">{{ error }}</p>
          <p v-if="loading" class="notice">读取中…</p>
          <div v-else class="folder-list">
            <template v-if="!activeRoot"
              ><button
                v-for="root in roots"
                :key="root.id"
                type="button"
                @click="load(root, root.path)"
              >
                <FileManagerIcon name="folder" />{{ root.path }}
              </button></template
            >
            <template v-else
              ><button
                v-for="folder in folders"
                :key="folder.path"
                type="button"
                @click="load(activeRoot, folder.path)"
              >
                <FileManagerIcon name="folder" />{{ folder.name }}
              </button>
              <p v-if="!error && !folders.length" class="notice">此目录没有子目录。</p></template
            >
          </div>
          <footer class="path-actions">
            <span>{{ listing?.path || '请选择目录' }}</span
            ><button type="button" @click="close">取消</button
            ><button
              type="button"
              class="primary"
              :disabled="!listing?.path || loading || !!error"
              @click="choose"
            >
              选择
            </button>
          </footer>
        </section>
      </div></Teleport
    >
  </div>
</template>
<style scoped>
.path-crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line);
}
.path-crumbs button {
  border: 0;
  padding: 5px 7px;
  background: transparent;
  overflow-wrap: anywhere;
}
.folder-list {
  min-height: 180px;
  max-height: 42vh;
  overflow: auto;
  padding: 10px 0;
}
.folder-list > button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: 10px 12px;
  text-align: left;
  overflow-wrap: anywhere;
}
.folder-list > button:hover {
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
}
.folder-list > button > svg {
  color: var(--muted);
  width: 24px;
  height: 24px;
}
.path-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-top: 14px;
  border-top: 1px solid var(--line);
}
.path-actions > span {
  flex: 1;
  font-size: 12px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
</style>
