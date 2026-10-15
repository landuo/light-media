<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { api, action, clone, bytes, date, friendly, refreshSettings, toast, go } from '../api';
const data = ref<any>({ downloaders: [], default_downloader: '' }),
  editing = ref<any>(null),
  original = ref<any>(null);
const saving = ref(false),
  testing = ref<string[]>([]),
  live = ref<Record<string, any>>({});
async function load() {
  const result = await action(() => api('/downloaders/manage'));
  if (result) data.value = result;
}
function create(kind = 'qbittorrent') {
  original.value = null;
  editing.value = {
    id: '',
    name: kind === 'transmission' ? 'Transmission' : 'qBittorrent',
    kind,
    base_url: kind === 'transmission' ? 'http://127.0.0.1:9091' : 'http://127.0.0.1:8080',
    username: '',
    password: '',
    download_path: '/downloads',
    mappings: [],
    default: !data.value.downloaders.length,
  };
}
function edit(row: any) {
  original.value = row;
  editing.value = { ...clone(row.config), default: row.default };
  editing.value.mappings ||= [];
}
async function save() {
  saving.value = true;
  try {
    const result = await action(() => api('/downloaders/manage', editing.value), '下载器已保存');
    if (result) {
      data.value = result;
      editing.value = null;
      await refreshSettings();
    }
  } finally {
    saving.value = false;
  }
}
async function setDefault(row: any) {
  const result = await action(
    () => api('/downloaders/manage', { ...row.config, default: true }),
    '默认下载器已更新',
  );
  if (result) {
    data.value = result;
    await refreshSettings();
  }
}
async function remove(row: any) {
  if (!window.confirm(`删除下载器「${row.config.name}」的连接配置？下载器服务、种子及文件会保留。`))
    return;
  const result = await action(
    () => api('/downloaders/manage/' + encodeURIComponent(row.config.id), undefined, 'DELETE'),
    '下载器配置已删除',
  );
  if (result) {
    data.value = result;
    delete live.value[row.config.id];
    await refreshSettings();
  }
}
async function test(row: any) {
  const id = row.config.id;
  testing.value.push(id);
  try {
    const result = await action(() =>
      api('/downloaders/manage/' + encodeURIComponent(id) + '/test', {}),
    );
    if (result) {
      live.value[id] = result;
      row.connection = result.connection;
      toast(
        result.connection.status === 'ok'
          ? `${row.config.name} 连接正常`
          : friendly(result.connection.error),
        result.connection.status !== 'ok',
      );
    }
  } finally {
    testing.value = testing.value.filter((v) => v !== id);
  }
}
onMounted(load);
</script>
<template>
  <section class="downloader-manager">
    <header class="dm-heading">
      <div>
        <h1>下载器</h1>
        <p>自行配置 qBittorrent 和 Transmission，支持多个实例及默认下载器。</p>
      </div>
      <div class="dm-actions">
        <button @click="go('directories')">目录管理</button>
        <button @click="create('transmission')">＋ Transmission</button
        ><button class="primary" @click="create()">＋ qBittorrent</button>
      </div>
    </header>
    <p v-if="!data.downloaders.length" class="dm-empty">
      尚未配置下载器。添加实例后，可在搜索下载和订阅中选择使用。
    </p>
    <div class="dm-grid">
      <article v-for="row in data.downloaders" :key="row.config.id" class="dm-card">
        <header>
          <span class="dm-icon">{{ row.config.kind === 'transmission' ? '⇅' : 'qB' }}</span>
          <div>
            <h2>{{ row.config.name }} <span v-if="row.default" class="dm-default">默认</span></h2>
            <small>{{ row.config.kind === 'transmission' ? 'Transmission' : 'qBittorrent' }}</small>
          </div>
          <span class="dm-status" :class="row.connection.status">{{
            row.connection.status === 'ok'
              ? '连接正常'
              : row.connection.status === 'failed'
                ? '连接异常'
                : '未检测'
          }}</span>
        </header>
        <p class="dm-address">{{ row.config.base_url }}</p>
        <div v-if="live[row.config.id]?.connection.status === 'ok'" class="dm-speeds">
          <span>↓ {{ bytes(live[row.config.id].download_speed) }}/s</span
          ><span>↑ {{ bytes(live[row.config.id].upload_speed) }}/s</span
          ><small>{{ live[row.config.id].task_count }} 个任务</small>
        </div>
        <dl>
          <dt>默认下载目录（下载器视角）</dt>
          <dd>{{ row.config.download_path }}</dd>
          <dt>路径映射（下载器 → 本应用）</dt>
          <dd v-if="!row.config.mappings?.length">相同路径，无额外映射</dd>
          <dd v-for="(m, i) in row.config.mappings" :key="i">{{ m.from }} → {{ m.to }}</dd>
        </dl>
        <p v-if="row.connection.error" class="dm-error">{{ friendly(row.connection.error) }}</p>
        <small class="dm-meta"
          >上次检测 {{ date(row.connection.checked_at)
          }}{{
            row.connection.duration_ms
              ? ' · ' + (row.connection.duration_ms / 1000).toFixed(2) + ' 秒'
              : ''
          }}</small
        >
        <footer class="dm-actions">
          <button class="small" @click="test(row)" :disabled="testing.includes(row.config.id)">
            {{ testing.includes(row.config.id) ? '检测中…' : '检测连接' }}</button
          ><button class="small" @click="edit(row)">编辑</button
          ><button v-if="!row.default" class="small" @click="setDefault(row)">设为默认</button
          ><button class="small danger" @click="remove(row)">删除</button>
        </footer>
      </article>
    </div>
    <p class="dm-note">
      连接检测仅认证并读取任务，不改动下载器全局设置或既有种子。已确认下载记录固定当时的下载器与路径；修改连接或映射后，受影响的整理任务会要求重新核对。
    </p>
    <div v-if="editing" class="dm-backdrop" @click.self="editing = null">
      <form
        class="dm-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="dm-title"
        @submit.prevent="save"
      >
        <header>
          <h2 id="dm-title">{{ original ? '编辑下载器' : '添加下载器' }}</h2>
          <button type="button" class="icon-button" aria-label="关闭" @click="editing = null">
            ×
          </button>
        </header>
        <div class="dm-form">
          <label>名称<input v-model.trim="editing.name" required maxlength="64" /></label
          ><label
            >类型<select v-model="editing.kind">
              <option value="qbittorrent">qBittorrent</option>
              <option value="transmission">Transmission</option>
            </select></label
          ><label class="dm-span"
            >服务器地址<input
              v-model.trim="editing.base_url"
              type="url"
              required
              placeholder="http(s)://IP:端口"
            /><small
              >可保留反向代理子路径；Transmission 可填写含 /transmission/rpc 的地址。</small
            ></label
          ><label>用户名<input v-model="editing.username" autocomplete="off" /></label
          ><label
            >密码<input
              v-model="editing.password"
              type="password"
              autocomplete="new-password"
              :placeholder="
                original?.credential_configured ? '已保存，留空保留' : '下载器登录密码'
              " /></label
          ><label class="dm-span"
            >默认下载目录（下载器视角）<input
              v-model.trim="editing.download_path"
              required
              placeholder="/downloads" /></label
          ><label class="dm-check dm-span"
            ><input v-model="editing.default" type="checkbox" />设为默认下载器</label
          >
        </div>
        <div class="dm-mapping">
          <header>
            <h3>路径映射</h3>
            <button
              type="button"
              class="small"
              @click="editing.mappings.push({ from: '', to: '' })"
            >
              ＋ 添加映射
            </button>
          </header>
          <p>把下载器返回的目录前缀映射到本应用可访问的目录；同路径可以不添加。</p>
          <div v-for="(m, i) in editing.mappings" :key="i" class="dm-map-row">
            <label
              >下载器目录<input v-model.trim="m.from" required placeholder="/downloads" /></label
            ><span>→</span
            ><label
              >本应用目录<input v-model.trim="m.to" required placeholder="/media/download" /></label
            ><button
              type="button"
              class="small danger"
              @click="editing.mappings.splice(i, 1)"
              aria-label="删除路径映射"
            >
              ×
            </button>
          </div>
        </div>
        <p class="dm-note">
          密码加密保存，不会回显；修改服务器地址、类型或账号时，请重新填写密码。
        </p>
        <footer class="dm-actions">
          <button type="button" @click="editing = null">取消</button
          ><button class="primary" type="submit" :disabled="saving">
            {{ saving ? '保存中…' : '保存下载器' }}
          </button>
        </footer>
      </form>
    </div>
  </section>
