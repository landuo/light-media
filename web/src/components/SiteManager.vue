<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import {
  action,
  api,
  bytes,
  clone,
  date,
  errorText,
  friendly,
  refreshSettings,
  toast,
} from '../api';
import {
  confirmedSiteImport,
  downloadSiteExport,
  readSiteImport,
  type SiteImportResult,
} from '../features/site-transfer';

const data = ref<any>({ sites: [], supported: [] });
const filter = ref('all'),
  query = ref(''),
  editing = ref<any>(null),
  original = ref<any>(null);
const loading = ref(false),
  saving = ref(false),
  refreshing = ref<string[]>([]),
  history = ref<any>(null);
const importing = ref(false),
  exporting = ref(false),
  transferError = ref('');
const importFile = ref<HTMLInputElement>();
const importResult = ref<SiteImportResult | null>(null);
const mutations = ref(0);
const transferBusy = computed(() => importing.value || exporting.value);
const transferBlocked = computed(
  () =>
    transferBusy.value ||
    loading.value ||
    saving.value ||
    refreshing.value.length > 0 ||
    mutations.value > 0 ||
    !!editing.value,
);
const importSummary = computed(() =>
  importResult.value
    ? `导入结果：新增 ${importResult.value.created}，更新 ${importResult.value.updated}，跳过 ${importResult.value.skipped}，失败 ${importResult.value.failed}`
    : '',
);
const importStatus: Record<string, string> = {
  created: '已新增',
  updated: '已更新',
  skipped: '已跳过',
  failed: '失败',
};
const filtered = computed(() =>
  data.value.sites.filter((row: any) => {
    if (filter.value === 'enabled' && !row.config.enabled) return false;
    if (filter.value === 'disabled' && row.config.enabled) return false;
    if (filter.value === 'failed' && row.connection.status !== 'failed') return false;
    return (row.config.name + row.config.base_url)
      .toLowerCase()
      .includes(query.value.toLowerCase());
  }),
);
const totals = computed(() => {
  const stats = data.value.sites.map((s: any) => s.stats).filter(Boolean);
  return ['uploaded', 'downloaded', 'seeding_bytes', 'seeding'].map((key) => ({
    key,
    observed: stats.filter((s: any) => s[key] != null).length,
    value: stats.reduce((n: number, s: any) => n + (s[key] ?? 0), 0),
  }));
});
const fieldNames: Record<string, string> = {
  uploaded: '累计上传',
  downloaded: '累计下载',
  seeding_bytes: '做种体积',
  seeding: '做种数量',
  ratio: '分享率',
  bonus: '积分 / 魔力',
  leeching: '下载中',
  level: '用户等级',
  joined_at: '加入时间',
};
function value(stats: any, key: string) {
  if (key === 'ratio' && stats?.ratio_infinite) return '∞';
  const n = stats?.[key];
  if (n == null || n === '') return '未提供';
  if (['uploaded', 'downloaded', 'seeding_bytes'].includes(key)) return bytes(n);
  if (typeof n === 'number')
    return n.toLocaleString('zh-CN', { maximumFractionDigits: key === 'ratio' ? 3 : 2 });
  return n;
}
async function load() {
  if (transferBusy.value) return;
  loading.value = true;
  try {
    const result = await action(() => api('/sites/manage'));
    if (result) data.value = result;
  } finally {
    loading.value = false;
  }
}
function create() {
  if (transferBusy.value) return;
  original.value = null;
  editing.value = {
    id: '',
    kind: 'mteam',
    name: '馒头',
    base_url: 'https://api.m-team.cc',
    cookie: '',
    api_key: '',
    enabled: true,
    daily_budget: 600,
  };
}
function edit(row: any) {
  if (transferBusy.value) return;
  original.value = row;
  editing.value = clone(row.config);
}
function template() {
  const t = data.value.supported.find((s: any) => s.kind === editing.value.kind);
  if (t) {
    editing.value.name = t.name;
    editing.value.base_url = t.base_url;
    editing.value.cookie = '';
    editing.value.api_key = '';
  }
}
async function save() {
  if (transferBusy.value) return;
  saving.value = true;
  try {
    const result = await action(() => api('/sites/manage', editing.value), '站点已保存');
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
  if (transferBusy.value) return;
  mutations.value++;
  try {
    const result = await action(
      () => api('/sites/manage', { ...row.config, enabled: !row.config.enabled }),
      row.config.enabled ? '站点已停用' : '站点已启用',
    );
    if (result) {
      data.value = result;
      await refreshSettings();
    }
  } finally {
    mutations.value--;
  }
}
async function remove(row: any) {
  if (transferBusy.value) return;
  if (
    !window.confirm(
      `删除「${row.config.name}」的连接配置和本应用统计记录？站点账户及下载器中的任务不会被删除。`,
    )
  )
    return;
  mutations.value++;
  try {
    const result = await action(
      () => api('/sites/manage/' + encodeURIComponent(row.config.id), undefined, 'DELETE'),
      '站点已删除',
    );
    if (result) {
      data.value = result;
      await refreshSettings();
    }
  } finally {
    mutations.value--;
  }
}
async function refresh(row: any) {
  if (transferBusy.value) return;
  const id = row.config.id;
  if (refreshing.value.includes(id)) return;
  refreshing.value.push(id);
  try {
    const result = await action(() =>
      api('/sites/manage/' + encodeURIComponent(id) + '/refresh', {}),
    );
    if (result) {
      data.value = result;
      const updated = result.sites.find((s: any) => s.config.id === id);
      if (updated?.connection.status === 'ok') toast(`${row.config.name} 数据已刷新`);
      else toast(`${row.config.name}：${friendly(updated?.connection.error || '刷新失败')}`, true);
    }
  } finally {
    refreshing.value = refreshing.value.filter((v) => v !== id);
  }
}
async function refreshAll() {
  if (transferBusy.value) return;
  for (const row of data.value.sites.filter((s: any) => s.config.enabled)) await refresh(row);
}
async function openHistory(row: any) {
  const result = await action(() =>
    api('/sites/manage/' + encodeURIComponent(row.config.id) + '/history'),
  );
  if (result) history.value = { ...result, name: row.config.name };
}
function homepage(row: any) {
  return row.config.kind === 'mteam'
    ? row.config.base_url.includes('.io')
      ? 'https://kp.m-team.io'
      : 'https://kp.m-team.cc'
    : row.config.base_url;
}
async function importSites(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file || transferBlocked.value) return;
  importing.value = true;
  transferError.value = '';
  importResult.value = null;
  try {
    const payload = await readSiteImport(file);
    importResult.value = confirmedSiteImport(await api('/sites/import', payload));
    if (importResult.value.imported) {
      try {
        data.value = await api('/sites/manage');
        await refreshSettings();
      } catch (e) {
        transferError.value = '站点已导入，但刷新配置失败：' + errorText(e);
      }
    }
  } catch (e) {
    transferError.value = errorText(e);
  } finally {
    importing.value = false;
  }
}
async function exportSites() {
  if (transferBlocked.value) return;
  exporting.value = true;
  transferError.value = '';
  try {
    await downloadSiteExport();
    toast('站点配置已导出');
  } catch (e) {
    transferError.value = errorText(e);
  } finally {
    exporting.value = false;
  }
}
onMounted(load);
</script>

