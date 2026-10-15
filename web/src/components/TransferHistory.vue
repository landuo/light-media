<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, bytes, date, errorText, label, toast } from '../api';
const emit = defineEmits<{ organize: [paths: string[]]; job: [id: string] }>();
const query = ref(''),
  status = ref(''),
  page = ref(1),
  pageSize = ref(50),
  result = ref<any>(null),
  loading = ref(false),
  error = ref(''),
  selected = ref<string[]>([]),
  detail = ref<any>(null),
  jobDetail = ref<any>(null);
const pages = computed(() => Math.max(1, Math.ceil((result.value?.total || 0) / pageSize.value)));
const items = computed(() => result.value?.items || []);
let generation = 0,
  timer: number;
async function load(reset = false, silent = false) {
  if (reset) page.value = 1;
  const run = ++generation;
  if (!silent) loading.value = true;
  error.value = '';
  try {
    const next = await api(
      '/history/transfers?' +
        new URLSearchParams({
          q: query.value,
          status: status.value,
          page: String(page.value),
          page_size: String(pageSize.value),
        }),
    );
    if (run !== generation) return;
    result.value = next;
    selected.value = selected.value.filter((id) => next.items.some((item: any) => item.id === id));
  } catch (e) {
    if (run === generation) error.value = errorText(e);
  } finally {
    if (run === generation) loading.value = false;
  }
}
function statusLabel(item: any) {
  return item.status === 'success'
    ? '成功'
    : item.status === 'pending'
      ? label(item.job_state)
      : '失败';
}
function numbers(item: any) {
  return item.media?.media_type === 'tv'
    ? `S${String(item.season).padStart(2, '0')} ${item.episodes?.length ? item.episodes.map((ep: number) => 'E' + String(ep).padStart(2, '0')).join(' ') : ''}`
    : '电影';
}
async function retry(item: any) {
  if (
    !window.confirm(`重试整理「${item.media.title}」的所选文件？系统会再次检查源文件和目标冲突。`)
  )
    return;
  try {
    const job = await api('/history/transfers/' + item.id + '/retry', { confirm: true });
    toast('整理重试已加入队列');
    detail.value = null;
    await load();
    await showJob(job.id);
  } catch (e) {
    error.value = errorText(e);
  }
}
async function showJob(id: string) {
  try {
    jobDetail.value = await api('/jobs/' + id);
  } catch (e) {
    error.value = errorText(e);
  }
}
function recognize(item: any) {
  emit('organize', [item.source]);
}
onMounted(() => {
  void load();
  timer = window.setInterval(() => {
    if (items.value.some((item: any) => item.status === 'pending') && !document.hidden)
      void load(false, true);
  }, 10000);
});
onUnmounted(() => {
  generation++;
  clearInterval(timer);
});
</script>

