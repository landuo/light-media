<script setup lang="ts">
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
      id: 'downloader-' + crypto.randomUUID(),
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
        <p class="muted">设置下载器连接信息。</p>
      </div>
    </header>
    <div class="downloader-grid">
      <article
        v-for="row in data.downloaders"
        :key="row.config.id"
        class="downloader-card"
        :class="{ disabled: row.config.enabled === false }"
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
            ⠿
          </button>
          <button
            class="icon-button danger"
            :aria-label="'移除 ' + row.config.name"
            @click.stop="remove(row)"
          >
            ×
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
          <span class="downloader-logo" aria-hidden="true">{{
            row.config.kind === 'transmission' ? '⇅' : 'qB'
          }}</span>
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
          ＋
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
            ×
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
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr));
  gap: 18px;
  margin: 24px 0;
}
.downloader-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 14px 20px 22px;
  cursor: pointer;
}
.downloader-card.disabled .card-content {
  opacity: 0.55;
}
.downloader-card:focus-visible,
.downloader-card:hover {
  border-color: var(--accent);
}
.card-controls {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
}
.card-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.card-content h3 {
  font-size: 17px;
  margin: 0 0 9px;
}
.card-content small {
  color: var(--muted);
}
.downloader-logo {
  background: var(--accent-soft);
  color: var(--accent);
  display: grid;
  place-items: center;
  width: 54px;
  height: 54px;
  border-radius: 14px;
  font-size: 25px;
  font-weight: 700;
}
.default-dot {
  display: inline-block;
  color: #37854f;
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
  padding: 5px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 10px;
  box-shadow: 0 10px 25px #0002;
}
.add-menu-options button {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  background: none;
}
.span-two {
  grid-column: 1/-1;
}
.modal footer {
  margin-top: 24px;
  justify-content: flex-end;
}
</style>
