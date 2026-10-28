<script setup lang="ts">
import {
  ref,
  shallowRef,
  markRaw,
  computed,
  onMounted,
  onUnmounted,
  type Component,
} from "vue";
import { useDisplay } from "vuetify";
import { VDialog } from "vuetify/components/VDialog";
import { VCard, VCardText, VCardActions } from "vuetify/components/VCard";
import { VDivider } from "vuetify/components/VDivider";
import { VBtn } from "vuetify/components/VBtn";
import { VAlert } from "vuetify/components/VAlert";
import { VProgressLinear } from "vuetify/components/VProgressLinear";
import { api, errorText, toast } from "../api";
import {
  pluginFormIssues,
  type PluginFormNode,
  type PluginCard,
} from "../features/plugin-forms";
import { VSpacer } from "vuetify/components/VGrid";
import PluginDialogCloseBtn from "./PluginDialogCloseBtn.vue";
import PluginFormRenderer from "./PluginFormRenderer.vue";
import { loadPluginRemote } from "../features/plugin-federation";
import { createPluginRemoteAPI } from "../features/plugin-remote-api";

import { pluginRuntimeSupportNotice } from "../features/plugin-runtime-support";

const props = defineProps<{ plugin: PluginCard }>();
const emit = defineEmits<{ close: []; saved: []; switch: []; changed: [] }>();
const display = useDisplay();
const renderMode = ref<"vuetify" | "vue">("vuetify");
const remote = shallowRef<Component>();
const controller = new AbortController();
const remoteAPI = shallowRef<ReturnType<typeof createPluginRemoteAPI>>();
let active = true;
const nodes = ref<PluginFormNode[]>([]),
  model = ref<Record<string, any>>({}),
  revision = ref(0);
const loading = ref(true),
  saving = ref(false),
  error = ref("");
const supportNotice = computed(() =>
  pluginRuntimeSupportNotice(props.plugin.runtime_support),
);
const issues = computed(() => pluginFormIssues(nodes.value));
const base = "/plugins/" + encodeURIComponent(props.plugin.instance_id);
function pointerControl(event: MouseEvent): HTMLElement | null {
  if (!(event.target instanceof Element)) return null;
  const control = event.target.closest('button, input[type="checkbox"]');
  if (control instanceof HTMLElement) return control;
  const label = event.target.closest("label");
  return label instanceof HTMLLabelElement &&
    label.control instanceof HTMLInputElement &&
    label.control.type === "checkbox"
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
    const form = await api(base + "/form");
    if (!active) return;
    if (!form || !["vuetify", "vue"].includes(form.render_mode) ||
        !form.model || typeof form.model !== "object" || Array.isArray(form.model) ||
        (form.render_mode === "vuetify" && !Array.isArray(form.conf)))
      throw new Error("插件界面格式无效");
    renderMode.value = form.render_mode;
    nodes.value = form.conf || [];
    model.value = form.model;
    revision.value = form.revision;
    if (renderMode.value === "vue") {
      const { component, descriptor } = await loadPluginRemote(
        props.plugin.instance_id,
        "Config",
        { signal: controller.signal, version: props.plugin.version },
      );
      if (!active) return;
      remoteAPI.value = createPluginRemoteAPI(descriptor.plugin_id, descriptor.instance_id,
        controller.signal, () => emit("changed"));
      remote.value = markRaw(component);
    }
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) loading.value = false;
  }
}
async function save() {
  if (props.plugin.available === false) {
    error.value = props.plugin.unavailable_reason || "插件暂不可用";
    return;
  }
  if (saving.value || loading.value || issues.value.length) return;
  saving.value = true;
  error.value = "";
  try {
    const accepted = await api(
      base + "/config",
      { revision: revision.value, config: model.value },
      "PUT",
    );
    if (accepted?.success !== true)
      throw new Error(accepted?.message || "插件配置保存失败");
    if (!active) return;
    toast(props.plugin.name + "配置已保存");
    emit("saved");
  } catch (e) {
    error.value = errorText(e);
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
    max-width="60rem"
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
      <PluginDialogCloseBtn
        :disabled="saving"
        aria-label="关闭配置"
        @click="emit('close')"
      />
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
        <p v-if="loading" role="status">正在加载配置…</p>
        <VAlert
          v-if="plugin.available === false"
          type="warning"
          variant="tonal"
          class="ma-4"
          >{{ plugin.unavailable_reason || "插件暂不可用" }}</VAlert
        >
        <VAlert
          v-if="error"
          type="error"
          variant="tonal"
          class="plugin-form-error"
          >{{ error }}</VAlert
        >
        <VAlert
          v-for="issue in issues"
          :key="issue"
          type="error"
          variant="tonal"
          >{{ issue }}</VAlert
        >
        <template v-if="!loading && !issues.length">
          <PluginFormRenderer
            v-for="(node, index) in nodes"
            :key="index"
            :node="node"
            :model="model"
            @error="error = $event"
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
          :disabled="
            plugin.available === false ||
            loading ||
            !!issues.length ||
            (!nodes.length && !!error)
          "
          @click="save()"
          >保存</VBtn
        >
      </VCardActions>
    </VCard>
    <VCard v-else class="plugin-config-card">
      <PluginDialogCloseBtn v-if="!remote || error" :disabled="saving" aria-label="关闭配置" @click="emit('close')" />
      <VCardText class="pa-0">
        <VAlert
          v-if="supportNotice"
          type="warning"
          variant="tonal"
          class="mb-4"
          data-testid="plugin-runtime-support"
          >{{ supportNotice }}</VAlert
        >
        <p v-if="loading" class="pa-4" role="status">正在加载配置…</p>
        <VAlert
          v-if="plugin.available === false"
          type="warning"
          variant="tonal"
          class="ma-4"
          >{{ plugin.unavailable_reason || "插件暂不可用" }}</VAlert
        >
        <VAlert v-if="error" type="error" variant="tonal" class="ma-4">{{
          error
        }}</VAlert>
        <VProgressLinear
          v-if="saving"
          indeterminate
          aria-label="正在保存配置"
        />
        <component
          v-if="remote && !loading && !error"
          :is="remote"
          :initial-config="model"
          :api="remoteAPI"
          @save="saveRemote"
          @switch="!saving && emit('switch')"
          @close="!saving && emit('close')"
        />
      </VCardText>
    </VCard>
  </VDialog>
</template>
