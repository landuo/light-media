<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, errorText, friendly, toast } from '../api';
import { candidateKey, type Candidate } from '../features/resources';
import ResourceCard from './ResourceCard.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
import AppIcon from './AppIcon.vue';
const props = defineProps<{ site: { id: string; name: string; enabled: boolean } }>();
const emit = defineEmits<{ close: [] }>();
const query = ref(''),
  page = ref(1),
  loading = ref(false),
  error = ref('');
const output = ref<any>(null),
  active = ref<Candidate | null>(null),
  selected = ref<string[]>([]),
  queue = ref<Candidate[]>([]),
  downloaded = ref<Record<string, string>>({});
const request = ref({ query: '', page: 1 });
const names = computed(() => ({ [props.site.id]: props.site.name }));
const candidates = computed<Candidate[]>(() => output.value?.candidates || []);
let alive = true;
async function search(reset = false) {
  if (loading.value) return;
  if (reset) page.value = 1;
  loading.value = true;
  error.value = '';
  output.value = null;
  selected.value = [];
  request.value = { query: query.value.trim(), page: page.value };
  try {
    const result = await api(
      '/sites/manage/' + encodeURIComponent(props.site.id) + '/resources',
      request.value,
    );
    if (alive) output.value = result;
  } catch (e) {
    if (alive) error.value = errorText(e);
  } finally {
    if (alive) loading.value = false;
  }
}
function select(candidate: Candidate) {
  const key = candidateKey(candidate);
  selected.value = selected.value.includes(key)
    ? selected.value.filter((item) => item !== key)
    : [...selected.value, key];
}
function open(candidate: Candidate) {
  queue.value = [];
  active.value = candidate;
}
function openSelected() {
  queue.value = candidates.value.filter((candidate) =>
    selected.value.includes(candidateKey(candidate)),
  );
  active.value = queue.value.shift() || null;
}
function downloadedOne() {
  if (active.value) {
    downloaded.value[candidateKey(active.value)] = 'planned';
    selected.value = selected.value.filter((key) => key !== candidateKey(active.value!));
  }
  toast('下载任务已添加');
  active.value = queue.value.shift() || null;
}
onMounted(() => void search());
onUnmounted(() => {
  alive = false;
});
</script>
<template>
  <div class="site-resource-backdrop" @click.self="emit('close')">
    <section
      class="site-resource-dialog"
      role="dialog"
      aria-modal="true"
      :aria-label="site.name + ' 资源浏览'"
    >
      <header>
        <div class="site-resource-heading">
          <span><AppIcon name="globe" /></span>
          <h2>{{ site.name }} · 资源</h2>
        </div>
        <button class="icon-button" aria-label="关闭站点资源" @click="emit('close')">
          <AppIcon name="close" />
        </button>
      </header>
      <form @submit.prevent="search(true)">
        <div class="site-resource-search">
          <AppIcon name="search" /><input
            v-model="query"
            aria-label="站点资源关键词"
            placeholder="搜索关键词，留空浏览最新资源"
          />
        </div>
        <button class="primary" :disabled="loading">搜索</button
        ><button v-if="selected.length" type="button" :disabled="loading" @click="openSelected">
          下载所选 {{ selected.length }} 项
        </button>
      </form>
      <div class="site-resource-body">
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <p v-if="loading" class="site-resource-loading" role="status">
          <AppIcon name="refresh" />正在读取站点资源…
        </p>
        <p
          v-for="siteResult in output?.sites || []"
          :key="siteResult.id"
          :class="{ 'error-box': siteResult.status !== 'ok' }"
        >
          {{ siteResult.message ? friendly(siteResult.message) : '' }}
        </p>
        <p v-if="output && !candidates.length" class="site-resource-empty">
          <AppIcon name="search" />本页未返回资源。
        </p>
        <div class="site-resource-grid">
          <ResourceCard
            v-for="candidate in candidates"
            :key="candidateKey(candidate)"
            :candidates="[candidate]"
            :names="names"
            :downloads="downloaded"
            :selected="selected"
            :show-identity-review="false"
            @select="select"
            @download="open"
          />
        </div>
      </div>
      <footer>
        <button
          :disabled="loading || page <= 1"
          @click="
            page--;
            search();
          "
        >
          上一页</button
        ><span>第 {{ page }} 页</span
        ><button
          :disabled="loading || !output?.has_more"
          @click="
            page++;
            search();
          "
        >
          下一页
        </button>
      </footer>
    </section>
    <div v-if="active" class="site-download-layer">
      <AddDownloadDialog
        :candidate="active"
        :request="{ ...request, browse: true }"
        @close="
          active = null;
          queue = [];
        "
        @done="downloadedOne"
      />
    </div>
  </div>
</template>
<style scoped>
.site-resource-backdrop {
  position: fixed;
  inset: 0;
  background: #12233273;
  backdrop-filter: blur(4px);
  z-index: 200;
  display: grid;
  place-items: center;
  padding: 20px;
}
.site-resource-dialog {
  display: flex;
  flex-direction: column;
  width: min(1180px, 100%);
  height: min(860px, calc(100dvh - 40px));
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 24px 80px #172b3a29;
  overflow: hidden;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 18px 24px;
  border-bottom: 1px solid var(--line);
}
h2 {
  font-size: 17px;
  font-weight: 650;
  margin: 0;
}
.site-resource-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.site-resource-heading > span {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: 10px;
  color: var(--accent);
  background: var(--accent-soft);
}
form {
  display: flex;
  gap: 10px;
  padding: 20px 24px;
  border-bottom: 1px solid var(--line);
}
.site-resource-search {
  position: relative;
  flex: 1;
  min-width: 0;
}
.site-resource-search > .app-icon {
  position: absolute;
  top: 50%;
  left: 13px;
  transform: translateY(-50%);
  color: var(--muted);
  width: 18px;
  height: 18px;
  pointer-events: none;
}
.site-resource-search input {
  padding-left: 41px;
}
.site-resource-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 20px 24px;
  background: color-mix(in srgb, var(--line) 12%, var(--surface));
}
.site-resource-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
  gap: 16px;
}
.site-resource-loading,
.site-resource-empty {
  min-height: 240px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  gap: 14px;
  color: var(--muted);
  font-size: 13px;
}
.site-resource-loading .app-icon,
.site-resource-empty .app-icon {
  width: 28px;
  height: 28px;
  color: var(--accent);
}
.site-resource-loading .app-icon {
  animation: site-resource-spin 1s linear infinite;
}
footer {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 16px;
  padding: 14px;
  border-top: 1px solid var(--line);
}
footer span {
  font-size: 12px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
@keyframes site-resource-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .site-resource-loading .app-icon {
    animation: none;
  }
}
.site-download-layer {
  position: fixed;
  inset: 0;
  z-index: 1;
}
@media (max-width: 600px) {
  .site-resource-backdrop {
    padding: 8px;
  }
  .site-resource-dialog {
    height: calc(100dvh - 16px);
  }
  form {
    padding: 12px;
    flex-wrap: wrap;
  }
  .site-resource-body {
    padding: 12px;
  }
  header {
    padding: 14px;
  }
  .site-resource-search {
    flex-basis: calc(100% - 90px);
  }
}
</style>