</template>
<style scoped>
.dm-heading,
.dm-actions,
.dm-card > header,
.dm-dialog > header,
.dm-mapping > header {
  display: flex;
  gap: 12px;
  align-items: center;
}
.dm-heading {
  justify-content: space-between;
  margin-bottom: 26px;
}
.dm-heading h1 {
  margin: 0 0 8px;
}
.dm-heading p,
.dm-note,
.dm-meta,
.dm-card dt,
.dm-card header small,
.dm-mapping p {
  color: var(--muted);
}
.dm-heading p {
  margin: 0;
}
.dm-actions {
  flex-wrap: wrap;
}
.dm-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 20px;
}
.dm-card {
  background: #fff;
  padding: 22px;
  border: 1px solid var(--line);
  border-radius: 13px;
  min-width: 0;
}
.dm-card h2 {
  font-size: 18px;
  margin: 0 0 7px;
}
.dm-icon {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 22px;
  font-weight: 700;
}
.dm-default {
  font-size: 10px;
  border-radius: 12px;
  padding: 3px 7px;
  background: var(--accent-soft);
  color: var(--accent);
  vertical-align: middle;
}
.dm-status {
  margin-left: auto;
  font-size: 11px;
  white-space: nowrap;
}
.dm-status.ok {
  color: #367749;
}
.dm-status.failed,
.dm-error {
  color: var(--danger);
}
.dm-address {
  font-size: 13px;
  word-break: break-all;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--line);
}
.dm-speeds {
  display: flex;
  align-items: center;
  gap: 20px;
  margin: 18px 0;
  font-weight: 600;
  color: var(--accent);
}
.dm-speeds small {
  color: var(--muted);
  margin-left: auto;
  font-weight: 400;
}
.dm-card dt {
  font-size: 11px;
  margin-top: 16px;
  margin-bottom: 6px;
}
.dm-card dd {
  margin: 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.dm-meta {
  display: block;
  font-size: 11px;
  margin: 20px 0 16px;
}
.dm-note {
  font-size: 12px;
  line-height: 1.8;
  margin: 24px 0 0;
}
.dm-empty {
  padding: 50px;
  text-align: center;
  background: #fff;
  border-radius: 12px;
  color: var(--muted);
}
.dm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 200;
  background: #12233270;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.dm-dialog {
  background: #fff;
  width: 680px;
  max-width: 100%;
  max-height: 90vh;
  overflow: auto;
  border-radius: 14px;
  padding: 25px;
  box-shadow: 0 30px 90px #0003;
}
.dm-dialog > header,
.dm-mapping > header {
  justify-content: space-between;
}
.dm-dialog h2 {
  margin: 0;
}
.dm-form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
  margin-top: 25px;
}
.dm-form label,
.dm-map-row label {
  display: grid;
  gap: 9px;
}
.dm-form small {
  font-size: 11px;
  line-height: 1.6;
  color: var(--muted);
}
.dm-span {
  grid-column: 1/-1;
}
.dm-check {
  display: flex !important;
  align-items: center;
}
.dm-mapping {
  margin-top: 25px;
  padding-top: 15px;
  border-top: 1px solid var(--line);
}
.dm-mapping h3 {
  font-size: 15px;
}
.dm-mapping p {
  font-size: 12px;
  line-height: 1.7;
}
.dm-map-row {
  display: grid;
  grid-template-columns: 1fr 20px 1fr 34px;
  align-items: end;
  gap: 10px;
  margin-top: 16px;
}
.dm-map-row label {
  font-size: 12px;
}
.dm-map-row > span {
  padding-bottom: 10px;
}
.dm-dialog > footer {
  justify-content: flex-end;
  margin-top: 25px;
}
@media (max-width: 750px) {
  .dm-heading {
    flex-direction: column;
    align-items: flex-start;
  }
  .dm-grid {
    grid-template-columns: 1fr;
  }
  .dm-form {
    grid-template-columns: 1fr;
  }
  .dm-span {
    grid-column: auto;
  }
  .dm-dialog {
    padding: 20px;
  }
  .dm-backdrop {
    padding: 10px;
  }
  .dm-map-row {
    grid-template-columns: 1fr 20px 1fr 30px;
    gap: 6px;
  }
}
</style>
