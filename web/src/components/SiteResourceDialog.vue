<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, errorText, friendly, toast } from '../api';
import { candidateKey, type Candidate } from '../features/resources';
import ResourceCard from './ResourceCard.vue';
import AddDownloadDialog from './AddDownloadDialog.vue';
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
        <h2>{{ site.name }} · 资源</h2>
        <button class="icon-button" aria-label="关闭站点资源" @click="emit('close')">×</button>
      </header>
      <form @submit.prevent="search(true)">
        <input
          v-model="query"
          aria-label="站点资源关键词"
          placeholder="搜索关键词，留空浏览最新资源"
        /><button class="primary" :disabled="loading">搜索</button
        ><button v-if="selected.length" type="button" :disabled="loading" @click="openSelected">
          下载所选 {{ selected.length }} 项
        </button>
      </form>
      <div class="site-resource-body">
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <p v-if="loading" role="status">正在读取站点资源…</p>
        <p
          v-for="siteResult in output?.sites || []"
          :key="siteResult.id"
          :class="{ 'error-box': siteResult.status !== 'ok' }"
        >
          {{ siteResult.message ? friendly(siteResult.message) : '' }}
        </p>
        <p v-if="output && !candidates.length">本页未返回资源。</p>
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
  background: #12233270;
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
  border-radius: 12px;
  box-shadow: 0 24px 80px #0003;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 20px;
  border-bottom: 1px solid var(--line);
}
h2 {
  font-size: 18px;
  margin: 0;
}
form {
  display: flex;
  gap: 10px;
  padding: 15px 20px;
}
form input {
  flex: 1;
  min-width: 0;
}
.site-resource-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 20px 20px;
}
.site-resource-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
  gap: 14px;
}
footer {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 16px;
  padding: 12px;
  border-top: 1px solid var(--line);
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
    padding: 0 12px 12px;
  }
}
</style>
