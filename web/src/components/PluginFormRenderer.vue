<script setup lang="ts">
import { h, ref, reactive, watchEffect, getCurrentInstance, defineAsyncComponent, type Component } from 'vue';
import * as VuetifyComponents from 'vuetify/components';
import { VForm } from 'vuetify/components/VForm';
import { VDivider } from 'vuetify/components/VDivider';
import { VListSubheader } from 'vuetify/components/VList';
import { VChip } from 'vuetify/components/VChip';
import { VRow, VCol, VSpacer } from 'vuetify/components/VGrid';
import { VSwitch } from 'vuetify/components/VSwitch';
import { VAlert } from 'vuetify/components/VAlert';
import { VTextField } from 'vuetify/components/VTextField';
import { VTextarea } from 'vuetify/components/VTextarea';
import { VSelect } from 'vuetify/components/VSelect';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VWindow, VWindowItem } from 'vuetify/components/VWindow';
import { VDialog } from 'vuetify/components/VDialog';
import { VTable } from 'vuetify/components/VTable';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VIcon } from 'vuetify/components/VIcon';
import { VAvatar } from 'vuetify/components/VAvatar';
import { VImg } from 'vuetify/components/VImg';
const VAceEditor = Object.assign(defineAsyncComponent(async () => {
  await import('../features/plugin-ace');
  return (await import('vue3-ace-editor')).VAceEditor;
}), { name: 'VAceEditor' });
import { pluginFieldProps, type PluginFormNode } from '../features/plugin-forms';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';

const props = defineProps<{
  node: PluginFormNode;
  model: Record<string, any>;
  pageScope?: string;
  view?: 'form' | 'page' | 'dashboard';
  action?: (event: NonNullable<PluginFormNode['events']>[string]) => void | Promise<void>;
}>();
const emit = defineEmits<{ error: [message: string] }>();
const renderError = ref('');
const selfComponent = getCurrentInstance()?.type || 'PluginFormRenderer';
const pageNodeEvents = reactive<Record<string, (...args: any[]) => unknown>>({});
watchEffect(() => {
  if (props.view !== 'page') return;
  // Fixed PageRender adds/replaces listeners but does not remove missing keys.
  // Each recursive PageRender owns this map until that node is unmounted.
  for (const [name, event] of Object.entries(props.node.events || {})) {
    pageNodeEvents['on' + name.charAt(0).toUpperCase() + name.slice(1)] = async () => {
      try {
        if (!props.action) throw new Error('此视图不能执行插件页面操作');
        await props.action(event);
      } catch (error) { reportError(error); }
    };
  }
});

const components: Record<string, Component | string> = {
  ...VuetifyComponents,
  VForm,
  VDivider,
  VSubheader: VListSubheader,
  VSpacer,
  VChip,
  VRow,
  VCol,
  VSwitch,
  VAlert,
  VTextField,
  VTextarea,
  VSelect,
  VTabs,
  VTab,
  VWindow,
  VWindowItem,
  VDialog,
  VDialogCloseBtn: PluginDialogCloseBtn,
  VCronField: defineAsyncComponent(() => import('./PluginCronField.vue')),
  VAceEditor,
  VTable,
  VCard,
  VCardTitle,
  VCardText,
  VIcon,
  VAvatar,
  VImg,
  VApexChart: defineAsyncComponent(() => import('vue3-apexcharts')),
  div: 'div',
  span: 'span',
  u: 'u',
  a: 'a',
  thead: 'thead',
  tbody: 'tbody',
  tr: 'tr',
  th: 'th',
  td: 'td',
};
for (const tag of [
  'p',
  'b',
  'strong',
  'em',
  'i',
  'small',
  'br',
  'ul',
  'ol',
  'li',
  'pre',
  'code',
  'h1',
  'h2',
  'h3',
  'h4',
  'label',
  'section',
  'article',
  'style',
])
  components[tag] = tag;
function render(node: PluginFormNode): any {
  try {
    const component = components[node.component];
    if (!component) return null; // The dialog blocks save and reports unsupported nodes.
    const formView = !props.view || props.view === 'form';
    const bound = formView
      ? pluginFieldProps(node.props || {}, props.model, node.component)
      : { ...(node.props || {}) };
    if (node.component === 'VSubheader') {
      bound.title = bound.text;
      delete bound.text;
    }
    if (node.component === 'a') bound.rel = 'noopener noreferrer';
    if (node.component === 'VImg' && /^\/plugin_icon\/[A-Za-z0-9_.-]+\.png$/.test(bound.src || ''))
      bound.src = import.meta.env.BASE_URL + bound.src.slice(1);
    if (props.view === 'page') Object.assign(bound, pageNodeEvents);
    for (const [name, handler] of Object.entries(bound)) {
      if (name.startsWith('on') && typeof handler === 'function')
        bound[name] = (...args: any[]) => {
          try {
            Promise.resolve(handler(...args)).catch(reportError);
          } catch (error) {
            reportError(error);
          }
        };
    }
    const slots = Object.fromEntries(
      Object.entries(props.view === 'page' ? {} : node.slots || {}).map(([name, content]) => [
        name,
        () => Array.isArray(content) ? content.map(render) : render(content),
      ]),
    );
    if (node.html && !formView) bound.innerHTML = node.html;
    return h(component, bound,
      node.html && !formView ? undefined : {
        ...slots,
        default: () => {
          if (!formView)
            return [
              node.text === undefined || node.text === null ? '' : String(node.text),
              ...(node.content || []).map((child, index) =>
                props.view === 'page'
                  ? h(selfComponent as Component, { key: index, node: child, model: props.model, view: 'page', action: props.action, onError: (message: string) => emit('error', message) })
                  : render(child)),
            ];
          // FormRender wraps its HTML in the same component inside the
          // default slot, and tests text by JS truthiness before content.
          if (node.html) return h(component, { innerHTML: node.html });
          if (node.text) return typeof node.text === 'string' ? node.text : props.model[node.text];
          return (node.content || []).map(render);
        },
      });

  } catch (error) {
    reportError(error);
    return null;
  }
}
function reportError(error: unknown) {
  renderError.value = error instanceof Error ? error.message : '插件界面执行失败';
  emit('error', renderError.value);
}
</script>
<template>
  <component :is="render(node)" /><VAlert v-if="renderError" type="error">{{ renderError }}</VAlert>
</template>

<style src="../assets/plugin-utilities.css"></style>
