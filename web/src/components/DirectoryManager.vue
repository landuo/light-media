<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { api, action, clone, date, label, errorText, refreshSettings } from '../api';
const emit = defineEmits<{ organize: [paths: string[]] }>();
const fileRule = ref<any>(null),
  records = ref<any[]>([]),
  filesLoading = ref(false),
  filesError = ref(''),
  loadError = ref('');
const fileStates: Record<string, string> = {
  observing: '等待文件稳定',
  baseline: '已有文件（未自动整理）',
  downloading: '下载中',
  tracking: '由下载任务跟踪整理',
  needs_review: '需要人工识别',
  queued: '整理已排队',
  imported: '已整理',
  error: '处理失败',
};
async function showFiles(row: any) {
  fileRule.value = row;
  filesLoading.value = true;
  records.value = [];
  filesError.value = '';
  try {
    const result = await api('/directories/manage/' + encodeURIComponent(row.config.id) + '/files');
    if (result)
      records.value = Array.isArray(result) ? result : result.records || result.items || [];
  } catch (e) {
    filesError.value = errorText(e);
  } finally {
    filesLoading.value = false;
  }
}
const data = ref<any>({ directories: [], downloaders: [], roots: [] });
const editing = ref<any>(null),
  saving = ref(false),
  loading = ref(false),
  checking = ref<string[]>([]);