<template>
  <section class="site-manager">
    <header class="manager-heading">
      <div class="manager-actions">
        <input
          ref="importFile"
          type="file"
          accept=".json,application/json"
          aria-label="选择站点配置文件"
          hidden
          @change="importSites"
        />
        <button
          :disabled="transferBlocked"
          title="导入 MoviePilot 或本应用导出的站点配置"
          @click="importFile?.click()"
        >
          {{ importing ? '正在导入…' : '导入' }}
        </button>
        <button
          :disabled="transferBlocked || !data.sites.length"
          title="导出文件包含站点 Cookie / API Key，请妥善保管"
          @click="exportSites"
        >
          {{ exporting ? '正在导出…' : '导出' }}
        </button>
        <button :disabled="loading || refreshing.length > 0 || transferBusy" @click="refreshAll">
          刷新全部数据</button
        ><button class="primary" :disabled="transferBusy" @click="create">＋ 添加站点</button>
      </div>
    </header>
    <p v-if="transferError" class="site-transfer-error" role="alert">{{ transferError }}</p>
    <section v-if="importResult" class="site-import-result" aria-label="站点导入结果">
      <div class="import-result-heading">
        <strong role="status">{{ importSummary }}</strong
        ><button class="icon-button" aria-label="关闭导入结果" @click="importResult = null">
          ×
        </button>
      </div>
      <details
        :open="
          importResult.failed > 0 ||
          importResult.skipped > 0 ||
          importResult.results.some((row) => row.warnings.length)
        "
      >
        <summary>查看逐站结果</summary>
        <ul>
          <li
            v-for="row in importResult.results"
            :key="row.index"
            :class="{ 'site-warning': row.status === 'failed' || row.status === 'skipped' }"
          >
            <strong>{{ row.name }} · {{ importStatus[row.status] }}</strong
            ><span>{{ row.message }}</span
            ><small v-for="(warning, index) in row.warnings" :key="index">{{ warning }}</small>
          </li>
        </ul>
      </details>
    </section>
    <div class="site-totals">
      <div v-for="t in totals" :key="t.key">
        <span>{{ fieldNames[t.key] }}</span
        ><strong>{{
          t.observed ? (t.key === 'seeding' ? t.value.toLocaleString() : bytes(t.value)) : '未获取'
        }}</strong
        ><small>{{ t.observed }} / {{ data.sites.length }} 个站点提供数据</small>
      </div>
    </div>
    <div class="manager-filter">
      <input v-model="query" aria-label="查找站点" placeholder="搜索站点名称或地址" /><select
        v-model="filter"
        aria-label="站点状态"
      >
        <option value="all">全部站点</option>
        <option value="enabled">已启用</option>
        <option value="disabled">已停用</option>
        <option value="failed">连接异常</option></select
      ><button @click="load" :disabled="loading || transferBusy">重新加载</button>
    </div>
    <p v-if="!data.sites.length && !loading" class="manager-empty">
      尚未添加站点。点击「添加站点」，选择原生适配并填写 Cookie 或 API Key。
    </p>
    <div class="site-grid">
      <article
        v-for="row in filtered"
        :key="row.config.id"
        class="site-card"
        :class="{ inactive: !row.config.enabled }"
      >
        <div class="site-card-title">
          <span class="site-monogram">{{ row.config.name.slice(0, 1) }}</span>
          <div>
            <h2>
              <a :href="homepage(row)" target="_blank" rel="noopener noreferrer"
                >{{ row.config.name }} ↗</a
              >
            </h2>
            <small>{{ row.config.base_url }}</small>
          </div>
          <span class="site-badge" :class="row.config.enabled ? row.connection.status : ''">{{
            !row.config.enabled
              ? '已停用'
              : row.connection.status === 'ok'
                ? '连接正常'
                : row.connection.status === 'failed'
                  ? '连接失败'
                  : '未检测'
          }}</span>
        </div>
        <div v-if="row.stats" class="site-account">
          <strong>{{ row.stats.username || '已认证账户' }}</strong
          ><span>{{ row.stats.level || '等级未提供' }}</span
          ><span v-if="row.stale" class="site-warning">显示上次成功数据</span>
        </div>
        <div class="site-traffic">
          <div v-for="key in ['uploaded', 'downloaded']" :key="key">
            <small>{{ fieldNames[key] }}</small
            ><strong :title="row.stats?.sources?.[key]">{{ value(row.stats, key) }}</strong>
          </div>
        </div>
        <dl class="site-metrics">
          <div v-for="key in ['ratio', 'seeding', 'seeding_bytes', 'bonus', 'leeching']" :key="key">
            <dt>{{ fieldNames[key] }}</dt>
            <dd :title="row.stats?.sources?.[key]">{{ value(row.stats, key) }}</dd>
          </div>
        </dl>
        <p v-if="row.connection.error" class="site-warning">{{ friendly(row.connection.error) }}</p>
        <details v-if="row.stats?.warnings?.length" class="site-warning">
          <summary>部分字段未完整获取</summary>
          <p v-for="warning in row.stats.warnings" :key="warning">{{ friendly(warning) }}</p>
        </details>
        <small class="site-updated"
          >数据：{{ date(row.stats?.captured_at) }} · 检测
          {{
            row.connection.duration_ms
              ? (row.connection.duration_ms / 1000).toFixed(2) + ' 秒'
              : '—'
          }}<br />近 24 小时请求 {{ row.usage_24h }} / {{ row.config.daily_budget || 600 }}</small
        >
        <footer class="manager-actions">
          <button
            class="small"
            :disabled="!row.config.enabled || refreshing.includes(row.config.id) || transferBusy"
            @click="refresh(row)"
          >
            {{ refreshing.includes(row.config.id) ? '正在刷新…' : '检测 / 刷新' }}</button
          ><button class="small" @click="openHistory(row)">历史数据</button
          ><button class="small" :disabled="transferBusy" @click="edit(row)">编辑</button
          ><button class="small" :disabled="transferBusy" @click="toggle(row)">
            {{ row.config.enabled ? '停用' : '启用' }}</button
          ><button class="small danger" :disabled="transferBusy" @click="remove(row)">删除</button>
        </footer>
      </article>
    </div>
    <p class="manager-note">
      数据只读获取，不执行签到、站内消息已读或账户修改。未提供的数值保持未知；历史按日保留最近 90
      天的成功快照。
    </p>
    <div v-if="editing" class="manager-backdrop" @click.self="editing = null">
      <form
        class="manager-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="site-form-title"
        @submit.prevent="save"
      >
        <header>
          <h2 id="site-form-title">{{ original ? '编辑站点' : '添加站点' }}</h2>
          <button type="button" class="icon-button" aria-label="关闭" @click="editing = null">
            ×
          </button>
        </header>
        <div class="manager-form-grid">
          <label
            >站点适配<select v-model="editing.kind" @change="template" :disabled="!!original">
              <option v-for="t in data.supported" :key="t.kind" :value="t.kind">
                {{ t.name }}
              </option>
            </select></label
          ><label>显示名称<input v-model.trim="editing.name" required maxlength="64" /></label
          ><label class="span-two"
            >站点地址<input v-model.trim="editing.base_url" type="url" required /><small
              >馒头可以填写前台或 API 地址；家园为 hdhome.org。</small
            ></label
          ><label v-if="editing.kind === 'mteam'" class="span-two"
            >API Key<input
              v-model="editing.api_key"
              type="password"
              autocomplete="new-password"
              :placeholder="
                original?.credential_configured ? '已保存，留空保留原凭据' : '填写站点 API Key'
              " /></label
          ><label v-else class="span-two"
            >Cookie<textarea
              v-model="editing.cookie"
              rows="3"
              autocomplete="off"
              :placeholder="
                original?.credential_configured
                  ? '已保存，留空保留原凭据'
                  : '填写浏览器登录后的 Cookie'
              "
            ></textarea></label
          ><label
            >近 24 小时请求预算<input
              v-model.number="editing.daily_budget"
              type="number"
              min="1"
              max="10000"
              required /></label
          ><label class="manager-checkbox"
            ><input v-model="editing.enabled" type="checkbox" />启用站点</label
          >
        </div>
        <p class="manager-note">
          当前支持上方五个原生站点；未知站点不会冒充适配。凭据加密保存，编辑时不会回显；更换域名需重新填写凭据。
        </p>
        <footer class="manager-actions">
          <button type="button" @click="editing = null">取消</button
          ><button type="submit" class="primary" :disabled="saving">
            {{ saving ? '保存中…' : '保存站点' }}
          </button>
        </footer>
      </form>
    </div>
    <div v-if="history" class="manager-backdrop" @click.self="history = null">
      <section
        class="manager-dialog manager-wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="site-history-title"
      >
        <header>
          <h2 id="site-history-title">{{ history.name }} · 历史数据</h2>
          <button class="icon-button" aria-label="关闭" @click="history = null">×</button>
        </header>
        <p v-if="!history.items.length">尚无成功快照，先刷新站点数据。</p>
        <div class="manager-table">
          <table v-if="history.items.length">
            <thead>
              <tr>
                <th>日期</th>
                <th>累计上传</th>
                <th>累计下载</th>
                <th>分享率</th>
                <th>做种数量</th>
                <th>做种体积</th>
                <th>积分 / 魔力</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in history.items" :key="item.stats.captured_at">
                <td>{{ date(item.stats.captured_at) }}</td>
                <td
                  v-for="key in [
                    'uploaded',
                    'downloaded',
                    'ratio',
                    'seeding',
                    'seeding_bytes',
                    'bonus',
                  ]"
                  :key="key"
                >
                  {{ value(item.stats, key) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </section>
</template>

<style scoped>
.manager-heading,
.manager-actions,
.site-card-title,
.site-account,
.manager-filter {
  display: flex;
  align-items: center;
  gap: 12px;
}
.manager-heading {
  justify-content: flex-end;
  margin-bottom: 24px;
}
.site-transfer-error {
  padding: 12px 15px;
  border-radius: 8px;
  background: #fff0ed;
  color: var(--danger);
  overflow-wrap: anywhere;
}
.site-import-result {
  padding: 14px 16px;
  margin-bottom: 20px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface);
}
.import-result-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.import-result-heading strong {
  font-size: 13px;
  line-height: 1.7;
}
.site-import-result summary {
  margin-top: 8px;
  font-size: 12px;
  cursor: pointer;
  color: var(--muted);
}
.site-import-result ul {
  padding-left: 20px;
  margin-bottom: 0;
}
.site-import-result li {
  padding: 5px 0;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.site-import-result li span,
.site-import-result li small {
  display: block;
  margin-top: 3px;
}
.manager-note {
  color: var(--muted);
  margin: 0;
}
.manager-actions {
  flex-wrap: wrap;
}
.site-totals {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
  margin-bottom: 24px;
}
.site-totals > div {
  display: grid;
  gap: 9px;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 20px;
}
.site-totals strong {
  font-size: 24px;
}
.site-totals span,
.site-totals small,
.site-updated {
  color: var(--muted);
}
.manager-filter {
  margin: 0 0 22px;
}
.manager-filter input {
  max-width: 360px;
}
.manager-filter select {
  max-width: 160px;
}
.site-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 20px;
}
.site-card {
  padding: 20px;
  border: 1px solid var(--line);
  background: #fff;
  border-radius: 13px;
  min-width: 0;
}
.site-card.inactive {
  opacity: 0.7;
}
.site-card-title h2 {
  font-size: 18px;
  margin: 0 0 5px;
}
.site-card-title small {
  font-size: 11px;
  color: var(--muted);
  word-break: break-all;
}
.site-monogram {
  display: grid;
  place-items: center;
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 23px;
  font-weight: 700;
}
.site-badge {
  margin-left: auto;
  font-size: 11px;
  white-space: nowrap;
  padding: 5px 8px;
  background: #f0f2f4;
  border-radius: 20px;
}
.site-badge.ok {
  background: #e5f4e9;
  color: #367749;
}
.site-badge.failed {
  background: #fff0ed;
  color: var(--danger);
}
.site-account {
  margin-top: 19px;
  flex-wrap: wrap;
  font-size: 12px;
}
.site-account > span {
  color: var(--muted);
}
.site-traffic {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin: 20px 0;
  padding: 17px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}
.site-traffic > div {
  display: grid;
  gap: 8px;
}
.site-traffic small {
  color: var(--muted);
}
.site-traffic strong {
  font-size: 21px;
}
.site-metrics {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 18px 10px;
  margin: 0 0 20px;
}
.site-metrics dt {
  color: var(--muted);
  font-size: 11px;
  margin-bottom: 7px;
}
.site-metrics dd {
  margin: 0;
  font-weight: 600;
  font-size: 14px;
}
.site-updated {
  display: block;
  line-height: 1.8;
  font-size: 11px;
  margin-bottom: 16px;
}
.site-warning {
  font-size: 12px;
  color: var(--danger);
}
.site-warning p {
  word-break: break-word;
}
.manager-note {
  font-size: 12px;
  line-height: 1.8;
  margin-top: 22px;
}
.manager-empty {
  padding: 50px 20px;
  text-align: center;
  background: #fff;
  border-radius: 12px;
  color: var(--muted);
}
.manager-backdrop {
  position: fixed;
  inset: 0;
  background: #12233270;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.manager-dialog {
  width: 640px;
  max-width: 100%;
  max-height: 90vh;
  overflow: auto;
  background: #fff;
  padding: 25px;
  border-radius: 14px;
  box-shadow: 0 30px 90px #0003;
}
.manager-wide {
  width: 1000px;
}
.manager-dialog > header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.manager-dialog h2 {
  margin: 0;
}
.manager-dialog > footer {
  justify-content: flex-end;
  margin-top: 25px;
}
.manager-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
}
.manager-form-grid label {
  display: grid;
  gap: 9px;
}
.manager-form-grid small {
  color: var(--muted);
  line-height: 1.6;
}
.span-two {
  grid-column: 1/-1;
}
.manager-checkbox {
  display: flex !important;
  align-items: center;
  gap: 10px;
  align-self: center;
}
.manager-table {
  overflow: auto;
}
.manager-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.manager-table th,
.manager-table td {
  padding: 13px;
  text-align: left;
  border-bottom: 1px solid var(--line);
  white-space: nowrap;
}
@media (max-width: 750px) {
  .manager-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .site-totals {
    grid-template-columns: repeat(2, 1fr);
  }
  .site-grid {
    grid-template-columns: 1fr;
  }
  .manager-filter {
    flex-wrap: wrap;
  }
  .manager-form-grid {
    grid-template-columns: 1fr;
  }
  .span-two {
    grid-column: auto;
  }
  .manager-dialog {
    padding: 20px;
  }
  .manager-backdrop {
    padding: 10px;
  }
  .site-card-title {
    gap: 8px;
  }
}
</style>
