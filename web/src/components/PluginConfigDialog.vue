<script setup lang="ts">
import { ref, shallowRef, markRaw, computed, onMounted, onUnmounted, type Component } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VDivider } from 'vuetify/components/VDivider';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { VProgressLinear } from 'vuetify/components/VProgressLinear';
import { api, errorText, toast } from '../api';
import { pluginFormIssues, type PluginFormNode, type PluginCard } from '../features/plugin-forms';
import { VSpacer } from 'vuetify/components/VGrid';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
import PluginFormRenderer from './PluginFormRenderer.vue';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';

import { pluginRuntimeSupportNotice } from '../features/plugin-runtime-support';

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: []; saved: []; switch: []; changed: [] }>();
const display = useDisplay();
const renderMode = ref<'vuetify' | 'vue'>('vuetify');
const remoteMaxWidth = ref('60rem');
function remoteLayout(layout?: { maxWidth?: unknown }) {
  const width = typeof layout?.maxWidth === 'string' ? layout.maxWidth.trim() : '';
  remoteMaxWidth.value = width || '60rem';
}
const remote = shallowRef<Component>();
const controller = new AbortController();
const remoteAPI = createPluginRemoteAPI(
  props.plugin.plugin_id,
  props.plugin.instance_id,
  controller.signal,
  props.plugin.plugin_id === 'BrushFlowLanduo' ? () => emit('changed') : undefined,
);
let active = true;
const nodes = ref<PluginFormNode[]>([]),
  model = ref<Record<string, any>>({}),
  revision = ref(0);
const loading = ref(true),
  saving = ref(false),
  error = ref('');
