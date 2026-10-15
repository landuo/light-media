<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, bytes, date, errorText, friendly, label, toast } from '../api';
import { candidateKey, resourceReviewReasons, type Candidate } from '../features/resources';
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
          <h2>{{ site.name }} 资源浏览</h2>
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
        <div v-if="candidates.length" class="site-resource-table">
          <table>
            <thead>
              <tr>
                <th class="resource-select"></th>
                <th>标题</th>
                <th>时间</th>
                <th>大小</th>
                <th>做种</th>
                <th>下载</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="candidate in candidates" :key="candidateKey(candidate)">
                <td class="resource-select">
                  <input
                    type="checkbox"
                    :aria-label="'选择 ' + candidate.resource.title"
                    :checked="selected.includes(candidateKey(candidate))"
                    :disabled="!candidate.accepted && !candidate.needs_confirmation"
                    @change="select(candidate)"
                  />
                </td>
                <td class="resource-title-cell">
                  <button
                    class="resource-title"
                    :disabled="!candidate.accepted && !candidate.needs_confirmation"
                    @click="open(candidate)"
                  >
                    {{ candidate.resource.title }}
                  </button>
                  <p v-if="candidate.resource.subtitle">{{ candidate.resource.subtitle }}</p>
                  <div class="resource-tags">
                    <span
                      v-if="candidate.resource.free || candidate.resource.download_factor === 0"
                      class="free"
                      >FREE</span
                    >
                    <span v-else-if="candidate.resource.download_factor < 1"
                      >{{ candidate.resource.download_factor }}×下载</span
                    >
                    <span v-if="candidate.resource.upload_factor > 1"
                      >{{ candidate.resource.upload_factor }}×上传</span
                    >
                    <span
                      v-for="tag in [
                        candidate.resource.resolution,
                        candidate.resource.codec,
                        candidate.resource.source,
                      ].filter(Boolean)"
                      :key="tag"
                      >{{ tag }}</span
                    >
                    <span v-if="downloaded[candidateKey(candidate)]">{{
                      label(downloaded[candidateKey(candidate)])
                    }}</span>
                  </div>
                  <details v-if="resourceReviewReasons(candidate, false).length">
                    <summary>{{ candidate.accepted ? '资源信息' : '不符合搜索规则' }}</summary>
                    <p>{{ resourceReviewReasons(candidate, false).map(friendly).join('；') }}</p>
                  </details>
                </td>
                <td>{{ date(candidate.resource.published_at) }}</td>
                <td>{{ candidate.resource.size > 0 ? bytes(candidate.resource.size) : '未知' }}</td>
                <td>{{ candidate.resource.seeders >= 0 ? candidate.resource.seeders : '未知' }}</td>
                <td>
                  {{ candidate.resource.leechers >= 0 ? candidate.resource.leechers : '未知' }}
                </td>
                <td class="resource-row-actions">
                  <a
                    v-if="candidate.resource.details_url?.startsWith('https://')"
                    :href="candidate.resource.details_url"
                    target="_blank"
                    rel="noopener noreferrer"
                    :aria-label="candidate.resource.title + ' 站点详情'"
                    title="站点详情"
                    ><AppIcon name="external"
                  /></a>
                  <button
                    class="icon-button"
                    :aria-label="'下载 ' + candidate.resource.title"
                    title="下载"
                    :disabled="!candidate.accepted && !candidate.needs_confirmation"
                    @click="open(candidate)"
                  >
                    <AppIcon name="download" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
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
  background: color-mix(in srgb, var(--text) 50%, transparent);
  z-index: 200;
  display: grid;
  place-items: center;
  padding: 20px;
}
.site-resource-dialog {
  display: flex;
  flex-direction: column;
  width: min(1472px, 100%);
  height: min(860px, calc(100dvh - 40px));
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow);
  overflow: hidden;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  min-height: 64px;
  padding: 12px 24px;
  color: white;
  background: var(--accent);
}
header .icon-button {
  color: inherit;
  border: 0;
  background: transparent;
}
h2 {
  font-size: 20px;
  font-weight: 500;
  margin: 0;
}
.site-resource-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
form {
  display: flex;
  gap: 10px;
  margin: 12px;
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: 8px;
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
  background: color-mix(in srgb, var(--text) 4%, var(--surface));
  border-color: transparent;
}
.site-resource-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0;
  background: var(--surface);
}
.site-resource-body > .error-box,
.site-resource-body > p:not(.site-resource-loading):not(.site-resource-empty) {
  margin: 12px;
}
.site-resource-grid {
  display: none;
}
.site-resource-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.site-resource-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--surface);
  text-align: left;
  font-weight: 500;
  color: var(--muted);
}
.site-resource-table th,
.site-resource-table td {
  border-bottom: 1px solid var(--line);
  padding: 10px 12px;
}
.site-resource-table tbody tr:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
}
.site-resource-table td:not(.resource-title-cell) {
  white-space: nowrap;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.resource-title-cell {
  width: 100%;
  min-width: 260px;
  overflow-wrap: anywhere;
}
.resource-title-cell > p {
  margin: 4px 0;
  font-size: 13px;
  color: var(--muted);
}
.resource-title {
  display: block;
  padding: 0;
  border: 0;
  text-align: left;
  line-height: 1.5;
  font-weight: 500;
  color: var(--text);
  background: transparent;
  box-shadow: none;
}
.resource-title:hover {
  color: var(--accent);
  background: transparent;
}
.resource-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 8px;
}
.resource-tags span {
  display: inline-flex;
  padding: 3px 8px;
  font-size: 12px;
  border-radius: 4px;
  color: var(--accent);
  background: var(--accent-soft);
}
.resource-tags span.free {
  color: var(--success, var(--accent));
  background: color-mix(in srgb, var(--success, var(--accent)) 12%, var(--surface));
}
.resource-select {
  width: 40px;
}
.resource-select input {
  width: 18px;
  height: 18px;
  margin: 0;
  accent-color: var(--accent);
}
.resource-row-actions .icon-button,
.resource-row-actions a {
  width: 32px;
  height: 32px;
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--muted);
  vertical-align: middle;
}
.resource-title-cell details {
  padding: 0;
  margin: 8px 0 0;
  border: 0;
  font-size: 12px;
  background: transparent;
}
.resource-title-cell details p {
  margin: 6px 0;
  color: var(--muted);
}
.resource-title-cell summary {
  padding: 0;
  color: var(--muted);
  background: transparent;
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
@media (max-width: 959px) {
  .site-resource-backdrop {
    padding: 0;
  }
  .site-resource-dialog {
    height: 100dvh;
    border-radius: 0;
  }
  .site-resource-table {
    display: none;
  }
  .site-resource-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 12px;
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
