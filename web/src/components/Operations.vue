<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue';
import {
  api,
  apiURL,
  action,
  ui,
  bytes,
  date,
  label,
  image,
  episodeNumbers,
  go,
  errorText,
  friendly,
  materialLabel,
} from '../api';
import DownloadReview from './DownloadReview.vue';
import { jobQueueInfo } from '../features/job-queue';
const props = defineProps<{ view: string; initialJob?: string }>(),
  data = ref<any>(null),
  loading = ref(false),
  error = ref(''),
  path = ref(''),
  recursive = ref(true),
  scanned = ref<any[]>([]),
  scanTruncated = ref(false),
  selectedPaths = ref<string[]>([]),
  lookup = ref(''),
  lookupKind = ref('tv'),
  lookupResults = ref<any[]>([]),
  media = ref<any>(null),
  root = ref(''),
  mode = ref('hardlink'),
  plan = ref<any>(null),
  planSelected = ref<string[]>([]),
  removePreview = ref<any>(null),
  jobDetail = ref<any>(null),
  downloadReview = ref<any>(null),
  logLevel = ref(''),
  logModule = ref(''),
  logSite = ref(''),
  logQuery = ref(''),
  logFrom = ref(''),
  logTo = ref(''),
  logJob = ref(''),
  live = ref(false),
  logGap = ref(''),
  assetQuery = ref(''),
  plans = ref<any[]>([]);
const candidates = ref<any[]>([]),
  candidateCursor = ref(''),
  candidateError = ref(''),
  candidateLoading = ref(false),
  registration = ref<any>(null),
  registrationSeason = ref(1),
  registrationEpisode = ref<number | ''>(''),
  registrationSeasonData = ref<any>(null),
  registrationSeasonLoading = ref(false),
  registrationConfirmed = ref(false);
const registrationError = ref('');
let candidateGeneration = 0,
  registrationGeneration = 0,
  identityGeneration = 0;
const names: any = {
  downloads: ['DOWNLOADS', '下载任务', '下载器保持原有做种状态，整理不会移动或删除下载源。'],
  organize: [
    'ORGANIZE',
    '整理，让文件回到该在的位置',
    '扫描允许的目录，人工确认身份与季集，再预览执行。',
  ],
  library: ['LIBRARY', '我的媒体库', '查看媒体资产、外部文件登记与飞牛入库状态。'],
  jobs: ['WORKFLOWS', '任务队列', '查看具体处理的影视、文件与当前阶段，失败时可查看原因并重试。'],
  logs: ['OBSERVABILITY', '日志中心', '按级别、模块、任务和时间定位问题。'],
};
const filteredAssets = computed(() =>
  Array.isArray(data.value)
    ? data.value.filter((a: any) =>
        (a.media?.title + ' ' + a.path).toLowerCase().includes(assetQuery.value.toLowerCase()),
      )
    : [],
);
const queueJobs = computed(() =>
  (Array.isArray(data.value) ? data.value : []).map((job: any) => ({
    ...job,
    display: jobQueueInfo(job),
  })),
);
const selectedJobInfo = computed(() =>
  jobDetail.value?.job ? jobQueueInfo(jobDetail.value.job) : null,
);
let timer: number,
  sse: EventSource | null = null;
