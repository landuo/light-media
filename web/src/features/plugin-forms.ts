import { pluginPageStyles } from './plugin-page-styles.ts';
import type { PluginRuntimeSupport } from './plugin-runtime-support';
export type PluginFormNode = {
  component: string;
  props?: Record<string, any>;
  content?: PluginFormNode[];
  text?: string | number | null;
};

const supported = new Set([
  'VForm',
  'VDivider',
  'VSubheader',
  'VRow',
  'VCol',
  'VSwitch',
  'VAceEditor',
  'VAlert',
  'VTextField',
  'VCronField',
  'VTextarea',
  'VSelect',
  'VCard',
  'VTabs',
  'VTab',
  'VWindow',
  'VWindowItem',
  'VDialog',
  'VDialogCloseBtn',
  'a',
  'span',
  'u',
]);
const tableElements = new Set([
  'style',
  'VSpacer',
  'VChip',
  'VTable',
  'thead',
  'tbody',
  'tr',
  'th',
  'td',
  'div',
  'span',
  'VCard',
  'VCardTitle',
  'VCardText',
  'VIcon',
  'VAvatar',
  'VImg',
  'VApexChart',
]);
const plainTextComponents = new Set(['span', 'u']);
const plainTextStyles = new Set([
  'color',
  'fontWeight',
  'fontStyle',
  'fontSize',
  'textDecoration',
  'whiteSpace',
  'margin',
  'marginLeft',
  'marginRight',
]);
function validPlainTextStyle(value: unknown): boolean {
  return (
    !!value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.entries(value).every(
      ([key, v]) =>
        plainTextStyles.has(key) &&
        ((typeof v === 'number' && Number.isFinite(v) && Math.abs(v) <= 10000) ||
          (typeof v === 'string' && /^[#A-Za-z0-9 .,%\-]{0,80}$/.test(v))),
    )
  );
}
function validPlainTextProps(props: Record<string, any>): boolean {
  return Object.entries(props).every(([key, value]) =>
    key === 'class'
      ? typeof value === 'string' && /^[A-Za-z0-9_ -]{0,256}$/.test(value)
      : key === 'title'
        ? typeof value === 'string' && value.length <= 2048
        : key === 'style' && validPlainTextStyle(value),
  );
}
function plainTextProps(props: Record<string, any>): Record<string, any> {
  return Object.fromEntries(
    Object.entries(props).filter(([key, value]) => validPlainTextProps({ [key]: value })),
  );
}

const modelKey = /^[A-Za-z_][A-Za-z0-9_.-]{0,255}$/;

// The installed Lark form declares exactly this password visibility action.
// Translate that one native behavior; no server-provided JavaScript executes.
const larkPasswordAction = {
  model: 'app_secret',
  type: "{{ app_secret_visible ? 'text' : 'password' }}",
  'append-inner-icon': "{{ app_secret_visible ? 'mdi-eye-off' : 'mdi-eye' }}",
  'onClick:append-inner': 'function(e){ model.app_secret_visible = !model.app_secret_visible }',
};
function originalPasswordAction(props: Record<string, any>): boolean {
  return Object.entries(larkPasswordAction).every(([key, value]) => props[key] === value);
}
function passwordVisible(model: Record<string, any>): boolean {
  const value = model.app_secret_visible;
  return Object.hasOwn(model, 'app_secret_visible') && !!value;
}

function validModelKey(value: any): value is string {
  return (
    typeof value === 'string' &&
    modelKey.test(value) &&
    !value.split('.').some((key) => ['__proto__', 'constructor', 'prototype'].includes(key))
  );
}

function visibilityModelKey(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const match = /^\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$/.exec(value);
  return match && validModelKey(match[1]) ? match[1] : null;
}

// A single literal model key, not expression evaluation or property traversal.
// Hiding a field never deletes or rewrites its saved value.
export function pluginNodeVisible(props: Record<string, any>, model: Record<string, any>): boolean {
  if (!Object.hasOwn(props, 'show')) return true;
  const key = visibilityModelKey(props.show);
  if (!key) return false;
  const current = model[key]; // Track an initially absent reactive key.
  return Object.hasOwn(model, key) && !!current;
}

export function validPluginLink(value: unknown): value is string {
  if (typeof value !== 'string' || !/^https?:\/\//.test(value)) return false;
  try {
    const url = new URL(value);
    return !!url.hostname && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function pluginFormIssues(nodes: PluginFormNode[], depth = 0): string[] {
  return pluginRenderIssues(nodes, false, depth, { remaining: 4096 });
}

export function pluginPageIssues(nodes: PluginFormNode[]): string[] {
  return pluginRenderIssues(nodes, true, 0, { remaining: 65536 });
}

function pluginRenderIssues(
  nodes: PluginFormNode[],
  page: boolean,
  depth: number,
  budget: { remaining: number },
): string[] {
  if (
    !Array.isArray(nodes) ||
    depth > 16 ||
    nodes.length > (page ? 16384 : 256) ||
    (budget.remaining -= nodes.length) < 0
  )
    return [page ? '插件页面结构无效或超过显示限制' : '插件表单结构无效或超过限制'];
  const issues: string[] = [];
  for (const node of nodes) {
    if (!node || (!supported.has(node.component) && !(page && tableElements.has(node.component)))) {
      issues.push('尚未实现的插件组件：' + String(node?.component || 'unknown'));
      continue;
    }
    if (
      node.component === 'VSubheader' &&
      !Object.entries(node.props || {}).every(
        ([key, value]) => key === 'text' && typeof value === 'string' && value.length <= 2048,
      )
    )
      issues.push('插件小标题属性无效');
    if (node.component === 'VSpacer' && Object.keys(node.props || {}).length)
      issues.push('插件间隔组件属性无效');
    if (
      node.component === 'VChip' &&
      !Object.entries(node.props || {}).every(
        ([key, value]) =>
          ['color', 'size', 'variant', 'prepend-icon', 'append-icon'].includes(key) &&
          typeof value === 'string' &&
          /^[A-Za-z0-9_-]{1,64}$/.test(value),
      )
    )
      issues.push('插件标签组件属性无效');
    if (
      node.component === 'style' &&
      (pluginPageStyles(node.text) === null ||
        Object.keys(node.props || {}).length ||
        node.content !== undefined)
    )
      issues.push('插件页面样式不在安全展示范围内');
    if (plainTextComponents.has(node.component) && !validPlainTextProps(node.props || {}))
      issues.push('插件文本组件属性无效');
    const passwordAction = originalPasswordAction(node.props || {});
    for (const [key, value] of Object.entries(node.props || {})) {
      if (key === 'show' && visibilityModelKey(value)) {
        continue;
      } else if (passwordAction && Object.hasOwn(larkPasswordAction, key) && key !== 'model') {
        continue;
      } else if (['model', 'modelvalue', 'v-model'].includes(key)) {
        if (!validModelKey(value)) issues.push('插件表单字段名无效');
      } else if (
        key.startsWith('on') ||
        ['innerHTML', 'outerHTML', 'srcdoc'].includes(key) ||
        key.includes('show') ||
        (typeof value === 'string' && value.includes('{{'))
      ) {
        issues.push('此插件的动态表单行为尚未移植');
      }
    }
    if ('html' in node || 'slots' in node) issues.push('此插件的富文本或插槽行为尚未移植');
    if (node.component === 'a' && !validPluginLink(node.props?.href))
      issues.push('插件链接地址无效');
    if (
      node.component === 'VImg' &&
      !/^\/plugin_icon\/[A-Za-z0-9_.-]+\.png$/.test(node.props?.src || '')
    )
      issues.push('插件图片地址无效');
    if (node.component === 'VApexChart' && !validPluginPieChart(node.props || {}))
      issues.push('插件图表结构无效或尚未移植');
    if (node.content) issues.push(...pluginRenderIssues(node.content, page, depth + 1, budget));
  }
  return [...new Set(issues)];
}

export function validPluginPieChart(props: Record<string, any>): boolean {
  const keys = (value: any, allowed: string[]) =>
    value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).every((key) => allowed.includes(key));
  const o = props.options;
  return !!(
    keys(props, ['height', 'options', 'series']) &&
    Number.isFinite(props.height) &&
    props.height >= 100 &&
    props.height <= 600 &&
    keys(o, ['chart', 'labels', 'title', 'legend', 'plotOptions', 'noData']) &&
    keys(o.chart, ['type']) &&
    o.chart.type === 'pie' &&
    Array.isArray(o.labels) &&
    o.labels.length <= 2048 &&
    o.labels.every((label: any) => typeof label === 'string' && label.length <= 256) &&
    Array.isArray(props.series) &&
    props.series.length === o.labels.length &&
    props.series.every(
      (value: any) => typeof value === 'number' && Number.isFinite(value) && value >= 0,
    ) &&
    keys(o.title, ['text']) &&
    typeof o.title.text === 'string' &&
    o.title.text.length <= 512 &&
    keys(o.legend, ['show']) &&
    typeof o.legend.show === 'boolean' &&
    keys(o.plotOptions, ['pie']) &&
    keys(o.plotOptions.pie, ['expandOnClick']) &&
    typeof o.plotOptions.pie.expandOnClick === 'boolean' &&
    keys(o.noData, ['text']) &&
    typeof o.noData.text === 'string' &&
    o.noData.text.length <= 256
  );
}

export function pluginChartHTML(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function pluginFieldProps(
  props: Record<string, any>,
  model: Record<string, any>,
  component?: string,
) {
  if (component && plainTextComponents.has(component)) return plainTextProps(props);
  const bound: Record<string, any> = {};
  const passwordAction = originalPasswordAction(props);
  for (const [key, value] of Object.entries(props)) {
    if (['model', 'modelvalue', 'v-model'].includes(key)) {
      if (!validModelKey(value)) continue;
      const name = key === 'modelvalue' ? 'value' : 'modelValue';
      // Original dotted names are literal dictionary keys, never property
      // traversal. A missing key must not resolve an inherited object member.
      const current = model[value]; // Track missing reactive keys before the own-key check.
      bound[name] = Object.hasOwn(model, value) ? current : undefined;
      bound['onUpdate:' + name] = (updated: any) => {
        model[value] = updated;
      };
    } else if (passwordAction && key === 'type') {
      bound.type = passwordVisible(model) ? 'text' : 'password';
    } else if (passwordAction && key === 'append-inner-icon') {
      bound[key] = passwordVisible(model) ? 'mdi-eye-off' : 'mdi-eye';
    } else if (passwordAction && key === 'onClick:append-inner') {
      bound[key] = () => {
        model.app_secret_visible = !passwordVisible(model);
      };
    } else if (
      !key.startsWith('on') &&
      !key.includes('show') &&
      (key !== 'href' || validPluginLink(value)) &&
      !['innerHTML', 'outerHTML', 'srcdoc'].includes(key)
    ) {
      bound[key] = value;
    }
  }
  return bound;
}

export type PluginCard = {
  runtime_support?: PluginRuntimeSupport;
  plugin_id: string;
  instance_id: string;
  name: string;
  description: string;
  version: string;
  display_version?: string;
  icon?: string;
  source_instance_id?: string;
  author: string;
  author_url: string;
  installed: boolean;
  state: boolean;
  has_update?: boolean;
  update_blocked_reason?: string;
  update_review_pending?: boolean;
  restore_review?: { version: string; generation: number; revision: number };
  restore_review_blocked_reason?: string;
  update?: {
    from_version: string;
    target_version: string;
    generation: number;
    revision: number;
  };
  has_page?: boolean;
  dashboards?: string[];
};
