<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VTextField } from 'vuetify/components/VTextField';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { api, refreshSettings, errorText, toast, ui } from '../api';
import { newAPIToken, copyAPIToken } from '../features/api-token';
import AppIcon from './AppIcon.vue';

const props = defineProps<{ configured: boolean }>();
const emit = defineEmits<{ saved: [settings: any] }>();
const draft = ref(''),
  busy = ref(false),
  error = ref('');
function generate() {
  try {
    draft.value = newAPIToken(crypto);
    error.value = '';
    toast('已生成，请保存');
  } catch {
    error.value = '无法生成随机令牌，请稍后重试';
  }
}
async function copy() {
  if (busy.value) return;
  if (!draft.value && !props.configured) {
    generate();
    return;
  }
  busy.value = true;
  try {
    const value = draft.value || (await api('/settings/api-token/reveal', {}, 'POST')).token;
    await copyAPIToken(value);
    error.value = '';
    toast('令牌已复制');
  } catch {
    error.value = '复制失败，请重试';
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (busy.value) return;
  if (draft.value.length < 16) {
    error.value = '不能小于16位字符';
    return;
  }
  busy.value = true;
  try {
    const current = await refreshSettings();
    const settings: Record<string, any> = { ...current, api_token: draft.value };
    delete settings.secret_state;
    const result = await api('/settings', settings, 'PUT');
    ui.settings = result;
    error.value = '';
    emit('saved', result);
    toast('设置已保存');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

onMounted(async () => {
  if (!props.configured) return;
  try {
    draft.value = (await api('/settings/api-token/reveal', {}, 'POST')).token || '';
  } catch {
    // 保留空输入，用户仍可重新生成
  }
});
</script>
<template>
  <VThemeProvider theme="light">
    <section class="panel api-token-settings">
      <VRow
        ><VCol cols="12" md="6">
          <VTextField
            v-model="draft"
            label="API令牌"
            type="text"
            autocomplete="new-password"
            :placeholder="configured ? '已保存，留空保留' : '不能小于16位字符'"
            hint="设置外部请求 API 时使用的 token 值"
            persistent-hint
            :disabled="busy"
            ><template #prepend-inner><AppIcon name="key" /></template>
            <template #append-inner
              ><VBtn
                variant="text"
                color="secondary"
                icon
                :disabled="busy"
                :aria-label="draft || configured ? '复制API令牌' : '生成API令牌'"
                @click="copy"
                ><AppIcon :name="draft || configured ? 'copy' : 'refresh'" /></VBtn
            ></template>
          </VTextField> </VCol
      ></VRow>
      <VAlert v-if="error" type="error" variant="tonal" class="api-token-error">{{ error }}</VAlert>
      <div class="button-row">
        <VBtn :disabled="busy || !draft" @click="save"><AppIcon name="save" />保存</VBtn
        ><VBtn variant="text" color="secondary" :disabled="busy" @click="generate">生成新令牌</VBtn>
      </div>
    </section>
  </VThemeProvider>
</template>
<style scoped>
.api-token-settings :deep(input) {
  border: 0;
  border-radius: 0;
  padding: 0;
  background: transparent;
  box-shadow: none;
}
.api-token-settings :deep(.v-field__input) {
  padding: 16px;
}
.api-token-settings :deep(.v-field__input > input) {
  padding: 0;
}
.api-token-settings :deep(.v-btn--icon) {
  width: 36px;
  height: 36px;
}
.api-token-settings :deep(.v-field__append-inner) {
  padding-top: 10px;
}
.api-token-error {
  margin: 16px 0;
}
</style>
