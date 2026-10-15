<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from 'vue';
import { api, errorText } from '../api';
import {
  browserRootForPath,
  cleanBrowserPath,
  virtualBrowserChildren,
  type BrowserItem,
  type BrowserRoot,
} from '../features/file-browser';
import FileManagerIcon from './FileManagerIcon.vue';

const props = defineProps<{
  modelValue: string;
  suggestions?: string[];
  disabled?: boolean;
}>();
const emit = defineEmits<{ 'update:modelValue': [path: string] }>();
const opened = ref(false),
  loading = ref(false),
  error = ref('');
const roots = ref<BrowserRoot[]>([]);
const expanded = ref<string[]>(['/']);
const children = ref<Record<string, BrowserItem[]>>({});
const pending = ref<string[]>([]),
  errors = ref<Record<string, string>>({});
const position = ref({ top: '0px', left: '0px' });
const panel = ref<HTMLElement>();
let trigger: HTMLElement | undefined;
let generation = 0;
const rows = computed(() => {
  const result: (BrowserItem & { depth: number })[] = [];
  const visit = (path: string, depth: number) => {
    for (const item of children.value[path] || []) {
      result.push({ ...item, depth });
      if (expanded.value.includes(item.path)) visit(item.path, depth + 1);
    }
  };
  visit('/', 0);
  return result;
});
function selectable(path: string) {
  return !!browserRootForPath(roots.value, path);
}
async function read(path: string, current = generation) {
  if (pending.value.includes(path)) return;
  pending.value.push(path);
  delete errors.value[path];
  try {
    const root = browserRootForPath(roots.value, path);
    const items = root
      ? (
          await api(
            '/files/list?' +
              new URLSearchParams({ root_id: root.id, path, sort: 'name', order: 'asc' }),
          )
        ).items || []
      : virtualBrowserChildren(roots.value, path);
    if (current === generation)
      children.value[path] = items.filter((item: BrowserItem) => item.type === 'dir');
  } catch (e) {
    if (current === generation) errors.value[path] = errorText(e);
  } finally {
    if (current === generation) pending.value = pending.value.filter((item) => item !== path);
  }
}
async function load() {
  const current = ++generation;
  loading.value = true;
  error.value = '';
  expanded.value = ['/'];
  children.value = {};
  pending.value = [];
  errors.value = {};
  try {
    const result = await api<BrowserRoot[]>('/files/roots');
    if (current !== generation) return;
    roots.value = result;
    await read('/', current);
    const segments = cleanBrowserPath(props.modelValue).split('/').filter(Boolean);
    let parent = '/';
    // Reveal the current path only through the authorized tree; typing a new
    // path remains possible in the input and is validated on submission.
    for (let i = 1; i <= segments.length; i++) {
      if (current !== generation) return;
      const path = '/' + segments.slice(0, i).join('/');
      if (!children.value[parent]?.some((item) => item.path === path)) break;
      if (i < segments.length) {
        expanded.value.push(path);
        await read(path, current);
      }
      parent = path;
    }
  } catch (e) {
    if (current === generation) error.value = errorText(e);
  } finally {
    if (current === generation) loading.value = false;
  }
}
async function open(event: MouseEvent) {
  if (props.disabled) return;
  trigger = event.currentTarget as HTMLElement;
  const rect = trigger.getBoundingClientRect();
  const width = Math.min(520, window.innerWidth - 32);
  position.value = {
    left: Math.max(16, Math.min(rect.right - width, window.innerWidth - width - 16)) + 'px',
    top: Math.max(16, Math.min(rect.bottom + 6, window.innerHeight - 460)) + 'px',
  };
  opened.value = true;
  await nextTick();
  panel.value?.focus();
  await load();
}
function close() {
  opened.value = false;
  generation++;
  trigger?.focus();
}
function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    close();
    return;
  }
  if (event.key !== 'Tab') return;
  event.preventDefault();
  event.stopPropagation();
  const buttons = [
    ...(panel.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || []),
  ].filter((button) => button.getClientRects().length > 0);
  if (!buttons.length) {
    panel.value?.focus();
    return;
  }
  const current = buttons.indexOf(event.target as HTMLButtonElement);
  const next =
    current < 0
      ? event.shiftKey
        ? buttons.length - 1
        : 0
      : (current + (event.shiftKey ? -1 : 1) + buttons.length) % buttons.length;
  buttons[next]?.focus();
}
async function toggle(path: string) {
  if (expanded.value.includes(path)) {
    expanded.value = expanded.value.filter((item) => item !== path);
  } else {
    expanded.value.push(path);
    if (!children.value[path]) await read(path);
  }
}
async function choose(path: string) {
  if (selectable(path)) emit('update:modelValue', path);
  if (!expanded.value.includes(path)) await toggle(path);
}
onUnmounted(() => generation++);
</script>

