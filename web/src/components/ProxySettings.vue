<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { api, action, refreshSettings } from '../api';
import { VTextField } from 'vuetify/components/VTextField';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
const value = ref(''),
  configured = ref(false),
  endpoint = ref(''),
  clear = ref(false),
  saving = ref(false),
  loading = ref(true);
async function load() {
  loading.value = true;
  try {
    const c = await refreshSettings();
    value.value = typeof c.proxy_host === 'string' ? c.proxy_host : '';
    configured.value = !!c.secret_state?.proxy_host;
    endpoint.value = c.proxy_endpoint || '';
    clear.value = false;
  } finally {
    loading.value = false;
  }
}
function remove() {
  value.value = '';
  clear.value = true;
}
async function save() {
  saving.value = true;
  try {
    const c = JSON.parse(JSON.stringify(await refreshSettings()));
    delete c.secret_state;
    c.proxy_host = value.value;
    c.clear_proxy_host = clear.value;
    const result = await action(() => api('/settings', c, 'PUT'), '网络设置已保存');
    if (result) await load();
  } finally {
    saving.value = false;
  }
}
onMounted(() => action(load));
</script>
<template>
  <header class="section-heading"><h2>网络</h2></header>
  <VTextField
    v-model.trim="value"
    label="代理服务器"
    type="text"
    prepend-inner-icon="mdi-server-network"
    autocomplete="new-password"
    maxlength="8192"
    :disabled="loading || saving"
    :placeholder="
      configured && !clear ? '已保存：' + endpoint : 'http(s)://ip:port 或 socks5(h)://ip:port'
    "
    persistent-placeholder
    :hint="
      configured && !clear
        ? '留空保留已保存的代理；点击清除后使用环境变量。'
        : '设置代理服务器地址，支持 HTTP、HTTPS、SOCKS5、SOCKS5h；留空使用环境变量。'
    "
    persistent-hint
    :clearable="!!value"
    @click:clear="remove"
  >
    <template v-if="configured && !clear && !value" #append-inner
      ><VBtn
        icon
        size="x-small"
        variant="text"
        aria-label="清除已保存的代理服务器"
        @click.stop="remove"
        ><VIcon icon="mdi-close-circle" /></VBtn
    ></template>
  </VTextField>
  <p class="hint">
    代理用于 TMDB
    等外部元数据与图片请求；资源站点还需在「站点管理」中开启该站点的代理开关。下载器和媒体库连接不受此设置影响。
  </p>
  <p class="hint">
    NAS / Docker 部署请填写容器能够访问的代理地址，例如 http://192.168.1.2:7890。127.0.0.1
    指向容器自身，不是 NAS 或电脑。保存后新请求生效，可用上方 TMDB「测试」检查连接。
  </p>
  <div class="button-row">
    <button class="primary" :disabled="loading || saving" @click="save">保存</button>
  </div>
</template>
