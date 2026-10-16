<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VDivider } from 'vuetify/components/VDivider';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText, toast } from '../api';
import { pluginFormIssues, type PluginFormNode, type PluginCard } from '../features/plugin-forms';
import AppIcon from './AppIcon.vue';
import PluginFormRenderer from './PluginFormRenderer.vue';

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const display = useDisplay();
const nodes = ref<PluginFormNode[]>([]),
  model = ref<Record<string, any>>({}),
  revision = ref(0);
const loading = ref(true),
  saving = ref(false),
  error = ref('');
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
    nodes.value = form.conf;
    model.value = form.model;
    revision.value = form.revision;
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
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
    while (Date.now() < deadline) {
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
onMounted(load);
</script>
<template>
  <VDialog
    :model-value="true"
    scrollable
    max-width="60rem"
    :fullscreen="!display.mdAndUp.value"
    :persistent="saving"
    content-class="plugin-dialog-surface"
    @update:model-value="!saving && emit('close')"
    @mousedown.capture="keepPointerTarget"
    @click.capture="focusAfterClick"
  >
    <VCard class="plugin-config-card">
      <VCardTitle class="plugin-dialog-title"
        >{{ plugin.name }} - 配置
        <VBtn
          variant="text"
          color="secondary"
          icon
          :disabled="saving"
          aria-label="关闭配置"
          @click="emit('close')"
          ><AppIcon name="close"
        /></VBtn>
      </VCardTitle>
      <VDivider />
      <VCardText class="plugin-dialog-content">
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
      <VCardActions class="plugin-dialog-actions">
        <VBtn
          color="primary"
          variant="flat"
          :loading="saving"
          :disabled="loading || !!issues.length || (!nodes.length && !!error)"
          @click="save"
          ><AppIcon name="save" />保存</VBtn
        >
      </VCardActions>
    </VCard>
  </VDialog>
</template>
<style>
.plugin-dialog-surface {
  color: var(--text);
  font-family: inherit;
}
.plugin-dialog-surface .plugin-config-card {
  border-radius: 8px;
}
.plugin-dialog-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px !important;
  font-size: 20px !important;
}
.plugin-dialog-content {
  padding: 24px !important;
}
.plugin-dialog-content .v-row + .v-row {
  margin-top: 12px;
}
.plugin-dialog-surface .v-label {
  flex-direction: row;
  gap: 0;
}
.plugin-dialog-surface .v-field input,
.plugin-dialog-surface .v-field textarea {
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  color: inherit;
  font: inherit;
  line-height: inherit;
  min-width: 0;
}
.plugin-dialog-surface .v-field__input > input {
  padding: 0;
}
.plugin-dialog-surface .v-field textarea {
  resize: none;
}
.plugin-dialog-actions {
  padding: 16px 24px !important;
  justify-content: flex-end;
  border-top: 1px solid var(--line);
}
.plugin-dialog-actions .v-btn {
  min-width: 100px;
}
.plugin-dialog-actions .app-icon {
  margin-right: 8px;
}
.plugin-form-error {
  margin-bottom: 16px;
}
.plugin-dialog-surface .ace_text-input {
  padding: 0;
  border: 0;
  box-shadow: none;
}
</style>
