<script setup lang="ts">
import { computed, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VDialog } from 'vuetify/components/VDialog';
import {
  VCard,
  VCardItem,
  VCardTitle,
  VCardSubtitle,
  VCardText,
  VCardActions,
} from 'vuetify/components/VCard';
import { VDivider } from 'vuetify/components/VDivider';
import { VForm } from 'vuetify/components/VForm';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { VTextField } from 'vuetify/components/VTextField';
import { VSwitch } from 'vuetify/components/VSwitch';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
const props = defineProps<{
  value: Record<string, any>;
  credentials?: { api_key?: boolean; password?: boolean };
}>();
const emit = defineEmits<{ close: []; confirm: [] }>();
const display = useDisplay(),
  form = ref<InstanceType<typeof VForm>>();
const qb = computed(() => ['qb', 'qbittorrent'].includes(props.value.kind));
const savedKey = computed(() => !!props.credentials?.api_key && !props.value.clear_api_key);
const apiKeyActive = computed(() => qb.value && (!!props.value.api_key || savedKey.value));
function clearKey() {
  props.value.api_key = '';
  props.value.clear_api_key = true;
}
async function confirm() {
  if ((await form.value?.validate())?.valid) emit('confirm');
}
const nameRules = [(v: string) => !!v?.trim() || '不能为空，且不能重名'];
const hostRules = [
  (v: string) => !props.value.enabled || !!v?.trim() || '地址不能为空',
  (v: string) => !v || /^https?:\/\/[^\s]+$/.test(v) || '请填写完整的 HTTP 或 HTTPS 地址',
];
</script>
<template>
  <VThemeProvider theme="light">
    <VDialog
      :model-value="true"
      scrollable
      max-width="40rem"
      :fullscreen="!display.mdAndUp.value"
      content-class="plugin-dialog-surface downloader-config-dialog"
      @update:model-value="!$event && emit('close')"
    >
      <VCard>
        <VCardItem class="downloader-config-heading"
          ><template #prepend><VIcon icon="mdi-download" /></template><VCardTitle>配置</VCardTitle
          ><VCardSubtitle>{{ value.name }}</VCardSubtitle
          ><template #append
            ><VBtn icon variant="text" aria-label="关闭下载器配置" @click="emit('close')"
              ><VIcon icon="mdi-close" /></VBtn></template
        ></VCardItem>
        <VDivider />
        <VCardText class="downloader-config-body"
          ><VForm ref="form" @submit.prevent="confirm">
            <VRow
              ><VCol cols="12" md="6"
                ><VSwitch
                  v-model="value.enabled"
                  label="启用"
                  @update:model-value="!$event && (value.default = false)" /></VCol
              ><VCol cols="12" md="6"
                ><VSwitch v-model="value.default" label="默认" :disabled="!value.enabled" /></VCol
            ></VRow>
            <VRow>
              <VCol cols="12" md="6"
                ><VTextField
                  v-model.trim="value.name"
                  label="名称"
                  placeholder="不能为空，且不能重名"
                  prepend-inner-icon="mdi-label"
                  maxlength="64"
                  :rules="nameRules"
              /></VCol>
              <VCol cols="12" md="6"
                ><VTextField
                  v-model.trim="value.base_url"
                  label="地址"
                  placeholder="http(s)://ip:port"
                  prepend-inner-icon="mdi-server"
                  :rules="hostRules"
              /></VCol>
              <VCol v-if="qb" cols="12"
                ><VTextField
                  v-model.trim="value.api_key"
                  type="password"
                  label="API Key"
                  :placeholder="savedKey ? '已保存，留空保留' : ''"
                  persistent-placeholder
                  prepend-inner-icon="mdi-key-variant"
                  :clearable="!!value.api_key"
                  :hint="
                    savedKey
                      ? 'API Key 已保存，留空保留；填写后优先使用 API Key 登录。'
                      : '填写后优先使用 WebUI API Key 登录。'
                  "
                  persistent-hint
                  autocomplete="new-password"
                  maxlength="4096"
                  @click:clear="clearKey"
                  ><template v-if="savedKey && !value.api_key" #append-inner
                    ><VBtn
                      icon
                      size="x-small"
                      variant="text"
                      aria-label="清除已保存的 API Key"
                      @click.stop="clearKey"
                      ><VIcon icon="mdi-close-circle" /></VBtn></template></VTextField
              ></VCol>
              <VCol cols="12" md="6"
                ><VTextField
                  v-model="value.username"
                  label="用户名"
                  prepend-inner-icon="mdi-account"
                  :disabled="apiKeyActive"
                  autocomplete="off"
              /></VCol>
              <VCol cols="12" md="6"
                ><VTextField
                  v-model="value.password"
                  type="password"
                  label="密码"
                  prepend-inner-icon="mdi-lock"
                  :disabled="apiKeyActive"
                  :placeholder="credentials?.password ? '已保存，留空保留' : ''"
                  persistent-placeholder
                  autocomplete="new-password"
              /></VCol>
              <template v-if="qb"
                ><VCol cols="12" md="6"
                  ><VSwitch v-model="value.auto_category" label="自动分类管理" /></VCol
                ><VCol cols="12" md="6"
                  ><VSwitch v-model="value.sequential" label="顺序下载" /></VCol
                ><VCol cols="12" md="6"
                  ><VSwitch v-model="value.first_last_piece" label="优先首尾文件" /></VCol
                ><VCol cols="12" md="6"
                  ><VSwitch v-model="value.force_resume" label="强制继续下载" /></VCol
              ></template>
            </VRow> </VForm
        ></VCardText>
        <VDivider /><VCardActions class="downloader-config-actions"
          ><VBtn color="primary" variant="flat" prepend-icon="mdi-content-save" @click="confirm"
            >保存</VBtn
          ></VCardActions
        >
      </VCard>
    </VDialog>
  </VThemeProvider>
</template>
<style scoped>
.downloader-config-heading {
  padding: 12px 24px;
}
.downloader-config-body {
  padding: 24px;
}
.downloader-config-actions {
  justify-content: flex-end;
  padding: 16px 24px;
}
</style>

<style src="../assets/plugin-utilities.css"></style>
