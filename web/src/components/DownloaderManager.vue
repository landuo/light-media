<script setup lang="ts">
import AppIcon from './AppIcon.vue';
import { onMounted, ref } from 'vue';
import { api, action, clone, errorText, friendly, refreshSettings, toast } from '../api';
const data = ref<any>({ downloaders: [], default_downloader: '' }),
  editing = ref<any>(null),
  original = ref<any>(null),
  saving = ref(false),
  adding = ref(false),
  dragging = ref('');
async function load() {
  const result = await action(() => api('/downloaders/manage'));
  if (result) data.value = result;
}
function create(kind: string) {
  adding.value = false;
  let n = data.value.downloaders.length + 1;
  while (data.value.downloaders.some((row: any) => row.config.name === '下载器' + n)) n++;
  data.value.downloaders.push({
    config: {
      // getRandomValues also works when the app is served over LAN HTTP.
      id:
        'downloader-' +
        Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) =>
          value.toString(16).padStart(8, '0'),
        ).join(''),
      name: '下载器' + n,
      kind,
      base_url: '',
      username: '',
      password: '',
      enabled: false,
    },
    default: false,
    credential_configured: false,
    connection: {},
  });
}
function edit(row: any) {
  original.value = row;
  editing.value = {
    ...clone(row.config),
    enabled: row.config.enabled !== false,
    default: row.default,
  };
}
function update() {
  if (
    data.value.downloaders.some(
      (row: any) =>
        row.config.id !== editing.value.id &&
        row.config.name.toLowerCase() === editing.value.name.toLowerCase(),
    )
  ) {
    toast('名称已存在', true);
    return;
  }
  if (!editing.value.enabled) editing.value.default = false;
  if (editing.value.default) for (const row of data.value.downloaders) row.default = false;
  const { default: selected, ...config } = clone(editing.value);
  const row = { ...(original.value || { connection: {} }), config, default: selected };
  const index = data.value.downloaders.findIndex((entry: any) => entry.config.id === config.id);
  if (index < 0) data.value.downloaders.push(row);
  else data.value.downloaders.splice(index, 1, row);
  editing.value = null;
}
function remove(row: any) {
  data.value.downloaders = data.value.downloaders.filter(
    (entry: any) => entry.config.id !== row.config.id,
  );
}
function drop(id: string) {
  const rows = data.value.downloaders;
  const source = rows.findIndex((row: any) => row.config.id === dragging.value);
  const target = rows.findIndex((row: any) => row.config.id === id);
  if (source < 0 || target < 0 || source === target) return;
  const [row] = rows.splice(source, 1);
  rows.splice(target, 0, row);
  dragging.value = '';
}
async function save() {
  saving.value = true;
  try {
    const current = clone(await refreshSettings());
    delete current.secret_state;
    current.downloaders = data.value.downloaders.map((row: any) => clone(row.config));
    current.default_downloader =
      data.value.downloaders.find((row: any) => row.default && row.config.enabled !== false)?.config
        .id ||
      data.value.downloaders.find((row: any) => row.config.enabled !== false)?.config.id ||
      '';
    const result = await action(() => api('/settings', current, 'PUT'), '下载器设置保存成功');
    if (result) {
      await refreshSettings();
      await load();
    }
  } catch (e) {
    toast(errorText(e), true);
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <section class="downloader-settings">
    <header class="section-heading">
      <div>
        <h2>下载器</h2>
      </div>
    </header>
    <div class="downloader-grid">
      <article
        v-for="row in data.downloaders"
        :key="row.config.id"
        class="downloader-card"
        :class="{
          disabled: row.config.enabled === false,
          transmission: row.config.kind === 'transmission',
        }"
        tabindex="0"
        @click="edit(row)"
        @keydown.enter.self="edit(row)"
        @dragover.prevent
        @drop.prevent="drop(row.config.id)"
      >
        <div class="card-controls">
          <button
            class="icon-button drag-handle"
            draggable="true"
            aria-label="拖动排序"
            title="拖动排序"
            @click.stop
            @dragstart="dragging = row.config.id"
            @dragend="dragging = ''"
          >
            <AppIcon name="grip" />
          </button>
          <button
            class="icon-button danger"
            :aria-label="'移除 ' + row.config.name"
            @click.stop="remove(row)"
          >
            <AppIcon name="close" />
          </button>
        </div>
        <div class="card-content">
          <div>
            <h3>
              <span
                v-if="row.default && row.config.enabled !== false"
                class="default-dot"
                aria-label="默认下载器"
                >●</span
              >{{ row.config.name }}
            </h3>
            <small>{{ row.config.kind === 'transmission' ? 'Transmission' : 'qBittorrent' }}</small>
            <p v-if="row.connection?.error" class="error-text">
              {{ friendly(row.connection.error) }}
            </p>
          </div>
          <span class="downloader-logo" aria-hidden="true"
            ><AppIcon v-if="row.config.kind === 'transmission'" name="download" /><template v-else
              >qB</template
            ></span
          >
        </div>
      </article>
    </div>
    <p v-if="!data.downloaders.length" class="notice">尚未添加下载器。</p>
    <footer class="button-row">
      <button class="primary" :disabled="saving" @click="save">
        {{ saving ? '保存中…' : '保存' }}
      </button>
      <div class="add-menu">
        <button aria-label="添加下载器" :aria-expanded="adding" @click="adding = !adding">
          <AppIcon name="plus" />
        </button>
        <div v-if="adding" class="add-menu-options">
          <button @click="create('qbittorrent')">qBittorrent</button
          ><button @click="create('transmission')">Transmission</button>
        </div>
      </div>
    </footer>
    <div v-if="editing" class="modal-backdrop" @click.self="editing = null">
      <form
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="downloader-title"
        @submit.prevent="update"
      >
        <header class="section-heading">
          <div>
            <h2 id="downloader-title">配置</h2>
            <p class="hint">{{ editing.name }}</p>
          </div>
          <button type="button" class="icon-button" aria-label="关闭" @click="editing = null">
            <AppIcon name="close" />
          </button>
        </header>
        <div class="form-grid">
          <label class="check"
            ><input
              v-model="editing.enabled"
              type="checkbox"
              @change="editing.enabled || (editing.default = false)"
            />启用</label
          >
          <label class="check"
            ><input
              v-model="editing.default"
              type="checkbox"
              :disabled="!editing.enabled"
            />默认</label
          >
          <label>名称<input v-model.trim="editing.name" required maxlength="64" /></label>
          <label class="span-two"
            >地址<input
              v-model.trim="editing.base_url"
              type="url"
              :required="editing.enabled"
              placeholder="http(s)://IP:端口"
          /></label>
          <label>用户名<input v-model="editing.username" autocomplete="off" /></label>
          <label
            >密码<input
              v-model="editing.password"
              type="password"
              autocomplete="new-password"
              :placeholder="original?.credential_configured ? '已保存，留空保留' : '登录密码'"
          /></label>
        </div>
        <footer class="button-row"><button class="primary" type="submit">保存</button></footer>
      </form>
    </div>
  </section>
</template>
<style scoped>
.downloader-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 256px), 1fr));
  gap: 12px;
  margin: 24px 0;
}
.downloader-settings > .section-heading {
  margin-bottom: 24px;
}
.downloader-settings > .section-heading h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 500;
}
.downloader-card {
  --downloader-accent: var(--info);
  position: relative;
  background:
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--downloader-accent) 2.5%, transparent),
      color-mix(in srgb, var(--downloader-accent) 1.2%, transparent) 46%,
      transparent 76%
    ),
    var(--surface);
  border: 1px solid color-mix(in srgb, var(--downloader-accent) 8%, transparent);
  border-radius: 8px;
  padding: 0;
  cursor: pointer;
  box-shadow: none;
  transition: border-color 0.2s;
}
.downloader-card.transmission {
  --downloader-accent: var(--danger);
}
.downloader-card::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 2px;
  background: var(--downloader-accent);
  opacity: 0.22;
  border-radius: 8px 0 0 8px;
}
.downloader-card.disabled .card-content {
  opacity: 0.65;
}
.downloader-card:hover {
  border-color: color-mix(in srgb, var(--downloader-accent) 16%, transparent);
}
.downloader-card:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--downloader-accent) 70%, transparent);
  outline-offset: 3px;
}
.card-controls {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 1;
  display: flex;
  justify-content: flex-end;
  gap: 4px;
}
.card-controls .icon-button,
.modal .icon-button {
  display: inline-flex;
  justify-content: center;
  align-items: center;
  width: 36px;
  height: 36px;
  min-height: 36px;
  padding: 7px;
  border-radius: 50%;
}
.card-controls .icon-button:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
}
.card-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 120px;
  padding: 24px;
}
.card-content > div {
  min-width: 0;
  padding-right: 56px;
}
.card-content h3 {
  overflow-wrap: anywhere;
  font-size: 20px;
  font-weight: 500;
  line-height: 1.35;
  margin: 0 0 4px;
}
.card-content small {
  color: var(--muted);
  font-size: 14px;
}
.downloader-logo {
  position: absolute;
  right: 16px;
  bottom: 12px;
  color: var(--downloader-accent);
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  font-size: 36px;
  font-weight: 500;
  flex-shrink: 0;
}
.downloader-logo :deep(svg) {
  width: 48px;
  height: 48px;
}
.default-dot {
  display: inline-block;
  color: var(--success);
  margin-right: 8px;
  font-size: 11px;
  vertical-align: middle;
}
.drag-handle {
  cursor: grab;
}
.add-menu {
  position: relative;
}
.add-menu-options {
  position: absolute;
  left: 0;
  top: calc(100% + 6px);
  z-index: 20;
  min-width: 170px;
  padding: 6px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow);
}
.add-menu-options button {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  background: none;
  border-radius: 4px;
  min-height: 40px;
}
.add-menu > button {
  width: 48px;
  height: 38px;
  min-height: 38px;
  padding: 8px 12px;
  border-color: transparent;
  color: var(--success);
  background: color-mix(in srgb, var(--success) 12%, var(--surface));
}
.add-menu-options button:hover {
  background: color-mix(in srgb, var(--text) 4%, transparent);
}
.downloader-settings > footer {
  padding-top: 16px;
  gap: 16px;
}
.span-two {
  grid-column: 1/-1;
}
.modal footer {
  margin-top: 24px;
  justify-content: flex-end;
}
.modal {
  width: 640px;
  max-height: calc(100dvh - 48px);
  border-radius: 8px;
  padding: 24px;
  box-shadow: var(--shadow);
}
.modal > .section-heading {
  padding-bottom: 16px;
  margin-bottom: 24px;
  border-bottom: 1px solid var(--line);
}
.modal .section-heading h2 {
  font-size: 20px;
  font-weight: 500;
}
.modal .form-grid {
  gap: 24px;
}
.modal .form-grid label:not(.check) {
  color: var(--muted);
  font-size: 12px;
}
.modal .form-grid input:not([type='checkbox']) {
  height: 56px;
  font-size: 16px;
}
.modal .check {
  min-height: 40px;
  font-size: 16px;
  gap: 12px;
}
@media (max-width: 959px) {
  .modal-backdrop {
    padding: 0;
  }
  .modal {
    width: 100%;
    height: 100dvh;
    max-height: 100dvh;
    border-radius: 0;
  }
}
@media (max-width: 760px) {
  .card-controls .icon-button,
  .modal .icon-button {
    width: 36px;
    height: 36px;
  }
}
</style>
