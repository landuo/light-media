<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VTextField } from 'vuetify/components/VTextField';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VDivider } from 'vuetify/components/VDivider';
import { VAlert } from 'vuetify/components/VAlert';
import { api, APIError, errorText } from '../api';
import type { PluginCard } from '../features/plugin-forms';
import {
  emptyPluginClone,
  pluginCloneErrors,
  pluginCloneInstanceId,
  pluginCloneErrorHint,
  type PluginCloneResult,
} from '../features/plugin-clone';

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: []; created: [result: PluginCloneResult] }>();
const display = useDisplay();
const form = reactive({
  ...emptyPluginClone(),
  name: `${props.plugin.name} 分身`,
  description: `${props.plugin.description || ''} (分身版本)`,
  version: props.plugin.display_version || props.plugin.version || '1.0',
  icon: props.plugin.icon || '',
});
const busy = ref(false),
  completed = ref(false),
  attempted = ref(false),
  error = ref('');
const requestForm = computed(() => ({
  suffix: form.suffix.trim(),
  name: form.name.trim(),
  description: form.description.trim(),
  version: form.version.trim(),
  icon: form.icon.trim(),
}));
const instanceId = computed(() => pluginCloneInstanceId(props.plugin, requestForm.value.suffix));
const errors = computed(() => pluginCloneErrors(props.plugin, requestForm.value));
function close() {
  if (!busy.value) emit('close');
}
async function submit() {
  if (busy.value || completed.value) return;
  attempted.value = true;
  error.value = '';
  if (Object.keys(errors.value).length) return;
  busy.value = true;
  try {
    const result = await api<PluginCloneResult & { success?: boolean; message?: string }>(
      '/plugins/' + encodeURIComponent(props.plugin.instance_id) + '/clone',
      { ...requestForm.value },
    );
    if (result?.success === false)
      throw new APIError('PLUGIN_CLONE_FAILED', result.message || '插件分身创建失败', '');
    if (
      result?.created !== true ||
      result.instance_id !== instanceId.value ||
      result.source_instance_id !== props.plugin.instance_id ||
      result.plugin_id !== props.plugin.plugin_id
    )
      throw new APIError(
        'INVALID_PLUGIN_CLONE_RESPONSE',
        '服务返回的分身结果无法确认，请刷新插件列表核对',
        '',
      );
    completed.value = true;
    emit('created', result);
  } catch (e) {
    error.value = [pluginCloneErrorHint((e as APIError)?.code), errorText(e)]
      .filter(Boolean)
      .join(' ');
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <VDialog
    :model-value="true"
    max-width="600"
    :fullscreen="!display.mdAndUp.value"
    scrollable
    :persistent="busy"
    content-class="plugin-dialog-surface"
    @update:model-value="
      (value) => {
        if (!value) close();
      }
    "
  >
    <VCard tag="form" class="plugin-config-card" novalidate @submit.prevent="submit">
      <VCardTitle class="plugin-dialog-title">
        <span>插件分身</span>
        <VBtn
          variant="text"
          icon
          type="button"
          color="secondary"
          :disabled="busy"
          aria-label="关闭插件分身"
          @click="close"
          ><VIcon icon="mdi-close"
        /></VBtn>
      </VCardTitle>
      <VDivider />
      <VCardText class="plugin-dialog-content">
        <p class="clone-source">来源：{{ plugin.name }}（{{ plugin.instance_id }}）</p>
        <VAlert v-if="error" type="error" variant="tonal" class="mb-4" role="alert">{{
          error
        }}</VAlert>
        <VTextField
          v-model="form.suffix"
          label="分身后缀"
          aria-label="分身后缀"
          variant="outlined"
          autofocus
          autocomplete="off"
          :disabled="busy"
          :error-messages="attempted || form.suffix ? errors.suffix : undefined"
          hint="1 至 20 位字母或数字；后缀转为小写后直接拼接来源实例编号"
          persistent-hint
        />
        <p class="clone-instance-preview" role="status">分身编号：{{ instanceId }}</p>
        <VTextField
          v-model="form.name"
          label="分身名称"
          aria-label="分身名称"
          :placeholder="plugin.name"
          variant="outlined"
          :disabled="busy"
          :error-messages="errors.name"
          hint="留空继承来源名称"
          persistent-hint
        />
        <VTextField
          v-model="form.description"
          label="分身描述"
          aria-label="分身描述"
          :placeholder="plugin.description"
          variant="outlined"
          :disabled="busy"
          :error-messages="errors.description"
          hint="留空继承来源描述"
          persistent-hint
        />
        <VTextField
          v-model="form.version"
          label="分身版本"
          aria-label="分身版本"
          :placeholder="plugin.display_version ?? plugin.version"
          variant="outlined"
          :disabled="busy"
          :error-messages="errors.version"
          hint="用于分身版本展示和更新检查；留空继承来源"
          persistent-hint
        />
        <VTextField
          v-model="form.icon"
          label="分身图标"
          aria-label="分身图标"
          :placeholder="plugin.icon || '/plugin_icon/statistic.png'"
          variant="outlined"
          :disabled="busy"
          :error-messages="errors.icon"
          hint="留空继承来源；支持 HTTP/HTTPS 图片地址或相对资源路径"
          persistent-hint
        />
        <VAlert type="info" variant="tonal" class="mt-2">
          仅复制配置，不复制历史记录及运行数据（KV）。来源配置非空时关闭 enable、enabled
          两个常用启用字段；来源配置为空时按插件默认值初始化，可能自动启用。创建后请检查分身设置。
        </VAlert>
      </VCardText>
      <VCardActions class="plugin-dialog-actions">
        <VBtn type="button" variant="text" :disabled="busy" @click="close">取消</VBtn>
        <VBtn
          type="submit"
          color="primary"
          variant="flat"
          :loading="busy"
          :disabled="busy || completed"
          >创建分身</VBtn
        >
      </VCardActions>
    </VCard>
  </VDialog>
</template>
<style scoped>
.clone-source {
  margin-bottom: 20px;
  overflow-wrap: anywhere;
}
.clone-instance-preview {
  margin: 0 0 20px;
  overflow-wrap: anywhere;
  font-size: 13px;
  color: var(--muted);
}
</style>