<template>
  <button
    type="button"
    class="directory-trigger"
    aria-label="浏览目的目录"
    aria-haspopup="dialog"
    :aria-expanded="opened"
    :disabled="disabled"
    @click.stop="open"
  >
    <FileManagerIcon name="folder" />
  </button>
  <Teleport to="body">
    <div v-if="opened" class="directory-picker-backdrop" @click.self="close">
      <section
        ref="panel"
        class="directory-picker"
        :style="position"
        role="dialog"
        aria-modal="true"
        aria-label="选择目的目录"
        tabindex="-1"
        @keydown="handleKeydown"
      >
        <header>
          <strong>选择目的目录</strong
          ><button type="button" aria-label="关闭目录选择" @click="close">
            <FileManagerIcon name="close" />
          </button>
        </header>
        <p class="current-path" :title="modelValue || '自动'">
          <span>当前路径</span>{{ modelValue || '自动' }}
        </p>
        <div class="directory-shortcuts">
          <button
            type="button"
            :class="{ active: !modelValue }"
            @click="emit('update:modelValue', '')"
          >
            自动
          </button>
          <button
            v-for="path in suggestions || []"
            :key="path"
            type="button"
            :title="path"
            :class="{ active: modelValue === path }"
            @click="emit('update:modelValue', path)"
          >
            {{ path }}
          </button>
        </div>
        <div class="directory-scroll" aria-label="本地目录树">
          <p v-if="loading" class="directory-status" role="status">正在加载目录…</p>
          <p v-else-if="error" class="directory-error" role="alert">
            {{ error }}<button type="button" @click="load">重试</button>
          </p>
          <template v-else>
            <button
              type="button"
              class="directory-local"
              :class="{ active: modelValue === '/' }"
              @click="choose('/')"
            >
              <FileManagerIcon name="local" />本地 /
            </button>
            <p v-if="errors['/']" class="directory-error" role="alert">
              {{ errors['/'] }}<button type="button" @click="read('/')">重试</button>
            </p>
            <template v-for="row in rows" :key="row.path">
              <div
                class="directory-row"
                :class="{ active: cleanBrowserPath(modelValue) === row.path && !!modelValue }"
                :style="{ paddingLeft: 8 + row.depth * 18 + 'px' }"
              >
                <button
                  type="button"
                  class="directory-expand"
                  :aria-label="(expanded.includes(row.path) ? '折叠 ' : '展开 ') + row.name"
                  :aria-expanded="expanded.includes(row.path)"
                  :disabled="pending.includes(row.path)"
                  @click="toggle(row.path)"
                >
                  {{ pending.includes(row.path) ? '…' : expanded.includes(row.path) ? '⌄' : '›' }}
                </button>
                <button
                  type="button"
                  class="directory-name"
                  :title="row.path"
                  :aria-label="(selectable(row.path) ? '选择目录 ' : '浏览目录 ') + row.path"
                  @click="choose(row.path)"
                >
                  <FileManagerIcon name="folder" /><span>{{ row.name }}</span
                  ><span
                    v-if="modelValue && cleanBrowserPath(modelValue) === row.path"
                    class="selected-mark"
                    >✓</span
                  >
                </button>
              </div>
              <p v-if="errors[row.path]" class="directory-error" role="alert">
                {{ errors[row.path] }}<button type="button" @click="read(row.path)">重试</button>
              </p>
              <p
                v-else-if="expanded.includes(row.path) && children[row.path]?.length === 0"
                class="directory-empty"
                :style="{ paddingLeft: 54 + row.depth * 18 + 'px' }"
              >
                没有子目录
              </p>
            </template>
            <p v-if="!rows.length && !errors['/']" class="directory-status">没有可浏览的目录</p>
          </template>
        </div>
        <footer>点击目录即可填入路径</footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.directory-trigger {
  flex: none;
  border: 0;
  border-left: 1px solid var(--line);
  border-radius: 0 6px 6px 0;
  background: transparent;
  padding: 8px 12px;
  color: var(--muted);
}
.directory-picker-backdrop {
  position: fixed;
  inset: 0;
  z-index: 150;
}
.directory-picker {
  position: fixed;
  width: min(520px, calc(100vw - 32px));
  max-height: min(440px, calc(100dvh - 32px));
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 10px;
  box-shadow: 0 14px 48px #111b3633;
  overflow: hidden;
  outline: none;
}
.directory-picker header {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-bottom: 1px solid var(--line);
}
.directory-picker button {
  box-shadow: none;
}
.directory-picker header button {
  padding: 4px;
  border: 0;
  background: transparent;
}
.current-path {
  flex: none;
  margin: 0;
  padding: 10px 14px 6px;
  font-size: 12px;
  overflow-wrap: anywhere;
  max-height: 70px;
  overflow-y: auto;
}
.current-path span {
  display: block;
  color: var(--muted);
  margin-bottom: 3px;
}
.directory-shortcuts {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  padding: 6px 12px 10px;
  flex: none;
}
.directory-shortcuts button {
  font-size: 12px;
  padding: 5px 9px;
  white-space: nowrap;
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: block;
  flex: none;
}
.directory-scroll {
  flex: 1;
  min-height: 140px;
  overflow: auto;
  padding: 4px 0;
  border-top: 1px solid var(--line);
}
.directory-row {
  display: flex;
  align-items: center;
}
.directory-row button,
.directory-local {
  background: transparent;
  border: 0;
  border-radius: 0;
}
.directory-expand {
  flex: none;
  width: 28px;
  padding: 9px 4px;
  font-size: 18px;
}
.directory-name {
  display: flex;
  align-items: center;
  gap: 9px;
  text-align: left;
  justify-content: flex-start;
  padding: 9px 8px;
  flex: 1;
  min-width: 0;
}
.directory-name > span:first-of-type {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.directory-name svg,
.directory-local svg {
  width: 19px;
  height: 19px;
}
.directory-local {
  justify-content: flex-start;
  gap: 8px;
  width: 100%;
  padding: 9px 14px;
}
.active {
  background: var(--accent-soft) !important;
  color: var(--accent);
}
.selected-mark {
  margin-left: auto;
  padding-right: 8px;
}
.directory-error {
  color: var(--danger);
  font-size: 12px;
  padding: 0 14px;
  overflow-wrap: anywhere;
}
.directory-error button {
  margin-left: 8px;
  padding: 3px 8px;
}
.directory-empty,
.directory-status {
  color: var(--muted);
  font-size: 12px;
  margin: 5px 0;
  padding: 4px 14px;
}
.directory-picker footer {
  flex: none;
  font-size: 11px;
  color: var(--muted);
  border-top: 1px solid var(--line);
  padding: 8px 14px;
}
</style>