<template>
  <section class="transfer-history">
    <div class="th-heading">
      <div>
        <span class="eyebrow">TRANSFER HISTORY</span>
        <h1>整理历史</h1>
        <p>按文件查看整理进度、结果和失败原因。</p>
      </div>
      <button :disabled="loading" @click="load()">刷新</button>
    </div>
    <p v-if="error" class="th-error" role="alert">{{ error }}</p>
    <form class="th-filters" @submit.prevent="load(true)">
      <input
        v-model="query"
        placeholder="搜索影视名称、源路径、目标路径"
        aria-label="搜索整理历史"
      /><select v-model="status" @change="load(true)" aria-label="整理状态">
        <option value="">所有状态</option>
        <option value="success">成功</option>
        <option value="pending">进行中</option>
        <option value="failed">失败</option></select
      ><select v-model="pageSize" @change="load(true)" aria-label="每页记录数">
        <option :value="25">25 条 / 页</option>
        <option :value="50">50 条 / 页</option>
        <option :value="100">100 条 / 页</option>
        <option :value="200">200 条 / 页</option></select
      ><button>搜索</button>
    </form>
    <div class="th-table-wrap" :aria-busy="loading">
      <table>
        <thead>
          <tr>
            <th>影视 / 季集</th>
            <th>源文件 → 目标文件</th>
            <th>方式 / 大小</th>
            <th>时间</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in items" :key="item.id">
            <td>
              <button class="th-title" @click="detail = item">
                {{ item.media.title }} <small>{{ item.media.year || '' }}</small></button
              ><small>{{ numbers(item) }}</small>
            </td>
            <td class="th-path">
              <span>{{ item.source }}</span
              ><span class="th-target">→ {{ item.target }}</span>
            </td>
            <td>
              {{ label(item.mode) }}<small>{{ bytes(item.size) }}</small>
            </td>
            <td>{{ date(item.updated_at) }}</td>
            <td>
              <span class="th-status" :class="item.status">{{ statusLabel(item) }}</span
              ><small v-if="item.error" class="th-error">{{ item.error }}</small>
            </td>
            <td>
              <div class="th-actions">
                <button @click="detail = item">详情</button
                ><button v-if="item.can_retry" @click="retry(item)">重试</button
                ><button v-if="item.can_recognize" @click="recognize(item)">重新识别</button
                ><button v-if="item.job_id" @click="showJob(item.job_id)">任务</button>
              </div>
            </td>
          </tr>
          <tr v-if="!items.length">
            <td colspan="6" class="th-empty">
              {{
                loading
                  ? '正在加载整理记录…'
                  : '没有匹配的整理历史。先在文件管理选择文件进行整理，提交后的记录会显示在这里。'
              }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="th-pagination">
      <span>共 {{ result?.total || 0 }} 条 · 第 {{ page }} / {{ pages }} 页</span
      ><button
        :disabled="page <= 1 || loading"
        @click="
          page--;
          load();
        "
      >
        上一页</button
      ><button
        :disabled="page >= pages || loading"
        @click="
          page++;
          load();
        "
      >
        下一页
      </button>
    </div>
    <div v-if="detail" class="th-overlay" @click.self="detail = null">
      <section role="dialog" aria-modal="true" aria-label="整理记录详情">
        <header>
          <h2>{{ detail.media.title }} · {{ numbers(detail) }}</h2>
          <button @click="detail = null">✕</button>
        </header>
        <dl>
          <dt>状态</dt>
          <dd>{{ statusLabel(detail) }} · {{ label(detail.file_status) }}</dd>
          <dt>源文件</dt>
          <dd>{{ detail.source }}</dd>
          <dt>目标文件</dt>
          <dd>{{ detail.target }}</dd>
          <dt>整理方式 / 大小</dt>
          <dd>{{ label(detail.mode) }} / {{ bytes(detail.size) }}</dd>
          <dt>开始 / 最近更新</dt>
          <dd>{{ date(detail.created_at) }} / {{ date(detail.updated_at) }}</dd>
          <dt v-if="detail.error">失败原因</dt>
          <dd v-if="detail.error" class="th-error">{{ detail.error }}</dd>
          <dt>任务链</dt>
          <dd>{{ detail.workflow_id }}</dd>
        </dl>
        <footer>
          <button v-if="detail.job_id" @click="showJob(detail.job_id)">查看任务日志</button
          ><button v-if="detail.can_retry" @click="retry(detail)">重试整理</button
          ><button v-if="detail.can_recognize" @click="recognize(detail)">从文件管理重新识别</button
          ><button @click="detail = null">关闭</button>
        </footer>
      </section>
    </div>
    <div v-if="jobDetail" class="th-overlay th-job" @click.self="jobDetail = null">
      <section role="dialog" aria-modal="true" aria-label="整理任务日志">
        <header>
          <h2>整理任务</h2>
          <button @click="jobDetail = null">✕</button>
        </header>
        <div class="th-job-body">
          <p>{{ label(jobDetail.job.state) }} · {{ jobDetail.job.id }}</p>
          <p v-if="jobDetail.job.error_message" class="th-error">
            {{ jobDetail.job.error_message }}
          </p>
          <ol>
            <li v-for="event in jobDetail.events" :key="event.seq">
              <small>{{ date(event.time) }} · {{ event.module }} · {{ event.level }}</small
              >{{ event.message }}
            </li>
          </ol>
        </div>
        <footer>
          <button @click="emit('job', jobDetail.job.id)">前往任务中心</button
          ><button @click="jobDetail = null">关闭</button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.transfer-history {
  max-width: 1500px;
  margin: auto;
}
.th-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 24px;
}
.th-heading h1 {
  font-size: 28px;
  margin: 5px 0;
}
.th-heading p {
  color: var(--muted, #9ba8ba);
}
.eyebrow {
  font-size: 11px;
  letter-spacing: 2px;
  color: var(--muted, #9ba8ba);
}
.th-filters {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  padding-bottom: 18px;
}
.th-filters input {
  flex: 1;
  min-width: 230px;
}
.th-table-wrap {
  overflow: auto;
}
table {
  border-collapse: collapse;
  width: 100%;
}
th,
td {
  text-align: left;
  padding: 14px 10px;
  border-bottom: 1px solid var(--line, #334154);
  vertical-align: top;
}
th {
  font-size: 12px;
  color: var(--muted, #9ba8ba);
  white-space: nowrap;
}
td {
  font-size: 13px;
}
td small {
  display: block;
  color: var(--muted, #9ba8ba);
  margin-top: 5px;
}
.th-title {
  text-align: left;
  border: 0;
  padding: 0;
  background: none;
  font-weight: 600;
}
.th-title small {
  display: inline;
}
.th-path {
  max-width: 460px;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.th-path span {
  display: block;
}
.th-target {
  margin-top: 5px;
  color: var(--muted, #9ba8ba);
}
.th-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  min-width: 125px;
}
.th-actions button {
  font-size: 12px;
  padding: 5px 8px;
}
.th-status {
  display: inline-flex;
  white-space: nowrap;
  padding: 3px 8px;
  border-radius: 20px;
  font-size: 12px;
}
.th-status.success {
  color: #86dcb3;
  background: #163e35;
}
.th-status.pending {
  color: #92bff4;
  background: #1f3c60;
}
.th-status.failed {
  color: #ffa5aa;
  background: #4a242e;
}
.th-error {
  color: #ff9da5 !important;
  white-space: pre-wrap;
}
.th-empty {
  text-align: center;
  padding: 60px;
  color: var(--muted, #9ba8ba);
}
.th-pagination {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 10px;
  margin-top: 20px;
}
.th-pagination span {
  margin-right: auto;
  color: var(--muted, #9ba8ba);
  font-size: 13px;
}
.th-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: #000a;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.th-overlay > section {
  background: var(--panel, #192331);
  border: 1px solid #3c4c63;
  border-radius: 12px;
  width: 800px;
  max-width: 100%;
  max-height: 90dvh;
  overflow: auto;
}
.th-overlay header,
.th-overlay footer {
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: space-between;
  padding: 16px 22px;
  border-bottom: 1px solid #354357;
}
.th-overlay h2 {
  font-size: 19px;
  margin: 0;
}
.th-overlay footer {
  justify-content: flex-end;
  border-top: 1px solid #354357;
  border-bottom: 0;
  flex-wrap: wrap;
}
.th-overlay dl {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 15px;
  padding: 20px 24px;
  margin: 0;
  font-size: 13px;
}
.th-overlay dt {
  color: var(--muted, #9ba8ba);
}
.th-overlay dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.th-job {
  z-index: 110;
}
.th-job-body {
  padding: 15px 22px;
}
.th-job-body li {
  padding: 8px 0;
  font-size: 13px;
}
.th-job-body li small {
  display: block;
  color: var(--muted, #9ba8ba);
  font-size: 11px;
  margin-bottom: 4px;
}
@media (max-width: 700px) {
  .th-overlay {
    padding: 6px;
  }
  .th-heading p {
    font-size: 12px;
  }
  .th-overlay dl {
    grid-template-columns: 80px 1fr;
    padding: 15px;
    gap: 12px;
  }
  .th-overlay > section {
    max-height: 96dvh;
  }
}
</style>