const modes: Record<string, string> = {
  monitor: '目录监听',
  downloader: '本应用下载完成监听',
  none: '不监控',
};
const roots = computed(() =>
  (data.value.roots || []).filter(
    (r: any) =>
      !editing.value?.media_type ||
      !r.kind ||
      r.kind === 'mixed' ||
      r.kind === editing.value.media_type,
  ),
);
async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    data.value = await api('/directories/manage');
  } catch (e) {
    loadError.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
function client(id: string) {
  return (
    data.value.downloaders.find((d: any) => (d.config || d).id === id)?.config ||
    data.value.downloaders.find((d: any) => d.id === id)
  );
}
function rootName(id: string) {
  const r = data.value.roots.find((r: any) => r.id === id);
  return r?.name || r?.path || id;
}
function begin(row?: any) {
  editing.value = row
    ? clone(row.config)
    : {
        id: '',
        name: '',
        downloader_id: '',
        download_path: '',
        source_path: '',
        root_id: '',
        media_type: 'tv',
        mode: 'hardlink',
        monitor_type: 'monitor',
        enabled: false,
        include_existing: false,
      };
}
async function save() {
  saving.value = true;
  try {
    const payload = clone(editing.value);
    if (!payload.downloader_id) payload.download_path = '';
    if (payload.monitor_type === 'none') payload.enabled = false;
    const result = await action(() => api('/directories/manage', payload), '目录规则已保存');
    if (result) {
      data.value = result;
      editing.value = null;
      await refreshSettings();
    }
  } finally {
    saving.value = false;
  }
}
async function toggle(row: any) {
  const enabled = !row.config.enabled;
  if (
    enabled &&
    !window.confirm(
      `启用「${row.config.name}」的${modes[row.config.monitor_type] || '自动整理'}？符合规则的文件将以${label(row.config.mode)}方式整理到「${rootName(row.config.root_id)}」，下载源文件会保留。`,
    )
  )
    return;
  const result = await action(
    () => api('/directories/manage', { ...row.config, enabled }),
    enabled ? '目录监听已启用' : '目录监听已暂停',
  );
  if (result) {
    data.value = result;
    await refreshSettings();
  }
}
async function remove(row: any) {
  if (
    !window.confirm(
      `删除「${row.config.name}」的目录规则？不会删除下载器任务、源文件或媒体库文件。`,
    )
  )
    return;
  const result = await action(
    () => api('/directories/manage/' + encodeURIComponent(row.config.id), undefined, 'DELETE'),
    '目录规则已删除',
  );
  if (result) {
    data.value = result;
    await refreshSettings();
  }
}
async function check(row: any) {
  checking.value.push(row.config.id);
  try {
    const result = await action(
      () => api('/directories/manage/' + encodeURIComponent(row.config.id) + '/check', {}),
      '目录检查已执行',
    );
    if (result) data.value = result;
  } finally {
    checking.value = checking.value.filter((id) => id !== row.config.id);
  }
}
watch(
  () => editing.value?.media_type,
  () => {
    if (editing.value?.root_id && !roots.value.some((r: any) => r.id === editing.value.root_id))
      editing.value.root_id = '';
  },
);
onMounted(load);
</script>
<template>
  <section>
    <header class="section-heading">
      <div>
        <h1>目录管理</h1>
        <p class="muted">下载到指定目录，再由监听任务识别、整理和刮削入库。</p>
      </div>
      <div class="button-row">
        <button :disabled="loading" @click="load">刷新</button
        ><button class="primary" @click="begin()">＋ 添加目录</button>
      </div>
    </header>
    <p v-if="loadError" class="error-box">{{ loadError }}</p>
    <div class="directory-flow panel">
      <span><b>01</b> 选择下载器与下载目录</span><span class="flow-arrow">→</span
      ><span><b>02</b> 监听文件或下载完成</span><span class="flow-arrow">→</span
      ><span><b>03</b> 整理到媒体库</span>
    </div>
    <div v-if="!loading && !loadError && !data.directories.length" class="empty-state panel">
      <h3>还没有目录整理规则</h3>
      <p>资源可以先下载。添加目录后，再启用对应监听任务自动整理。</p>
      <button @click="begin()">添加第一条目录规则</button>
    </div>
    <div class="directory-grid">
      <article v-for="row in data.directories" :key="row.config.id" class="panel directory-card">
        <header class="section-heading">
          <div>
            <h2>{{ row.config.name }}</h2>
            <p class="hint">
              {{ modes[row.config.monitor_type] || '未设置监听' }} ·
              {{ row.config.media_type === 'mixed' ? '电影与剧集' : label(row.config.media_type) }}
            </p>
          </div>
          <span
            class="badge"
            :class="{ green: row.config.enabled && row.config.monitor_type !== 'none' }"
            >{{
              row.config.enabled && row.config.monitor_type !== 'none' ? '监听已启用' : '未启用'
            }}</span
          >
        </header>
        <dl>
          <dt>下载器</dt>
          <dd>{{ client(row.config.downloader_id)?.name || '仅监听本地目录' }}</dd>
          <template v-if="row.config.download_path"
            ><dt>下载目录</dt>
            <dd>{{ row.config.download_path }}</dd></template
          >
          <dt>应用内源目录</dt>
          <dd>{{ row.application_path || row.config.source_path || '待映射' }}</dd>
          <dt>媒体库目录</dt>
          <dd>{{ rootName(row.config.root_id) }}</dd>
          <dt>整理方式</dt>
          <dd>{{ label(row.config.mode) }}<small>保留下载源文件</small></dd>
        </dl>
        <div class="directory-status">
          <span :class="{ 'error-text': ['failed', 'error'].includes(row.connection?.status) }">{{
            row.connection?.status === 'ok'
              ? '检查正常'
              : ['failed', 'error'].includes(row.connection?.status)
                ? '检查失败'
                : '尚未检查'
          }}</span
          ><span>待处理 {{ row.pending_count || 0 }} 项</span
          ><small>{{ date(row.connection?.checked_at) }}</small>
        </div>
        <p v-if="row.connection?.error" class="error-box">{{ row.connection.error }}</p>
        <footer class="button-row">
          <button class="small" @click="begin(row)">编辑</button
          ><button
            v-if="row.config.monitor_type === 'monitor'"
            class="small"
            @click="showFiles(row)"
          >
            监听文件</button
          ><button v-if="row.config.monitor_type !== 'none'" class="small" @click="toggle(row)">
            {{ row.config.enabled ? '暂停监听' : '启用监听' }}</button
          ><button
            class="small"
            :disabled="!row.config.enabled || checking.includes(row.config.id)"
            @click="check(row)"
          >
            {{ checking.includes(row.config.id) ? '检查中…' : '立即检查' }}</button
          ><button class="small danger" @click="remove(row)">删除</button>
        </footer>
      </article>
    </div>
    <p class="hint">
      下载器路径和本应用路径不同时，请先在下载器设置中配置路径映射。媒体库目标在这里维护，下载和订阅只选择保存目录。
    </p>
    <div v-if="editing" class="modal-backdrop" @click.self="editing = null">
      <form
        class="modal wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="directory-title"
        @submit.prevent="save"
      >
        <header class="section-heading">
          <h2 id="directory-title">{{ editing.id ? '编辑目录规则' : '添加目录规则' }}</h2>
          <button class="icon-button" type="button" aria-label="关闭" @click="editing = null">
            ×
          </button>
        </header>
        <div class="form-grid">
          <label
            >名称<input
              v-model.trim="editing.name"
              required
              maxlength="80"
              placeholder="例如：剧集下载"
          /></label>
          <label
            >媒体类型<select v-model="editing.media_type">
              <option value="mixed">电影与剧集</option>
              <option value="movie">电影</option>
              <option value="tv">剧集</option>
            </select></label
          >
          <label
            >下载器<select
              v-model="editing.downloader_id"
              :required="editing.monitor_type === 'downloader'"
            >
              <option value="">不关联下载器（仅文件监听）</option>
              <option
                v-for="item in data.downloaders"
                :key="(item.config || item).id"
                :value="(item.config || item).id"
              >
                {{ (item.config || item).name }}
              </option>
            </select></label
          >
          <label
            >下载目录<input
              v-model.trim="editing.download_path"
              :required="!!editing.downloader_id"
              :disabled="!editing.downloader_id"
              placeholder="下载器看到的路径，例如 /downloads/tv"
            /><small class="hint">配置后可在资源下载、订阅中选择。</small></label
          >
          <label class="span-two"
            >应用内源目录<input
              v-model.trim="editing.source_path"
              :required="!editing.downloader_id"
              placeholder="留空则根据下载器路径映射推导"
            /><small class="hint"
              >光影容器可访问的目录，必须位于设置中允许扫描的源目录内。</small
            ></label
          >
          <label
            >媒体库目录<select v-model="editing.root_id" required>
              <option value="" disabled>选择整理目标</option>
              <option v-for="r in roots" :key="r.id" :value="r.id">
                {{ r.name || r.path }}
              </option></select
            ><small v-if="!roots.length" class="hint"
              >请先在设置 → 目录与映射添加媒体库目录。</small
            ></label
          >
          <label
            >整理方式<select v-model="editing.mode">
              <option value="hardlink">硬链接（同一文件系统）</option>
              <option value="copy">复制</option>
              <option value="auto">自动（跨卷时复制）</option>
            </select></label
          >
          <label
            >监听方式<select v-model="editing.monitor_type">
              <option value="monitor">目录监听</option>
              <option value="downloader">本应用下载完成监听</option>
              <option value="none">不监控</option>
            </select></label
          >
          <label class="check"
            ><input
              v-model="editing.enabled"
              type="checkbox"
              :disabled="editing.monitor_type === 'none'"
            />保存后启用自动整理</label
          >
        </div>
        <label v-if="editing.monitor_type === 'monitor'" class="check"
          ><input
            v-model="editing.include_existing"
            type="checkbox"
          />首次启用时也整理此目录中已有的文件</label
        >
        <p class="notice" v-if="editing.monitor_type === 'monitor'">
          目录监听跟踪源目录内的文件，稳定至少 60
          秒，并核对关联下载器的文件是否完成。默认保留首次扫描发现的已有文件；已有文件可从文件管理手动整理。
        </p>
        <p class="notice" v-else-if="editing.monitor_type === 'downloader'">
          检查本应用发起、已完成且位于此下载目录的任务，再识别和整理。其他程序下载的文件请使用「目录监听」。源文件保留在原下载位置继续做种。
        </p>
        <p class="notice" v-else>目录仅用于选择下载位置，不自动整理。</p>
        <p class="hint">
          新规则默认关闭。启用后会按规则在媒体库中写入整理文件和刮削信息；无法明确识别的文件会进入待处理状态。
        </p>
        <footer class="button-row">
          <button type="button" @click="editing = null">取消</button
          ><button class="primary" type="submit" :disabled="saving">
            {{ saving ? '保存中…' : '保存目录规则' }}
          </button>
        </footer>
      </form>
    </div>
    <div v-if="fileRule" class="modal-backdrop" @click.self="fileRule = null">
      <section class="modal wide" role="dialog" aria-modal="true" aria-label="目录监听文件">
        <header class="section-heading">
          <div>
            <h2>{{ fileRule.config.name }} · 监听文件</h2>
            <p class="hint">识别失败或需要确认的文件可在文件管理中手动整理。</p>
          </div>
          <button class="icon-button" aria-label="关闭" @click="fileRule = null">×</button>
        </header>
        <button :disabled="filesLoading" @click="showFiles(fileRule)">
          {{ filesLoading ? '读取中…' : '刷新文件状态' }}
        </button>
        <p v-if="filesError" class="error-box">{{ filesError }}</p>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>文件</th>
                <th>状态 / 原因</th>
                <th>最后观察</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="record in records" :key="record.path">
                <td class="path-cell">{{ record.path }}</td>
                <td>
                  {{ fileStates[record.state] || record.state
                  }}<small v-if="record.reason">{{ record.reason }}</small>
                </td>
                <td>{{ date(record.last_seen) }}</td>
                <td>
                  <button class="small" @click="emit('organize', [record.path])">文件管理</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!filesLoading && !filesError && !records.length" class="notice">
          尚无监听记录。启用后等待周期检查，或点击目录卡片上的「立即检查」。
        </p>
      </section>
    </div>
  </section>
</template>
<style scoped>
.directory-flow {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  padding: 18px 22px;
  margin: 20px 0;
  font-size: 13px;
}
.directory-flow b {
  color: var(--accent);
  padding-right: 7px;
  font-size: 11px;
}
.flow-arrow {
  color: var(--muted);
}
.directory-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 440px), 1fr));
  gap: 18px;
}
.directory-card {
  padding: 22px;
  min-width: 0;
}
.directory-card h2 {
  font-size: 17px;
}
.directory-card dl {
  display: grid;
  grid-template-columns: 105px minmax(0, 1fr);
  gap: 12px;
  margin: 20px 0;
  font-size: 13px;
}
.directory-card dt {
  color: var(--muted);
}
.directory-card dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.directory-card dd small {
  margin-left: 10px;
  color: var(--muted);
}
.directory-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 12px;
  padding-top: 15px;
  border-top: 1px solid var(--line);
}
.directory-status small {
  margin-left: auto;
  color: var(--muted);
}
.directory-card footer {
  margin-top: 18px;
}
.span-two {
  grid-column: 1/-1;
}
</style>
