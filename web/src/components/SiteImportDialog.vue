<script setup lang="ts">
import { computed, ref } from 'vue';
import { api, errorText } from '../api';
import {
  importSiteDocument,
  readSiteImport,
  siteImportDocument,
  sitePreview,
  type SiteImportDocument,
  type SiteImportResult,
} from '../features/site-transfer';

const emit = defineEmits<{
  close: [];
  imported: [result: SiteImportResult];
  busy: [value: boolean];
}>();
const fileInput = ref<HTMLInputElement>();
const document = ref<SiteImportDocument | null>(null);
const result = ref<SiteImportResult | null>(null);
const error = ref(''),
  reading = ref(false),
  importing = ref(false),
  dragging = ref(false),
  completed = ref(0);
const preview = computed(() => document.value?.sites.slice(0, 5).map(sitePreview) || []);
const status: Record<string, string> = {
  created: '已新增',
  updated: '已更新',
  skipped: '已跳过',
  failed: '失败',
};
async function read(file?: File) {
  if (!file || reading.value || importing.value) return;
  reading.value = true;
  error.value = '';
  result.value = null;
  document.value = null;
  try {
    document.value = siteImportDocument(await readSiteImport(file));
  } catch (e) {
    error.value = errorText(e);
  } finally {
    reading.value = false;
  }
}
async function select(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  await read(file);
}
async function drop(event: DragEvent) {
  dragging.value = false;
  await read(event.dataTransfer?.files[0]);
}
function reset() {
  if (!importing.value) {
    document.value = null;
    result.value = null;
    error.value = '';
  }
}
function close() {
  if (!importing.value && !reading.value) emit('close');
}
async function start() {
  if (!document.value || importing.value) return;
  importing.value = true;
  emit('busy', true);
  error.value = '';
  try {
    result.value = await importSiteDocument(
      document.value,
      (body) => api('/sites/import', body),
      (done) => {
        completed.value = done;
      },
    );
    emit('imported', result.value);
    if (
      result.value.imported &&
      !result.value.failed &&
      !result.value.skipped &&
      !result.value.results.some((row) => row.warnings.length)
    )
      emit('close');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    importing.value = false;
    emit('busy', false);
  }
}
</script>
<template>
  <div class="site-import-backdrop" @click.self="close">
    <section
      class="site-import-dialog"
      role="dialog"
      aria-modal="true"
      aria-label="导入站点"
      @keydown.esc.stop="close"
    >
      <header>
        <div>
          <h2>导入站点</h2>
          <small>从 MoviePilot 或本应用导出的 JSON 文件导入</small>
        </div>
        <button
          class="icon-button"
          aria-label="关闭站点导入"
          :disabled="importing || reading"
          @click="close"
        >
          ×
        </button>
      </header>
      <div class="site-import-body">
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <div
          v-if="!document"
          class="site-import-drop"
          :class="{ dragging }"
          @dragover.prevent="dragging = true"
          @dragleave="dragging = false"
          @drop.prevent="drop"
        >
          <input
            ref="fileInput"
            type="file"
            accept=".json,application/json"
            aria-label="选择站点配置文件"
            :disabled="reading"
            @change="select"
          />
          <p>将文件拖放到此处，或选择文件</p>
          <small>{{ reading ? '正在读取…' : '支持 JSON 文件，最大 2 MiB' }}</small>
        </div>
        <template v-else-if="importing">
          <p role="status">正在导入 {{ completed }} / {{ document.sites.length }}</p>
          <progress :value="completed" :max="document.sites.length"></progress>
        </template>
        <template v-else-if="result">
          <p class="site-import-summary" role="status">
            导入结果：新增 {{ result.created }}，更新 {{ result.updated }}，跳过
            {{ result.skipped }}，失败 {{ result.failed }}
          </p>
          <ul class="site-import-list">
            <li v-for="row in result.results" :key="row.index">
              <strong>{{ row.name }} · {{ status[row.status] }}</strong
              ><span>{{ row.message }}</span
              ><small v-for="(warning, index) in row.warnings" :key="index">{{ warning }}</small>
            </li>
          </ul>
          <footer>
            <button @click="reset">重置</button><button class="primary" @click="close">关闭</button>
          </footer>
        </template>
        <template v-else>
          <p>已读取 {{ document.sites.length }} 个站点，确认后开始导入。</p>
          <h3>
            数据预览 <small>显示 {{ preview.length }} / {{ document.sites.length }}</small>
          </h3>
          <ul class="site-import-list">
            <li v-for="(row, index) in preview" :key="index">
              <strong>{{ row.valid ? '✓' : '!' }} {{ row.name }}</strong
              ><span>{{ row.url || '未提供有效站点地址' }}</span>
            </li>
          </ul>
          <footer>
            <button @click="reset">重置</button
            ><button class="primary" @click="start">开始导入</button>
          </footer>
        </template>
      </div>
    </section>
  </div>
</template>
<style scoped>
.site-import-backdrop {
  position: fixed;
  inset: 0;
  z-index: 210;
  display: grid;
  place-items: center;
  padding: 20px;
  background: #12233270;
}
.site-import-dialog {
  width: min(760px, 100%);
  max-height: calc(100dvh - 40px);
  display: flex;
  flex-direction: column;
  border-radius: 12px;
  background: var(--surface);
  box-shadow: 0 24px 80px #0003;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--line);
}
h2 {
  margin: 0 0 5px;
  font-size: 18px;
}
small {
  color: var(--muted);
}
h3 {
  font-size: 14px;
}
.site-import-body {
  padding: 22px;
  overflow: auto;
}
.site-import-drop {
  border: 2px dashed var(--line);
  border-radius: 10px;
  padding: 36px 24px;
  text-align: center;
}
.site-import-drop.dragging {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.site-import-list {
  list-style: none;
  padding: 0;
  border: 1px solid var(--line);
  border-radius: 8px;
}
.site-import-list li {
  padding: 12px 15px;
  border-bottom: 1px solid var(--line);
  overflow-wrap: anywhere;
}
.site-import-list li:last-child {
  border: 0;
}
.site-import-list span,
.site-import-list small {
  display: block;
  margin-top: 5px;
  font-size: 12px;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 20px;
}
progress {
  width: 100%;
}
.site-import-summary {
  line-height: 1.7;
}
@media (max-width: 600px) {
  .site-import-backdrop {
    padding: 10px;
  }
  .site-import-body {
    padding: 16px;
  }
}
</style>