const supportNotice = computed(() => pluginRuntimeSupportNotice(props.plugin.runtime_support));
const issues = computed(() => pluginFormIssues(nodes.value));
const base = '/plugins/' + encodeURIComponent(props.plugin.instance_id);
function pointerControl(event: MouseEvent): HTMLElement | null {
  if (!(event.target instanceof Element)) return null;
  const control = event.target.closest('button, input[type="checkbox"]');
  if (control instanceof HTMLElement) return control;
  const label = event.target.closest('label');
  return label instanceof HTMLLabelElement &&
    label.control instanceof HTMLInputElement &&
    label.control.type === 'checkbox'
    ? label.control
    : null;
}
function keepPointerTarget(event: MouseEvent) {
  // Focus-only hints resize centered dialogs. Keep the pressed control in
  // place until click dispatch, then restore the same normal focus behavior.
  if (event.button === 0 && pointerControl(event)) event.preventDefault();
}
function focusAfterClick(event: MouseEvent) {
  if (event.detail === 0) return;
  const control = pointerControl(event);
  requestAnimationFrame(() => {
    if (control?.isConnected) control.focus({ preventScroll: true });
  });
}
async function load() {
  try {
    const form = await api(base + '/form');
    if (!active) return;
    renderMode.value = form.render_mode || 'vuetify';
    if (!['vuetify', 'vue'].includes(renderMode.value)) throw new Error('插件界面格式无效');
    nodes.value = form.conf || [];
    model.value = form.model;
    revision.value = form.revision;
    if (renderMode.value === 'vue') {
      const component = await loadPluginRemote(
        props.plugin.plugin_id,
        props.plugin.instance_id,
        'Config',
      );
      if (active) remote.value = markRaw(component);
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
async function save() {
  if (saving.value || loading.value || issues.value.length) return;
  saving.value = true;
  error.value = '';
  try {
    const accepted = await api(
      base + '/config',
      { revision: revision.value, config: model.value },
      'PUT',
    );
    const deadline = Date.now() + 30000;
    while (active && Date.now() < deadline) {
      const runs = await api(base + '/runs');
      const run = runs.find((item: any) => item.id === accepted.run.id);
      if (run?.state === 'succeeded') {
        toast(props.plugin.name + '配置已保存');
        emit('saved');
        return;
      }
      if (run && ['failed', 'canceled', 'reconcile_pending'].includes(run.state)) {
        throw new Error('配置已保存，但插件执行未完成：' + (run.error_code || run.state));
      }
      await new Promise((resolve) => setTimeout(resolve, 350));
    }
    if (!active) return;
    throw new Error('配置已保存，插件仍在执行，请在插件执行记录中查看结果');
  } catch (e) {
    error.value = errorText(e);
    // Preserve the user's draft. Re-fetch only the revision so a failed action
    // can be corrected and resubmitted without a silent stale overwrite.
    try {
      revision.value = (await api(base + '/form')).revision;
    } catch {
      /* retain draft */
    }
  } finally {
    saving.value = false;
  }
}
async function saveRemote(config: Record<string, any>) {
  model.value = config;
  await save();
}
onMounted(load);
onUnmounted(() => {
  active = false;
  controller.abort();
});
</script>
<template>
  <VDialog
    :model-value="true"
    scrollable
    :max-width="remoteMaxWidth"
    :fullscreen="!display.mdAndUp.value"
    :persistent="saving"
    content-class="plugin-dialog-surface"
    @update:model-value="!saving && emit('close')"
    @mousedown.capture="keepPointerTarget"
    @click.capture="focusAfterClick"
  >
    <VCard
      v-if="renderMode === 'vuetify'"
      :title="`${plugin.name} - 插件配置`"
      class="plugin-config-card"
    >
      <PluginDialogCloseBtn :disabled="saving" aria-label="关闭配置" @click="emit('close')" />
      <VDivider />
      <VCardText class="plugin-dialog-content">
        <VAlert
          v-if="supportNotice"
          type="warning"
          variant="tonal"
          class="mb-4"
          data-testid="plugin-runtime-support"
          >{{ supportNotice }}</VAlert
        >
        <VAlert v-if="plugin.update_review_pending" type="warning" variant="tonal" class="ma-4"
          >版本已迁移，旧一次性操作尚未执行。请核对开关和内容后保存；保存仅确认当前实例，不影响其他分身。关闭或取消不会执行。</VAlert
        >
        <p v-if="loading" role="status">正在加载配置…</p>
        <VAlert v-if="error" type="error" variant="tonal" class="plugin-form-error">{{
          error
        }}</VAlert>
        <VAlert v-for="issue in issues" :key="issue" type="error" variant="tonal">{{
          issue
        }}</VAlert>
        <template v-if="!loading && !issues.length">
          <PluginFormRenderer
            v-for="(node, index) in nodes"
            :key="index"
            :node="node"
            :model="model"
          />
          <p v-if="!nodes.length && !error">此插件没有可配置项</p>
        </template>
      </VCardText>
      <VCardActions class="app-dialog-actions plugin-dialog-actions">
        <VBtn
          v-if="plugin.has_page"
          color="info"
          variant="tonal"
          prepend-icon="mdi-database-eye-outline"
          :disabled="saving"
          @click="emit('switch')"
          >查看数据</VBtn
        >
        <VSpacer />
        <VBtn
          color="primary"
          variant="flat"
          prepend-icon="mdi-content-save"
          class="px-5"
          :loading="saving"
          :disabled="loading || !!issues.length || (!nodes.length && !!error)"
          @click="save()"
          >保存</VBtn
        >
      </VCardActions>
    </VCard>
    <VCard v-else class="plugin-config-card">
      <VCardText class="pa-0">
        <VAlert
          v-if="supportNotice"
          type="warning"
          variant="tonal"
          class="mb-4"
          data-testid="plugin-runtime-support"
          >{{ supportNotice }}</VAlert
        >
        <VAlert v-if="plugin.update_review_pending" type="warning" variant="tonal" class="ma-4"
          >版本已迁移，旧一次性操作尚未执行。请核对开关和内容后保存；保存仅确认当前实例，不影响其他分身。关闭或取消不会执行。</VAlert
        >
        <p v-if="loading" class="pa-4" role="status">正在加载配置…</p>
        <VAlert v-if="error" type="error" variant="tonal" class="ma-4">{{ error }}</VAlert>
        <VProgressLinear v-if="saving" indeterminate aria-label="正在保存配置" />
        <component
          v-if="remote && !loading"
          :is="remote"
          :initial-config="model"
          :api="remoteAPI"
          @layout="remoteLayout"
          @save="saveRemote"
          @switch="!saving && emit('switch')"
          @close="!saving && emit('close')"
        />
      </VCardText>
    </VCard>
  </VDialog>
</template>
