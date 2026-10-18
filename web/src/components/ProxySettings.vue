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
  saving = ref(false);
async function load() {
  const c = await refreshSettings();
  value.value = '';
  configured.value = !!c.secret_state?.proxy_host;
  endpoint.value = c.proxy_endpoint || '';
  clear.value = false;
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
    type="password"
    prepend-inner-icon="mdi-server-network"
    autocomplete="new-password"
    maxlength="8192"
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
  <div class="button-row">
    <button class="primary" :disabled="saving" @click="save">保存</button>
  </div>
</template>
