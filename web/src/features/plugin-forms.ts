import { pluginPageStyles } from './plugin-page-styles.ts';
import { pluginJSONComponents } from './plugin-json-components.ts';
import type { PluginRuntimeSupport } from './plugin-runtime-support';
export type PluginFormNode = {
  component: string;
  props?: Record<string, any>;
  content?: PluginFormNode[];
  text?: string | number | null;
  html?: string;
  slots?: Record<string, PluginFormNode | PluginFormNode[]>;
  events?: Record<string, { api: string; method: string; params?: Record<string, unknown> }>;
};

const supported = new Set([
  ...pluginJSONComponents,
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
for (const tag of [
  'div',
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
  supported.add(tag as any);
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

function validModelKey(value: unknown): boolean {
  // These are literal JSON object keys, not paths or JavaScript identifiers.
  // Prevent prototype mutation while preserving Unicode, punctuation and the
  // source's ordinary scalar-to-property-key conversion.
  return (
    (value === null || ['string', 'number', 'boolean'].includes(typeof value)) &&
    !['__proto__', 'constructor', 'prototype'].includes(String(value))
  );
}

function possibleLinkModelKey(value: unknown): boolean {
  // A bare model alias is resolved and URL-checked again by pluginFieldProps.
  // An invalid URL/scheme must not become an alias merely because object keys
  // can contain punctuation.
  return typeof value === 'string' && validModelKey(value) && !/[:/\\?#\u0000-\u001f]/u.test(value);
}


const isExpression = (value: unknown): value is string =>
  typeof value === 'string' && value.startsWith('{{') && value.endsWith('}}');
const expressionText = (value: string) => (isExpression(value) ? value.slice(2, -2).trim() : value);
function expression(value: string, model: Record<string, any>) {
  // Installed plugin code is trusted just like its Vue federation component.
  // The original JSON form supports expressions against its own draft model.
  return new Function('model', `with(model) { return (${expressionText(value)}) }`)(model);
}
function validExpression(value: unknown) {
  if (typeof value !== 'string') return false;
  try {
    new Function('model', `with(model) { return (${expressionText(value)}) }`);
    return true;
  } catch {
    return false;
  }
}
function validEventFunction(value: unknown): value is string {
  // The source invokes a parenthesized expression as an event function. This
  // also supports a factory expression returning a function, not only a
  // function/arrow literal at the beginning of the string.
  return typeof value === 'string' && validExpression(value);
}

// Fixed show/v-show evaluates the expression against the draft. Missing names
// raise ReferenceError; they must not silently hide a malformed plugin field.
export function pluginNodeVisible(props: Record<string, any>, model: Record<string, any>): boolean {
  if (!Object.hasOwn(props, 'show') && !Object.hasOwn(props, 'v-show')) return true;
  const value = Object.hasOwn(props, 'show') ? props.show : props['v-show'];
  // The source requires a string here; preserve its ordinary JS exception for
  // an invalid supplied value instead of coercing it into a visibility flag.
  const code = value.startsWith('{{') && value.endsWith('}}') ? value.slice(2, -2).trim() : value;
  return !!new Function('model', `with(model) { return ${code} }`)(model);
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
      node.component === 'style' &&
      pluginPageStyles(node.text) === null
    )
      issues.push('插件样式文本格式无效');
    const passwordAction = originalPasswordAction(node.props || {});
    for (const [key, value] of Object.entries(node.props || {})) {
      if (page && !['innerHTML', 'outerHTML', 'srcdoc'].includes(key)) {
        // Page/Dashboard v-bind uses literal props, not FormRender bindings.
        continue;
      } else if (key.startsWith('on') && typeof value !== 'string') {
        continue;
      } else if (['show', 'v-show'].includes(key) && validExpression(value)) {
        continue;
      } else if (passwordAction && Object.hasOwn(larkPasswordAction, key) && key !== 'model') {
        continue;
      } else if (
        ['model', 'modelvalue', 'v-model'].includes(key) ||
        key.startsWith('model:') ||
        key.startsWith('v-model:')
      ) {
        if (!validModelKey(value)) issues.push('插件表单字段名无效');
      } else if (
        (key.startsWith('on') && validEventFunction(value)) ||
        (isExpression(value) && validExpression(value))
      ) {
        continue;
      } else if (
        key.startsWith('on') ||
        ['innerHTML', 'outerHTML', 'srcdoc'].includes(key) ||
        ['show', 'v-show'].includes(key) ||
        (typeof value === 'string' && value.includes('{{'))
      ) {
        issues.push('插件动态属性或事件格式无效');
      }
    }
    if (node.html !== undefined && typeof node.html !== 'string') issues.push('插件富文本格式无效');
    if (node.slots)
      for (const slot of Object.values(node.slots))
        issues.push(
          ...pluginRenderIssues(Array.isArray(slot) ? slot : [slot], page, depth + 1, budget),
        );
    if (node.events)
      for (const [name, event] of Object.entries(node.events))
        if (
          !/^[A-Za-z][\w:-]*$/.test(name) ||
          !event ||
          typeof event.api !== 'string' ||
          typeof event.method !== 'string'
        )
          issues.push('插件页面事件格式无效');
    if (
      (node.component === 'a' || Object.hasOwn(node.props || {}, 'href')) &&
      !validPluginLink(node.props?.href) &&
      !(isExpression(node.props?.href) && validExpression(node.props?.href)) &&
      !possibleLinkModelKey(node.props?.href)
    )
      issues.push('插件链接地址无效');
    if (
      node.component === 'VImg' &&
      !/^\/plugin_icon\/[A-Za-z0-9_.-]+\.png$/.test(node.props?.src || '') &&
      !validPluginLink(node.props?.src) &&
      !isExpression(node.props?.src)
    )
      issues.push('插件图片地址无效');
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
  const bound: Record<string, any> = {};
  const passwordAction = originalPasswordAction(props);
  for (const [key, value] of Object.entries(props)) {
    if (
      ['model', 'modelvalue', 'v-model'].includes(key) ||
      key.startsWith('model:') ||
      key.startsWith('v-model:')
    ) {
      if (!validModelKey(value)) continue;
      const name = key.includes(':')
        ? key.split(':')[1]
        : key === 'modelvalue'
          ? 'value'
          : 'modelValue';
      if (!validModelKey(name)) continue;
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
    } else if (['show', 'v-show'].includes(key)) {
      if (!bound.style) bound.style = {};
      bound.style.display = pluginNodeVisible({ [key]: value }, model) ? '' : 'none';
    } else if (key.startsWith('on') && validEventFunction(value)) {
      bound[key] = (event: unknown) =>
        new Function('model', 'event', `with(model) { return (${value})(event) }`)(model, event);
    } else if (
      !key.startsWith('on') &&
      !['show', 'v-show'].includes(key) &&
      !['innerHTML', 'outerHTML', 'srcdoc'].includes(key)
    ) {
      const resolved = isExpression(value)
        ? expression(value, model)
        : typeof value === 'string' && Object.hasOwn(model, value)
          ? model[value]
          : value;
      if (key === 'href' && !validPluginLink(resolved)) continue;
      bound[key] = resolved;
    }
  }
  return bound;
}

export type PluginCard = {
  available?: boolean;
  unavailable_reason?: string;
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
  plugin_label?: string;
  add_time?: number;
  installed: boolean;
  state: boolean;
  has_update?: boolean;
  repo_url?: string;
  repository_version?: string;
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
