<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { api, errorText } from '../api';
import {
  browserRootForPath,
  virtualBrowserChildren,
  type BrowserRoot,
  type BrowserItem,
} from '../features/file-browser';
import FileManagerIcon from './FileManagerIcon.vue';
const props = defineProps<{ roots: BrowserRoot[]; currentPath: string; refreshKey?: number }>();
const emit = defineEmits<{ navigate: [path: string] }>();
const expanded = ref<string[]>(['/']);
const children = ref<Record<string, BrowserItem[]>>({});
const pending = ref<string[]>([]);
const errors = ref<Record<string, string>>({});
const rows = computed(() => {
  const output: (BrowserItem & { depth: number })[] = [];
  const visit = (path: string, depth: number) => {
    for (const item of children.value[path] || []) {
      output.push({ ...item, depth });
      if (expanded.value.includes(item.path)) visit(item.path, depth + 1);
    }
  };
  visit('/', 0);
  return output;
});
async function read(path: string) {
  if (pending.value.includes(path)) return;
  pending.value.push(path);
  delete errors.value[path];
  try {
    const root = browserRootForPath(props.roots, path);
    children.value[path] = root
      ? (
          (
            await api(
              '/files/list?' +
                new URLSearchParams({ root_id: root.id, path, sort: 'name', order: 'asc' }),
            )
          ).items || []
        ).filter((item: BrowserItem) => item.type === 'dir')
      : virtualBrowserChildren(props.roots, path);
  } catch (e) {
    errors.value[path] = errorText(e);
  } finally {
    pending.value = pending.value.filter((value) => value !== path);
  }
}
async function toggle(path: string) {
  if (expanded.value.includes(path))
    expanded.value = expanded.value.filter((value) => value !== path);
  else {
    expanded.value.push(path);
    await read(path);
  }
}
async function exposeCurrent() {
  await read('/');
  const parts = props.currentPath.split('/').filter(Boolean);
  for (let i = 1; i < parts.length; i++) {
    const path = '/' + parts.slice(0, i).join('/');
    if (!expanded.value.includes(path)) expanded.value.push(path);
    await read(path);
  }
}
watch(
  () => [props.currentPath, props.refreshKey, props.roots] as const,
  () => void exposeCurrent(),
  { immediate: true },
);
</script>
<template>
  <aside class="file-tree" aria-label="目录树">
    <button class="tree-local" @click="emit('navigate', '/')">
      <FileManagerIcon name="local" />本地
    </button>
    <div
      v-for="row in rows"
      :key="row.path"
      class="tree-row"
      :class="{ active: currentPath === row.path }"
      :style="{ paddingLeft: 9 + row.depth * 15 + 'px' }"
    >
      <button
        class="tree-expand"
        :aria-label="(expanded.includes(row.path) ? '折叠 ' : '展开 ') + row.name"
        :disabled="pending.includes(row.path)"
        @click="toggle(row.path)"
      >
        {{ expanded.includes(row.path) ? '⌄' : '›' }}
      </button>
      <button class="tree-name" :title="row.path" @click="emit('navigate', row.path)">
        <FileManagerIcon name="folder" />{{ row.name }}
      </button>
      <span v-if="errors[row.path]" class="tree-error" :title="errors[row.path]">!</span>
    </div>
  </aside>
</template>
<style scoped>
.file-tree {
  width: 250px;
  flex: none;
  border-right: 1px solid var(--line);
  padding: 8px 0;
  overflow: auto;
}
.file-tree button {
  border: 0;
  background: transparent;
  padding: 8px;
  box-shadow: none;
}
.tree-local {
  width: 100%;
  justify-content: flex-start;
  gap: 9px;
}
.tree-row {
  display: flex;
  align-items: center;
}
.tree-row.active {
  background: var(--accent-soft);
  color: var(--accent);
}
.tree-expand {
  width: 24px;
  flex: none;
  padding: 7px 3px !important;
}
.tree-name {
  min-width: 0;
  flex: 1;
  justify-content: flex-start;
  overflow: hidden;
  text-overflow: ellipsis;
  gap: 8px;
  white-space: nowrap;
}
.tree-name svg {
  width: 18px;
  height: 18px;
}
.tree-error {
  color: var(--danger);
  padding: 4px;
}
@media (max-width: 760px) {
  .file-tree {
    display: none;
  }
}
</style>
