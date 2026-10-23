<script setup lang="ts">
import { h, defineAsyncComponent, type Component } from 'vue';
import { VForm } from 'vuetify/components/VForm';
import { VDivider } from 'vuetify/components/VDivider';
import { VListSubheader } from 'vuetify/components/VList';
import { VChip } from 'vuetify/components/VChip';
import { pluginPageStyles } from '../features/plugin-page-styles';
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
import { VAceEditor } from 'vue3-ace-editor';
import 'ace-builds/src-noconflict/mode-yaml';
import 'ace-builds/src-noconflict/theme-monokai';
import { pluginFieldProps, pluginNodeVisible, type PluginFormNode } from '../features/plugin-forms';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';

const props = defineProps<{
  node: PluginFormNode;
  model: Record<string, any>;
  pageScope?: string;
}>();
const components: Record<string, Component | string> = {
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
  VApexChart: defineAsyncComponent(() => import('./PluginPieChart.vue')),
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
function render(node: PluginFormNode): any {
  if (node.component === 'style') {
    const css = props.pageScope ? pluginPageStyles(node.text, props.pageScope) : null;
    return css === null ? null : h('style', {}, css);
  }
  const component = components[node.component];
  if (!component) return null; // The dialog blocks save and reports unsupported nodes.
  const bound = pluginFieldProps(node.props || {}, props.model, node.component);
  if (Object.hasOwn(node.props || {}, 'show'))
    bound.style = [
      bound.style,
      { display: pluginNodeVisible(node.props || {}, props.model) ? '' : 'none' },
    ];
  if (node.component === 'VSubheader') {
    bound.title = bound.text;
    delete bound.text;
  }
  if (node.component === 'a') bound.rel = 'noopener noreferrer';
  if (node.component === 'VImg' && /^\/plugin_icon\/[A-Za-z0-9_.-]+\.png$/.test(bound.src || ''))
    bound.src = import.meta.env.BASE_URL + bound.src.slice(1);
  if (node.component === 'VAceEditor')
    bound.options = { useWorker: false, fontSize: 14, tabSize: 2 };
  return h(
    component,
    bound,
    node.component === 'VAceEditor' || (node.text === undefined && !node.content?.length)
      ? undefined
      : {
          default: () =>
            node.text !== undefined
              ? node.text === null
                ? ''
                : String(node.text)
              : (node.content || []).map(render),
        },
  );
}
</script>
<template><component :is="render(node)" /></template>
