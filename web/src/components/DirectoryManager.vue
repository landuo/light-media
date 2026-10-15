<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { api, action, clone, errorText, refreshSettings, toast } from '../api';
import PathPicker from './PathPicker.vue';
defineEmits<{ organize: [paths: string[]] }>();
const directories = ref<any[]>([]),
  expanded = ref<string[]>([]),
  saving = ref(false),
  loading = ref(false),
  loadError = ref(''),
  dragging = ref('');
let serial = 0;
function rows(result: any) {
  directories.value = (result.directories || []).map((row: any) => ({
    ...clone(row.config || row),
    monitor_type:
      (row.config || row).enabled === false ? 'none' : (row.config || row).monitor_type || 'none',
    _key: String(++serial),
  }));
}
async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    rows(await api('/directories/manage'));
  } catch (e) {
    loadError.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
function add() {
  let n = directories.value.length + 1;
  while (directories.value.some((directory) => directory.name === '目录' + n)) n++;
  const key = String(++serial);
  directories.value.push({
    _key: key,
    id: '',
    name: '目录' + n,
    source_path: '',
    library_path: '',
    media_type: 'mixed',
    mode: 'hardlink',
    monitor_type: 'none',
    enabled: false,
    include_existing: false,
    renaming: false,
    scraping: false,
    library_type_folder: false,
  });
  expanded.value.push(key);
}
function toggle(key: string) {
  expanded.value = expanded.value.includes(key)
    ? expanded.value.filter((value) => value !== key)
    : [...expanded.value, key];
}
function drop(key: string) {
  const source = directories.value.findIndex((directory) => directory._key === dragging.value);
  const target = directories.value.findIndex((directory) => directory._key === key);
  if (source < 0 || target < 0 || source === target) return;
  const [directory] = directories.value.splice(source, 1);
  directories.value.splice(target, 0, directory);
  dragging.value = '';
}
function remove(key: string) {
  directories.value = directories.value.filter((directory) => directory._key !== key);
}
async function save() {
  const invalid = directories.value.find(
    (directory) =>
      !directory.name?.trim() ||
      !directory.source_path?.trim() ||
      (directory.monitor_type !== 'none' && !directory.library_path?.trim()),
  );
  if (invalid) {
    if (!expanded.value.includes(invalid._key)) expanded.value.push(invalid._key);
    toast('请填写目录别名、资源目录和媒体库目录', true);
    return;
  }
  if (
    new Set(directories.value.map((directory) => directory.name)).size !== directories.value.length
  ) {
    toast('存在重复目录名称！无法保存，请修改！', true);
    return;
  }
  saving.value = true;
  try {
    const payload = directories.value.map((directory) => ({
      id: directory.id,
      name: directory.name,
      source_path: directory.source_path,
      library_path: directory.library_path || '',
      media_type: directory.media_type,
      mode: directory.mode,
      monitor_type: directory.monitor_type || 'none',
      enabled: directory.monitor_type !== 'none',
      include_existing: !!directory.include_existing,
      renaming: directory.renaming !== false,
      scraping: directory.scraping !== false,
      library_type_folder: !!directory.library_type_folder,
    }));
    const result = await action(
      () => api('/directories/manage', { directories: payload }, 'PUT'),
      '目录设置保存成功',
    );
    if (result) {
      rows(result);
      await refreshSettings();
    }
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <section class="panel directory-settings">
    <header class="section-heading">
      <div>
        <h2>目录</h2>
      </div>
    </header>
    <p v-if="loadError" class="error-box">{{ loadError }}</p>
    <form novalidate @submit.prevent="save">
      <div class="directory-grid">
        <article
          v-for="directory in directories"
          :key="directory._key"
          class="directory-card"
          @dragover.prevent
          @drop.prevent="drop(directory._key)"
        >
          <header>
            <label>
              <span class="hint">目录别名</span>
              <input v-model.trim="directory.name" required maxlength="80" />
            </label>
            <button
              type="button"
              class="icon-button directory-drag"
              draggable="true"
              aria-label="拖动排序"
              title="拖动排序，按排列顺序依次匹配目录规则"
              @dragstart="dragging = directory._key"
              @dragend="dragging = ''"
            >
              ⠿
            </button>
            <button
              type="button"
              class="icon-button danger"
              :aria-label="'移除 ' + directory.name"
              @click="remove(directory._key)"
            >
              ×
            </button>
          </header>
          <div v-show="expanded.includes(directory._key)" class="directory-fields form-grid">
            <label
              >媒体类型<select v-model="directory.media_type">
                <option value="mixed">全部</option>
                <option value="movie">电影</option>
                <option value="tv">电视剧</option>
              </select></label
            >
            <label>资源存储<input value="本地" readonly /></label>
            <PathPicker
              v-model="directory.source_path"
              label="资源目录"
              class="span-two"
              required
              placeholder="/media/download"
            />
            <label class="span-two"
              >自动整理<select v-model="directory.monitor_type">
                <option value="none">不整理</option>
                <option value="downloader">下载器监控</option>
                <option value="monitor">目录监控</option>
              </select></label
            >
            <template v-if="directory.monitor_type !== 'none'">
              <label>媒体库存储<input value="本地" readonly /></label>
              <label
                >整理方式<select v-model="directory.mode">
                  <option value="hardlink">硬链接</option>
                  <option value="copy">复制</option>
                  <option value="auto">自动</option>
                </select></label
              >
              <PathPicker
                v-model="directory.library_path"
                label="媒体库目录"
                class="span-two"
                required
                placeholder="/media/电影"
              />
              <label class="check"
                ><input v-model="directory.renaming" type="checkbox" />智能重命名</label
              >
              <label class="check"
                ><input v-model="directory.scraping" type="checkbox" />刮削元数据</label
              >
              <label class="check span-two"
                ><input
                  v-model="directory.library_type_folder"
                  type="checkbox"
                />按媒体类型建立子目录</label
              >
            </template>
          </div>
          <p v-if="!expanded.includes(directory._key)" class="directory-summary">
            {{ directory.source_path || '资源目录未设置' }}
            <span v-if="directory.monitor_type !== 'none'">
              → {{ directory.library_path || '媒体库目录未设置' }}</span
            >
          </p>
          <button
            class="directory-expand"
            type="button"
            :aria-label="(expanded.includes(directory._key) ? '收起 ' : '展开 ') + directory.name"
            :aria-expanded="expanded.includes(directory._key)"
            @click="toggle(directory._key)"
          >
            {{ expanded.includes(directory._key) ? '⌃' : '⌄' }}
          </button>
        </article>
      </div>
      <p v-if="!loading && !loadError && !directories.length" class="notice">尚未添加目录。</p>
      <footer class="button-row">
        <button class="primary" type="submit" :disabled="saving || loading">
          {{ saving ? '保存中…' : '保存' }}
        </button>
        <button type="button" @click="add">＋ 添加</button>
      </footer>
    </form>
  </section>
</template>
<style scoped>
.directory-settings > header p {
  margin-bottom: 0;
}
.directory-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 380px), 1fr));
  gap: 18px;
  margin: 24px 0;
  align-items: start;
}
.directory-card {
  border: 1px solid var(--line);
  border-radius: 12px;
  overflow: hidden;
  background: var(--surface);
}
.directory-card > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 16px 20px;
}
.directory-drag {
  cursor: grab;
}
.directory-card > header label {
  flex: 1;
}
.directory-card > header input {
  font-weight: 600;
}
.directory-fields {
  padding: 0 20px 18px;
}
.span-two {
  grid-column: 1 / -1;
}
.directory-summary {
  margin: 0;
  padding: 0 20px 16px;
  font-size: 12px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.directory-expand {
  display: block;
  width: 100%;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
  font-size: 22px;
  line-height: 1;
}
</style>
