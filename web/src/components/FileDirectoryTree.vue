<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue';
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
let generation = 0;
let alive = true;
const invalidated = new Set<string>();
const requests = new Map<string, { controller: AbortController; promise: Promise<void> }>();
function cancelReads() {
  generation++;
  for (const request of requests.values()) request.controller.abort();
  requests.clear();
  pending.value = [];
}
function read(path: string): Promise<void> {
  if (
    !alive ||
    (Object.prototype.hasOwnProperty.call(children.value, path) && !invalidated.has(path))
  )
    return Promise.resolve();
  const existing = requests.get(path);
  if (existing) return existing.promise;
  const run = generation,
    controller = new AbortController();
  const request = { controller, promise: Promise.resolve() };
  requests.set(path, request);
  pending.value.push(path);
  delete errors.value[path];
  request.promise = (async () => {
    try {
      const root = browserRootForPath(props.roots, path);
      const items = root
        ? (
            await api(
              '/files/list?' +
                new URLSearchParams({ root_id: root.id, path, sort: 'name', order: 'asc' }),
              undefined,
              'GET',
              controller.signal,
            )
          ).items || []
        : virtualBrowserChildren(props.roots, path);
      if (!alive || run !== generation || controller.signal.aborted) return;
      const directories = items.filter((item: BrowserItem) => item.type === 'dir');
      const retained = new Set(directories.map((item: BrowserItem) => item.path));
      for (const old of children.value[path] || []) {
        if (retained.has(old.path)) continue;
        for (const key of Object.keys(children.value))
          if (key === old.path || key.startsWith(old.path + '/')) delete children.value[key];
      }
      children.value[path] = directories;
      invalidated.delete(path);
    } catch (e) {
      if (alive && run === generation && !controller.signal.aborted)
        errors.value[path] = errorText(e);
    } finally {
      if (requests.get(path) === request) {
        requests.delete(path);
        pending.value = pending.value.filter((value) => value !== path);
      }
    }
  })();
  return request.promise;
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
  const run = generation;
  const parts = props.currentPath.split('/').filter(Boolean);
  await read('/');
  for (let i = 1; i < parts.length; i++) {
    if (!alive || run !== generation) return;
    const path = '/' + parts.slice(0, i).join('/');
    if (!expanded.value.includes(path)) expanded.value.push(path);
    await read(path);
  }
}
watch(
  () => [props.currentPath, props.refreshKey, props.roots] as const,
  async (value, previous) => {
    cancelReads();
    const run = generation;
    if (previous && value[2] !== previous[2]) {
      children.value = {};
      invalidated.clear();
    }
    const refresh = previous && value[1] !== previous[1];
    if (refresh) {
      const path = props.currentPath;
      invalidated.add(path);
      invalidated.add(path.substring(0, path.lastIndexOf('/')) || '/');
    }
    await exposeCurrent();
    if (refresh && alive && run === generation && expanded.value.includes(props.currentPath))
      await read(props.currentPath);
  },
  { immediate: true },
);
onUnmounted(() => {
  alive = false;
  cancelReads();
});
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
  width: 240px;
  flex: none;
  border-right: 1px solid var(--line);
  padding: 8px 0;
  overflow: auto;
}
.file-tree button {
  border: 0;
  background: transparent;
  padding: 4px 8px;
  box-shadow: none;
}
.tree-local {
  width: 100%;
  justify-content: flex-start;
  gap: 9px;
  color: var(--accent);
  min-height: 36px;
  font-size: 14px;
}
.tree-row {
  display: flex;
  align-items: center;
  min-height: 32px;
  font-size: 14px;
}
.tree-row:hover {
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
}
.tree-row.active {
  background: var(--accent-soft);
  color: var(--accent);
}
.tree-expand {
  width: 24px;
  flex: none;
  padding: 4px 3px !important;
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
  width: 20px;
  height: 20px;
  color: #ffb300;
}
.tree-row.active .tree-name svg {
  color: var(--accent);
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