function logParams() {
  return new URLSearchParams({
    level: logLevel.value,
    module: logModule.value,
    site_id: logSite.value,
    q: logQuery.value,
    from: logFrom.value ? new Date(logFrom.value).toISOString() : '',
    to: logTo.value ? new Date(logTo.value).toISOString() : '',
    job_id: logJob.value,
  });
}
async function load(silent = false) {
  if (!silent) loading.value = true;
  error.value = '';
  try {
    if (props.view === 'downloads') data.value = await api('/downloads');
    else if (props.view === 'library') {
      data.value = (await api('/assets')) || [];
      await loadCandidates();
    } else if (props.view === 'jobs') data.value = (await api('/jobs')) || [];
    else if (props.view === 'logs') {
      data.value = await api('/logs?' + logParams());
      logGap.value = data.value?.gap ? '历史日志存在缺口，当前查询可能不完整。' : '';
    } else if (props.view === 'organize') plans.value = (await api('/import/plans')) || [];
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function loadCandidates(append = false) {
  if (!root.value) {
    candidates.value = [];
    candidateCursor.value = '';
    return;
  }
  const generation = ++candidateGeneration;
  candidateLoading.value = true;
  candidateError.value = '';
  if (!append) {
    candidates.value = [];
    candidateCursor.value = '';
  }
  try {
    const query = new URLSearchParams({
      root_id: root.value,
      limit: '50',
      cursor: append ? candidateCursor.value : '',
    });
    const result = await api('/assets/candidates?' + query);
    if (generation !== candidateGeneration) return;
    candidates.value = [...(append ? candidates.value : []), ...(result.items || [])];
    candidateCursor.value = result.next_cursor || '';
  } catch (e) {
    if (generation === candidateGeneration) candidateError.value = errorText(e);
  } finally {
    if (generation === candidateGeneration) candidateLoading.value = false;
  }
}
function beginRegistration(candidate: any) {
  identityGeneration++;
  registrationGeneration++;
  registration.value = candidate;
  media.value = null;
  lookup.value = '';
  lookupResults.value = [];
  registrationSeasonData.value = null;
  registrationEpisode.value = '';
  registrationConfirmed.value = false;
  registrationError.value = '';
}
async function loadRegistrationSeason() {
  const generation = ++registrationGeneration;
  registrationSeasonData.value = null;
  registrationEpisode.value = '';
  registrationConfirmed.value = false;
  if (media.value?.media_type !== 'tv') return;
  registrationSeasonLoading.value = true;
  try {
    const result = await action(() =>
      api('/catalog/season?id=' + media.value.id + '&season=' + registrationSeason.value),
    );
    if (generation === registrationGeneration) registrationSeasonData.value = result;
  } finally {
    if (generation === registrationGeneration) registrationSeasonLoading.value = false;
  }
}
async function registerAsset() {
  if (!media.value || !registrationConfirmed.value) return;
  registrationError.value = '';
  const result = await action(
    () =>
      api('/assets/register', {
        candidate_id: registration.value.id,
        media_type: media.value.media_type,
        tmdb_id: media.value.id,
        season: media.value.media_type === 'movie' ? 0 : registrationSeason.value,
        episodes: media.value.media_type === 'movie' ? [] : [registrationEpisode.value],
        confirm: true,
      }).catch((e) => {
        registrationError.value = errorText(e);
        throw e;
      }),
    '已登记外部文件，原视频与旁车未修改',
  );
  if (result) {
    registration.value = null;
    await load();
  }
}
async function moreLogs() {
  if (!data.value?.next_cursor || loading.value) return;
  loading.value = true;
  try {
    const query = logParams();
    query.set('cursor', data.value.next_cursor);
    const next = await api('/logs?' + query);
    const seen = new Set(
      (data.value.entries || []).filter((entry: any) => entry.id).map((entry: any) => entry.id),
    );
    data.value = {
      ...next,
      entries: [
        ...data.value.entries,
        ...(next.entries || []).filter((entry: any) => !entry.id || !seen.has(entry.id)),
      ].slice(0, 2000),
    };
    if (next.gap) logGap.value = '历史日志存在缺口，当前查询可能不完整。';
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function downloadAction(intent: any, name: string) {
  if (name === 'remove_keep_data' && !confirm('从下载器移除此任务，但保留下载数据？')) return;
  await action(() => api('/downloads/' + intent.id + '/action', { action: name }), '操作已提交');
  load();
}
function task(intent: any) {
  return data.value?.instances
    ?.find((d: any) => d.id === intent.downloader_id)
    ?.tasks?.find((t: any) => t.hash === intent.hash);
}
async function scan() {
  const r = await action(() =>
    api('/import/scan', { path: path.value, recursive: recursive.value }),
  );
  if (r) {
    scanned.value = r.files || [];
    scanTruncated.value = r.truncated;
    selectedPaths.value = scanned.value.map((f) => f.path);
    for (const f of scanned.value) {
      f.manual_season = f.parsed?.type === 'movie' ? 0 : (f.parsed?.original?.season ?? '');
      f.manual_episodes = (f.parsed?.original?.episodes || f.parsed?.episodes || []).join(',');
    }
    plan.value = null;
  }
}
async function find() {
  lookupResults.value =
    (
      await action(() =>
        api(
          '/catalog/search?q=' +
            encodeURIComponent(lookup.value) +
            '&kind=' +
            lookupKind.value +
            '&page=1',
        ),
      )
    )?.results || [];
}
async function pick(m: any) {
  const generation = ++identityGeneration;
  const result = await action(() => api('/catalog/detail?kind=' + m.media_type + '&id=' + m.id));
  if (result && generation === identityGeneration) {
    media.value = result;
    lookupResults.value = [];
    if (registration.value) {
      registrationConfirmed.value = false;
      registrationSeason.value =
        result.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
      await loadRegistrationSeason();
    }
  }
}
async function adoptNFO(file: any) {
  const inferred = file.inferred_media;
  if (!inferred?.id || !['movie', 'tv'].includes(inferred.media_type)) return;
  await pick(inferred);
  if (media.value?.id !== inferred.id || media.value?.media_type !== inferred.media_type) return;
  if (inferred.media_type === 'movie') {
    file.manual_season = 0;
    file.manual_episodes = '';
  } else if (file.numbering) {
    file.manual_season = file.numbering.season ?? '';
    file.manual_episodes = (file.numbering.episodes || []).join(',');
  }
}
async function makePlan() {
  const r = await action(async () => {
    if (!media.value) throw new Error('请选择影视身份');
    if (
      media.value.media_type === 'tv' &&
      scanned.value.some(
        (f) => selectedPaths.value.includes(f.path) && (!f.manual_season || +f.manual_season < 1),
      )
    )
      throw new Error('请为每个所选文件明确确认标准季号');
    return api('/import/preview', {
      media_type: media.value.media_type,
      tmdb_id: media.value.id,
      root_id: root.value,
      mode: mode.value,
      files: scanned.value
        .filter((f) => selectedPaths.value.includes(f.path))
        .map((f) => ({
          path: f.path,
          season: media.value.media_type === 'movie' ? 0 : +f.manual_season,
          episodes: media.value.media_type === 'tv' ? episodeNumbers(f.manual_episodes) : [],
        })),
      mappings: [],
    });
  });
  if (r) {
    plan.value = r;
    planSelected.value = (r.files || [])
      .filter((f: any) => !f.reason && f.status !== 'blocked')
      .map((f: any) => f.id);
  }
}
async function execute() {
  const r = await action(
    () => api('/import/execute', { plan_id: plan.value.id, file_ids: planSelected.value }),
    '整理任务已提交',
  );
  if (r) {
    plan.value = null;
    load();
    go('jobs');
  }
}
async function assetAction(a: any, name: string) {
  await action(() => api('/assets/' + a.id + '/' + name, {}), '任务已提交');
  load();
}
async function inspectRemove(a: any) {
  removePreview.value = await action(() => api('/assets/' + a.id + '/remove-preview', {}));
}
async function remove() {
  const r = await action(
    () =>
      api('/assets/' + removePreview.value.asset_id + '/remove', {
        preview_id: removePreview.value.preview_id,
        confirm: true,
        delete_files: true,
        exclude: true,
      }),
    '自管资产已移除，下载源已保留',
  );
  if (r) {
    removePreview.value = null;
    load();
  }
}
async function resolve(a: any, name: string) {
  await action(() => api('/assets/' + a.id + '/resolve', { action: name }), '缺失状态已更新');
  load();
}
async function libraryScan() {
  await action(() => api('/assets/scan', { root_id: root.value }), '核对任务已提交');
}
async function inspectJob(j: any) {
  jobDetail.value = await action(() => api('/jobs/' + j.id));
}
async function jobAction(j: any, name: string) {
  await action(() => api('/jobs/' + j.id + '/' + name, {}), '任务状态已更新');
  load();
  if (jobDetail.value?.job.id === j.id) inspectJob(j);
}
function stream() {
  sse?.close();
  sse = null;
  if (live.value) {
    sse = new EventSource(apiURL('/logs/stream?' + logParams()), { withCredentials: true });
    sse.onmessage = (e) => {
      try {
        const entry = JSON.parse(e.data);
        if (
          (logSite.value && entry.site_id !== logSite.value) ||
          (logLevel.value && !String(entry.level).includes(logLevel.value)) ||
          (logModule.value && !String(entry.module).includes(logModule.value)) ||
          (logQuery.value &&
            !JSON.stringify(entry).toLowerCase().includes(logQuery.value.toLowerCase()))
        )
          return;
        const at = new Date(entry.time).getTime();
        if (
          (logFrom.value && at < new Date(logFrom.value).getTime()) ||
          (logTo.value && at > new Date(logTo.value).getTime())
        )
          return;
        if (!data.value) data.value = { entries: [] };
        if (entry.id && data.value.entries.some((x: any) => x.id === entry.id)) return;
        data.value.entries.unshift(entry);
        data.value.entries = data.value.entries.slice(0, 2000);
      } catch {}
    };
    sse.addEventListener('gap', () => {
      logGap.value = '实时日志存在缺口，已停止跟随。请重新查询历史日志补齐上下文。';
      sse?.close();
      sse = null;
      live.value = false;
    });
    sse.onerror = () => {
      sse?.close();
      sse = null;
      live.value = false;
      error.value = '实时日志连接已断开，请重新连接。';
    };
  }
}
onMounted(() => {
  root.value = ui.settings?.default_root || ui.settings?.roots?.[0]?.id || '';
  if (props.view === 'logs' && props.initialJob)
    logJob.value = decodeURIComponent(props.initialJob);
  load();
  if (props.view === 'jobs' && props.initialJob)
    inspectJob({ id: decodeURIComponent(props.initialJob) });
  if (props.view === 'jobs' || props.view === 'downloads')
    timer = window.setInterval(() => {
      if (!document.hidden) load(true);
    }, 15000);
});
onUnmounted(() => {
  clearInterval(timer);
  sse?.close();
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">{{ names[view]?.[0] }}</p>
      <h1>{{ names[view]?.[1] }}</h1>
      <p class="muted">{{ names[view]?.[2] }}</p>
    </div>
    <button @click="load()" :disabled="loading">↻ 刷新</button>
  </div>
  <p v-if="error" class="error-box">{{ error }}</p>
  <template v-if="view === 'downloads'"
    ><div class="stat-row">
      <div class="stat">
        <span>本程序任务</span><strong>{{ data?.intents?.length || 0 }}</strong>
      </div>
      <div class="stat">
        <span>已连接实例</span
        ><strong>{{ data?.instances?.filter((x: any) => !x.error).length || 0 }}</strong>
      </div>
    </div>
    <section v-for="d in data?.instances || []" :key="d.id" class="status-strip">
      <span :class="['badge', { bad: d.error }]"
        >{{ d.id }} · {{ d.error || d.tasks?.length + ' 个任务' }}</span
      >
    </section>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>影视 / 资源</th>
            <th>状态</th>
            <th>下载进度</th>
            <th>速度 / 分享率</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in data?.intents || []" :key="d.id">
            <td class="resource-title">
              <strong>{{ d.media?.title }}</strong
              ><small>{{ d.resource_title }}</small
              ><small>{{ d.downloader_id }} · {{ d.id }}</small
              ><small v-if="d.download_path">下载目录：{{ d.download_path }}</small
              ><small v-if="d.error" class="error-text">{{ d.error }}</small>
            </td>
            <td>
              <span class="badge">{{ label(d.state) }}</span>
              <small>{{ materialLabel(d) }}</small>
            </td>
            <td>
              <div class="progress">
                <span
                  :style="{
                    width: Math.min(100, (task(d)?.progress ?? d.progress ?? 0) * 100) + '%',
                  }"
                ></span>
              </div>
              {{ Math.round((task(d)?.progress ?? d.progress ?? 0) * 100) }}%<small>{{
                bytes(task(d)?.total_size || 0)
              }}</small>
            </td>
            <td>
              ↓ {{ bytes(task(d)?.download_speed || 0) }}/s<small
                >↑ {{ bytes(task(d)?.upload_speed || 0) }}/s ·
                {{ (task(d)?.ratio || 0).toFixed(2) }}</small
              >
            </td>
            <td>
              <div class="button-row">
                <button class="small" @click="downloadAction(d, 'start')">开始</button
                ><button class="small" @click="downloadReview = d">文件与人工纠错</button
                ><button class="small" @click="downloadAction(d, 'pause')">暂停</button
                ><button class="small" @click="downloadAction(d, 'verify')">校验</button
                ><button class="small danger" @click="downloadAction(d, 'remove_keep_data')">
                  移除任务
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!data?.intents?.length && !loading" class="empty-state">
      <span>↓</span>
      <h3>还没有本程序发起的下载</h3>
      <p>在影视详情里搜索资源，核对预览后即可开始。</p>
      <button @click="go('discover')">发现影视</button>
    </div>
    <details v-for="d in data?.instances || []" :key="d.id">
      <summary>{{ d.id }} 实例中的其他任务（只读）</summary>
      <div class="table-wrap">
        <table>
          <tbody>
            <tr v-for="t in d.tasks || []" :key="t.id">
              <td>{{ t.name }}</td>
              <td>{{ label(t.status) }}</td>
              <td>{{ Math.round(t.progress * 100) }}%</td>
              <td>{{ bytes(t.total_size) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details></template
  >
  <template v-else-if="view === 'organize'"
    ><section class="panel">
      <div class="section-heading">
        <h2><span class="step">1</span>选择源文件</h2>
      </div>
      <form class="inline-form" @submit.prevent="scan">
        <input
          v-model="path"
          placeholder="允许目录内的绝对路径，例如 /media/download/电视剧"
          required
        /><label class="check"><input v-model="recursive" type="checkbox" />递归扫描</label
        ><button class="primary" :disabled="ui.busy > 0">扫描文件</button>
      </form>
      <p class="hint">
        允许扫描：{{
          (ui.settings?.allowed_roots || []).join('、') || '尚未设置，请先在设置中添加目录'
        }}
      </p>
      <p v-if="scanTruncated" class="warning">扫描结果已截断，请缩小源目录后重新扫描。</p>
      <div v-if="scanned.length" class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>选择</th>
              <th>源文件</th>
              <th>体积</th>
              <th>原识别 / 人工修正</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in scanned" :key="f.path">
              <td><input type="checkbox" v-model="selectedPaths" :value="f.path" /></td>
              <td class="path-cell">
                {{ f.path }}<small>{{ f.parsed?.reason }}</small
                ><small v-if="f.parsed?.original?.form === 'absolute_episode'" class="warning"
                  >绝对集号：请明确填写标准季并核对集号</small
                >
                <small v-if="f.inferred_media">
                  NFO 提示：{{ f.inferred_media.title || 'TMDB ' + f.inferred_media.id }} ·
                  {{ f.inferred_media.media_type }} {{ f.inferred_media.id }}
                  <button class="small" @click="adoptNFO(f)">采用 NFO 识别</button>
                </small>
                <small v-if="f.identity_reason" class="warning">{{ f.identity_reason }}</small>
              </td>
              <td>{{ bytes(f.size) }}</td>
              <td>
                <div class="inline-form">
                  <label
                    >季<input
                      v-model.number="f.manual_season"
                      type="number"
                      min="0"
                      placeholder="待确认" /></label
                  ><label>集<input v-model="f.manual_episodes" placeholder="1,2,5-8" /></label>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <section class="panel">
      <h2><span class="step">2</span>确认身份与目标</h2>
      <form class="inline-form" @submit.prevent="find">
        <select v-model="lookupKind">
          <option value="tv">剧集</option>
          <option value="movie">电影</option></select
        ><input v-model="lookup" placeholder="搜索正确的影视作品" required /><button>
          搜索 TMDB
        </button>
      </form>
      <div class="lookup-list">
        <button v-for="m in lookupResults" :key="m.id" @click="pick(m)">
          {{ m.title }} <span>{{ m.year }} · TMDB {{ m.id }}</span>
        </button>
      </div>
      <div v-if="media" class="notice">
        <strong>{{ media.title }}</strong> · {{ media.year }} · {{ label(media.media_type) }} · TMDB
        {{ media.id }}
      </div>
      <div class="form-grid">
        <label
          >入库目录<select v-model="root">
            <option v-for="r in ui.settings?.roots || []" :key="r.id" :value="r.id">
              {{ r.name || r.path }}
            </option>
          </select></label
        ><label
          >整理方式<select v-model="mode">
            <option value="hardlink">硬链接</option>
            <option value="copy">复制</option>
            <option value="auto">自动（允许复制回退）</option>
          </select></label
        >
      </div>
      <button
        class="primary"
        :disabled="!media || !selectedPaths.length || !root || ui.busy > 0"
        @click="makePlan"
      >
        生成整理预览
      </button>
    </section>
    <section v-if="plan" class="panel">
      <div class="section-heading">
        <h2><span class="step">3</span>核对整理预览</h2>
        <span class="badge">{{ plan.id }}</span>
      </div>
      <p class="notice">
        预计新增占用 {{ bytes(plan.space_needed) }} · 目标可用空间 {{ bytes(plan.space_available) }}
      </p>
      <div v-if="plan.warnings?.length" class="warning">
        <p v-for="warning in plan.warnings" :key="warning">{{ friendly(warning) }}</p>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th></th>
              <th>源路径 → 目标路径</th>
              <th>实际方式 / 大小</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in plan.files" :key="f.id">
              <td>
                <input
                  type="checkbox"
                  v-model="planSelected"
                  :value="f.id"
                  :disabled="f.status === 'blocked'"
                />
              </td>
              <td class="path-cell">
                {{ f.source }}<small>→ {{ f.target }}</small>
              </td>
              <td>
                {{ label(f.method) }}<small>{{ bytes(f.size) }}</small>
              </td>
              <td>
                {{ label(f.status) }}<small class="error-text">{{ f.reason }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="hint">只执行勾选文件。执行前服务端会复核源文件状态与目标冲突。</p>
      <button class="primary" :disabled="!planSelected.length || ui.busy > 0" @click="execute">
        确认执行 {{ planSelected.length }} 个文件
      </button>
    </section>
    <details v-if="plans.length">
      <summary>历史整理计划</summary>
      <div class="table-wrap">
        <table>
          <tbody>
            <tr v-for="p in plans" :key="p.id">
              <td>{{ p.media?.title }}</td>
              <td>{{ p.mode }}</td>
              <td>{{ p.files?.length }} 个文件</td>
              <td>{{ label(p.state) }}</td>
              <td>{{ date(p.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details></template
  >
  <template v-else-if="view === 'library'"
    ><div class="toolbar">
      <input v-model="assetQuery" placeholder="筛选片名或文件路径" /><select
        v-model="root"
        @change="loadCandidates()"
      >
        <option v-for="r in ui.settings?.roots || []" :key="r.id" :value="r.id">
          {{ r.name || r.path }}
        </option></select
      ><button @click="libraryScan">核对库内文件</button>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>影视与文件</th>
            <th>资产状态</th>
            <th>刮削 / 飞牛</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in filteredAssets" :key="a.id">
            <td class="resource-title">
              <strong>{{ a.media?.title }}</strong
              ><small
                >{{ a.season ? 'S' + a.season : '' }}
                {{ (a.episodes || []).map((e: number) => 'E' + e).join(' ') }} ·
                {{ bytes(a.size) }}</small
              ><small class="path-cell">{{ a.path }}</small
              ><small v-if="a.error" class="error-text">{{ a.error }}</small>
              <small v-if="a.identity_source === 'manual'" class="hint"
                >人工登记于 {{ date(a.identity_confirmed_at) }} · 原视频与旁车保持不变</small
              >
            </td>
            <td>
              <span class="badge">{{ label(a.state) }}</span
              ><small v-if="a.missing_seen_at">{{ date(a.missing_seen_at) }} 首次发现缺失</small>
            </td>
            <td>
              {{ label(a.scrape_state) }}<small>{{ label(a.fnos_state) }}</small>
            </td>
            <td>
              <div class="button-row">
                <button class="small" @click="assetAction(a, 'scrape')">补刮</button
                ><button class="small" @click="assetAction(a, 'sync')">同步飞牛</button
                ><button class="small" @click="assetAction(a, 'check')">核对</button
                ><button class="small danger" @click="inspectRemove(a)">移除</button>
              </div>
              <div
                v-if="
                  a.missing_seen_at ||
                  [
                    'removed',
                    'missing_confirmed',
                    'missing_pending',
                    'missing_suspected',
                    'missing',
                  ].includes(a.state)
                "
                class="button-row"
              >
                <button
                  v-if="a.state !== 'removed'"
                  class="small"
                  @click="resolve(a, 'keep_waiting')"
                >
                  继续等待</button
                ><button
                  v-if="a.state !== 'removed'"
                  class="small"
                  @click="resolve(a, 'confirm_missing')"
                >
                  确认缺失</button
                ><button class="small" @click="resolve(a, 'allow_reacquire')">允许重新获取</button>
              </div>
              <small v-if="a.state === 'removed'" class="hint"
                >已移除资产默认不再获取；点击允许重新获取才会解除排除。</small
              >
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <section class="panel">
      <div class="section-heading">
        <h2>待识别的库内文件</h2>
        <button @click="loadCandidates()" :disabled="candidateLoading">刷新候选</button>
      </div>
      <p class="hint">
        先核对库内文件；缺少可确认身份的文件会进入这里。人工登记只建立外部索引，不移动、覆盖、刮削或自动下载。
      </p>
      <p v-if="candidateError" class="error-box">{{ candidateError }}</p>
      <div v-for="candidate in candidates" :key="candidate.id" class="config-item">
        <p class="path-cell">{{ candidate.path }}</p>
        <p class="hint">
          {{ bytes(candidate.snapshot?.size) }} · {{ candidate.reason || candidate.reason_code }} ·
          {{ date(candidate.detected_at) }}
        </p>
        <button class="small primary" @click="beginRegistration(candidate)">
          人工确认身份并登记
        </button>
      </div>
      <p v-if="!candidates.length && !candidateLoading && !candidateError" class="muted">
        当前目录没有待确认文件。
      </p>
      <button v-if="candidateCursor" :disabled="candidateLoading" @click="loadCandidates(true)">
        加载更多待确认文件
      </button>
    </section>
    <div v-if="!filteredAssets.length && !loading" class="empty-state">
      <span>▤</span>
      <h3>还没有登记的媒体资产</h3>
      <p>完成整理或确认现有库文件身份后，资产会显示在这里。</p>
    </div></template
  >
  <template v-else-if="view === 'jobs'"
    ><div class="table-wrap job-queue-table">
      <table>
        <thead>
          <tr>
            <th>任务与对象</th>
            <th>状态与阶段</th>
            <th>执行时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="j in queueJobs" :key="j.id">
            <td class="job-object">
              <button class="text-button job-title" @click="inspectJob(j)">
                {{ j.display.title }}
              </button>
              <small v-if="j.display.showSubject">{{ j.display.subject }}</small>
              <small v-if="j.display.extra">{{ j.display.extra }}</small>
              <small v-if="j.display.source" class="job-path" :title="j.display.source">
                源：{{ j.display.source }}
              </small>
              <small v-if="j.display.target" class="job-path" :title="j.display.target">
                目标：{{ j.display.target }}
              </small>
            </td>
            <td class="job-progress">
              <span :class="['badge', { bad: j.state === 'failed' }]">{{ label(j.state) }}</span
              ><small class="job-stage">{{ j.display.stage }}</small>
              <small v-if="j.display.activity" class="job-activity">
                最近记录：{{ j.display.activity }}
                <span v-if="j.display.activityAt"> · {{ date(j.display.activityAt) }}</span>
              </small>
              <small v-if="j.error_message || j.error_code" class="error-text">
                {{ j.error_message || j.error_code }}
              </small>
            </td>
            <td class="job-timing">
              <small>更新：{{ date(j.updated_at) }}</small>
              <small v-if="['queued', 'retry_wait', 'waiting_paused'].includes(j.state)">
                计划：{{ date(j.run_at * 1000) }}
              </small>
              <small>{{ j.attempt > 0 ? `已尝试 ${j.attempt} 次` : '尚未执行' }}</small>
            </td>
            <td>
              <div class="button-row">
                <button class="small" @click="inspectJob(j)">详情</button
                ><button
                  class="small"
                  @click="jobAction(j, 'retry')"
                  :disabled="!['failed', 'cancelled', 'retry_wait'].includes(j.state)"
                >
                  重试</button
                ><button
                  class="small danger"
                  @click="jobAction(j, 'cancel')"
                  :disabled="
                    !['queued', 'running', 'retry_wait', 'waiting_paused'].includes(j.state)
                  "
                >
                  取消
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!data?.length && !loading" class="empty-state">
      <h3>当前没有任务</h3>
      <p>下载、订阅检查、整理和刮削任务会在这里记录。</p>
    </div></template
  >
  <template v-else-if="view === 'logs'"
    ><section class="panel">
      <form class="form-grid" @submit.prevent="load()">
        <label
          >级别<select v-model="logLevel">
            <option value="">全部级别</option>
            <option>DEBUG</option>
            <option>INFO</option>
            <option>WARN</option>
            <option>ERROR</option>
          </select></label
        ><label
          >站点<select v-model="logSite">
            <option value="">全部站点</option>
            <option v-for="s in ui.settings?.sites || []" :key="s.id" :value="s.id">
              {{ s.name || s.id }}
            </option>
          </select></label
        ><label>模块<input v-model="logModule" placeholder="如 import、download、fnos" /></label
        ><label>任务 ID<input v-model="logJob" placeholder="精确关联任务" /></label
        ><label
          >关键词 / 请求 ID<input v-model="logQuery" placeholder="日志内容、请求或流程 ID" /></label
        ><label>开始时间<input v-model="logFrom" type="datetime-local" /></label
        ><label>结束时间<input v-model="logTo" type="datetime-local" /></label>
        <div class="button-row">
          <button class="primary">查询日志</button
          ><a class="button" :href="apiURL('/logs/export?' + logParams())" download>导出当前筛选</a
          ><label class="check"
            ><input v-model="live" type="checkbox" @change="stream" />实时跟随</label
          >
        </div>
      </form>
      <p class="hint">
        导出文件末尾包含 export_meta，说明查询是否完整及后续游标；日志展示最多保留 2000 行。
      </p>
      <p v-if="logGap" class="warning">{{ logGap }}</p>
      <p
        v-if="data?.health?.status === 'failed' || data?.health?.error || data?.health?.write_error"
        class="error-box"
      >
        日志写入异常：{{
          data.health.write_error || data.health.error || '请检查磁盘空间与写入权限'
        }}
      </p>
      <p v-if="data?.complete === false" class="notice">本次日志尚未查完，可继续向前加载。</p>
      <p v-if="data?.health?.stdout_error" class="error-box">
        标准输出异常：{{ data.health.stdout_error }}
      </p>
      <p v-if="data?.health?.cleanup_error" class="error-box">
        日志清理异常：{{ data.health.cleanup_error }}
      </p>
      <p v-if="data?.health?.retention_error" class="error-box">
        日志保留策略执行异常：{{ data.health.retention_error }}
      </p>
      <p v-if="data?.oldest_time" class="hint">
        本次已查至 {{ date(data.oldest_time) }} · 扫描 {{ bytes(data.scanned_bytes) }}
      </p>
    </section>
    <div class="log-view">
      <div
        v-for="(entry, i) in data?.entries || []"
        :key="entry.seq || i"
        :class="['log-line', String(entry.level).toLowerCase()]"
      >
        <time>{{ date(entry.time) }}</time
        ><b>{{ entry.level }}</b
        ><span class="log-module">{{ entry.module }}</span
        ><span
          >{{ entry.message || entry.event
          }}<small v-if="entry.duration_ms !== undefined"
            >HTTP {{ entry.status_code || entry.http_status || entry.status || '—' }} ·
            {{ entry.duration_ms }} ms</small
          ><small v-if="entry.job_id || entry.workflow_id || entry.request_id"
            >任务 {{ entry.job_id || '—' }} · 流程 {{ entry.workflow_id || '—' }} · 请求
            {{ entry.request_id || '—' }}</small
          ></span
        >
      </div>
      <div v-if="!data?.entries?.length && !loading" class="empty-state">
        <h3>当前筛选没有日志</h3>
        <p>调整条件，或开启实时跟随等待新事件。</p>
      </div>
    </div>
    <button
      v-if="data?.next_cursor"
      :disabled="loading || (data.entries?.length || 0) >= 2000"
      @click="moreLogs"
    >
      继续加载更早日志
    </button>
  </template>
  <DownloadReview
    v-if="downloadReview"
    :intent="downloadReview"
    @close="downloadReview = null"
    @completed="load()"
  />
  <div v-if="registration" class="modal-backdrop">
    <section class="modal wide">
      <div class="section-heading">
        <h2>人工登记外部文件</h2>
        <button
          class="icon-button"
          @click="
            registration = null;
            registrationGeneration++;
          "
        >
          ×
        </button>
      </div>
      <p class="path-cell">{{ registration.path }}</p>
      <form class="inline-form" @submit.prevent="find">
        <select v-model="lookupKind">
          <option value="tv">剧集</option>
          <option value="movie">电影</option></select
        ><input v-model="lookup" required placeholder="搜索正确的影视作品" /><button>
          搜索 TMDB
        </button>
      </form>
      <div class="lookup-list">
        <button v-for="m in lookupResults" :key="m.media_type + ':' + m.id" @click="pick(m)">
          {{ m.title }} <span>{{ m.year }} · {{ label(m.media_type) }} · TMDB {{ m.id }}</span>
        </button>
      </div>
      <div v-if="media" class="notice">
        已选择 {{ media.title }} · {{ media.year }} · TMDB {{ media.id }}
      </div>
      <div v-if="media?.media_type === 'tv'" class="form-grid">
        <label
          >标准季<select v-model.number="registrationSeason" @change="loadRegistrationSeason">
            <option
              v-for="s in media.seasons?.filter((s: any) => s.season_number > 0) || []"
              :key="s.season_number"
              :value="s.season_number"
            >
              {{ s.name || '第 ' + s.season_number + ' 季' }}
            </option>
          </select></label
        >
        <label
          >标准集<select
            v-model="registrationEpisode"
            :disabled="registrationSeasonLoading || !registrationSeasonData"
          >
            <option value="">请明确选择单集</option>
            <option
              v-for="ep in registrationSeasonData?.episodes || []"
              :key="ep.episode_number"
              :value="ep.episode_number"
            >
              E{{ ep.episode_number }} · {{ ep.name }}
            </option>
          </select></label
        >
        <button
          v-if="!registrationSeasonData && !registrationSeasonLoading"
          @click="loadRegistrationSeason"
        >
          重试加载官方季集
        </button>
      </div>
      <p class="warning">
        只登记当前文件的身份与季集，不改动视频、NFO、字幕，也不会触发整理、刮削或下载。文件已变化时必须重新扫描。
      </p>
      <p v-if="registrationError" class="error-box">{{ registrationError }}</p>
      <label class="check"
        ><input
          v-model="registrationConfirmed"
          type="checkbox"
        />我已核对当前文件与所选影视、标准季集一致</label
      >
      <div class="button-row">
        <button
          @click="
            registration = null;
            registrationGeneration++;
          "
        >
          取消</button
        ><button
          class="primary"
          :disabled="
            !registrationConfirmed ||
            !media ||
            ui.busy > 0 ||
            (media.media_type === 'tv' &&
              (!registrationEpisode || !registrationSeasonData || registrationSeasonLoading))
          "
          @click="registerAsset"
        >
          确认登记此文件
        </button>
      </div>
    </section>
  </div>
  <div v-if="removePreview" class="modal-backdrop">
    <section class="modal">
      <div class="section-heading">
        <h2>移除自管库文件</h2>
        <button class="icon-button" @click="removePreview = null">×</button>
      </div>
      <p class="warning">
        以下文件将被删除。下载源和下载器内的原始数据会保留，默认不触发重新获取。
      </p>
      <pre class="code-box">{{ JSON.stringify(removePreview.files, null, 2) }}</pre>
      <div class="button-row">
        <button @click="removePreview = null">取消</button
        ><button class="danger" @click="remove">确认移除列出的文件</button>
      </div>
    </section>
  </div>
  <div v-if="jobDetail" class="modal-backdrop" @click.self="jobDetail = null">
    <section class="modal wide job-detail">
      <div class="section-heading">
        <h2>{{ selectedJobInfo?.title }}</h2>
        <button class="icon-button" aria-label="关闭任务详情" @click="jobDetail = null">×</button>
      </div>
      <div class="notice job-detail-state">
        <span :class="['badge', { bad: jobDetail.job.state === 'failed' }]">
          {{ label(jobDetail.job.state) }}
        </span>
        <span>{{ selectedJobInfo?.stage }}</span>
      </div>
      <dl class="job-facts">
        <dt>任务类型</dt>
        <dd>{{ selectedJobInfo?.typeLabel }}</dd>
        <dt>处理对象</dt>
        <dd>{{ selectedJobInfo?.subject }}</dd>
        <template v-if="selectedJobInfo?.source">
          <dt>源文件 / 目录</dt>
          <dd>{{ selectedJobInfo.source }}</dd>
        </template>
        <template v-if="selectedJobInfo?.target">
          <dt>目标文件 / 目录</dt>
          <dd>{{ selectedJobInfo.target }}</dd>
        </template>
        <template v-if="selectedJobInfo?.fileCount">
          <dt>文件数量</dt>
          <dd>{{ selectedJobInfo.fileCount }} 个</dd>
        </template>
        <template v-if="selectedJobInfo?.downloader">
          <dt>下载器</dt>
          <dd>{{ selectedJobInfo.downloader }}</dd>
        </template>
        <dt>更新时间</dt>
        <dd>{{ date(jobDetail.job.updated_at) }}</dd>
      </dl>
      <p v-if="jobDetail.job.error_message" class="error-box">{{ jobDetail.job.error_message }}</p>
      <h3>执行记录</h3>
      <p v-if="!jobDetail.events?.length" class="muted">此任务暂无执行记录。</p>
      <div v-for="e in jobDetail.events || []" :key="e.seq" class="timeline-entry">
        <small>{{ date(e.time) }} · {{ e.level }} · {{ e.module }}</small>
        <p>{{ e.message || e.event }}</p>
      </div>
      <details v-for="(decision, index) in jobDetail.decisions || []" :key="index">
        <summary>
          资源筛选记录 · {{ date(decision.time) }} · {{ decision.candidates?.length || 0 }} 个候选
        </summary>
        <div v-for="(candidate, n) in decision.candidates || []" :key="n" class="timeline-entry">
          <strong>{{ candidate.resource?.title }}</strong>
          <small
            >{{ candidate.resource?.site_id }} ·
            {{
              candidate.accepted
                ? candidate.needs_confirmation
                  ? '需要复核'
                  : '符合策略'
                : '已过滤'
            }}</small
          >
          <p v-for="reason in candidate.reasons || []" :key="reason">{{ friendly(reason) }}</p>
        </div>
        <p v-if="!decision.candidates?.length" class="muted">本轮没有资源候选。</p>
        <p v-for="site in decision.sites || []" :key="site.id" class="hint">
          {{ site.id }} · {{ label(site.status) }} · {{ friendly(site.message || '') }}
        </p>
      </details>
      <details class="job-technical">
        <summary>技术信息与任务参数</summary>
        <dl class="job-facts">
          <dt>任务 ID</dt>
          <dd>{{ jobDetail.job.id }}</dd>
          <dt>工作流 ID</dt>
          <dd>{{ jobDetail.job.workflow_id || '—' }}</dd>
          <dt>任务类型标识</dt>
          <dd>{{ jobDetail.job.kind }}</dd>
          <dt>执行次数</dt>
          <dd>{{ jobDetail.job.attempt }}</dd>
          <dt>创建时间</dt>
          <dd>{{ date(jobDetail.job.created_at) }}</dd>
        </dl>
        <pre class="code-box">{{ JSON.stringify(jobDetail.job.payload, null, 2) }}</pre>
      </details>
      <div class="button-row">
        <button @click="inspectJob(jobDetail.job)">刷新</button
        ><button @click="go('logs/' + encodeURIComponent(jobDetail.job.id))">查看关联日志</button
        ><button
          @click="jobAction(jobDetail.job, 'retry')"
          :disabled="!['failed', 'cancelled', 'retry_wait'].includes(jobDetail.job.state)"
        >
          重试任务</button
        ><button
          class="danger"
          @click="jobAction(jobDetail.job, 'cancel')"
          :disabled="
            !['queued', 'running', 'retry_wait', 'waiting_paused'].includes(jobDetail.job.state)
          "
        >
          取消任务
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.job-object {
  width: 41%;
  min-width: 240px;
  max-width: 450px;
}
.job-title {
  font-size: 13px;
  font-weight: 600;
  line-height: 1.7;
  text-align: left;
  overflow-wrap: anywhere;
}
.job-path {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.job-progress {
  min-width: 180px;
  max-width: 300px;
}
.job-stage {
  color: #43516a;
}
.job-activity {
  overflow-wrap: anywhere;
}
.job-timing {
  min-width: 155px;
}
.job-detail .section-heading {
  align-items: start;
}
.job-detail h2 {
  overflow-wrap: anywhere;
  line-height: 1.6;
}
.job-detail-state {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.job-facts {
  display: grid;
  grid-template-columns: 105px minmax(0, 1fr);
  gap: 10px 16px;
  margin: 20px 0;
  font-size: 12px;
  line-height: 1.7;
}
.job-facts dt {
  color: #69768a;
}
.job-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: #43516a;
}
.job-technical {
  margin-top: 18px;
}
@media (max-width: 760px) {
  .job-queue-table table,
  .job-queue-table tbody,
  .job-queue-table tr,
  .job-queue-table td {
    display: block;
    width: auto;
    min-width: 0;
    max-width: none;
  }
  .job-queue-table thead {
    display: none;
  }
  .job-queue-table tr {
    padding: 16px;
    border-bottom: 1px solid #edf0f5;
  }
  .job-queue-table tr:last-child {
    border-bottom: 0;
  }
  .job-queue-table td {
    padding: 0;
    border: 0;
  }
  .job-queue-table td + td {
    margin-top: 12px;
  }
  .job-timing small {
    display: inline;
    margin-right: 10px;
  }
  .job-facts {
    grid-template-columns: 82px minmax(0, 1fr);
    gap: 10px;
  }
}
</style>
