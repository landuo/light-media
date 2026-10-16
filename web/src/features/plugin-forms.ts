export type PluginFormNode = {
  component: string;
  props?: Record<string, any>;
  content?: PluginFormNode[];
  text?: string;
};

const supported = new Set([
  'VForm',
  'VRow',
  'VCol',
  'VSwitch',
  'VAceEditor',
  'VAlert',
  'VTextField',
  'VTextarea',
  'VSelect',
]);
const modelKey = /^[A-Za-z][A-Za-z0-9_]*$/;

export function pluginFormIssues(nodes: PluginFormNode[], depth = 0): string[] {
  if (!Array.isArray(nodes) || depth > 16 || nodes.length > 256)
    return ['插件表单结构无效或超过限制'];
  const issues: string[] = [];
  for (const node of nodes) {
    if (!node || !supported.has(node.component)) {
      issues.push('尚未实现的插件组件：' + String(node?.component || 'unknown'));
      continue;
    }
    for (const [key, value] of Object.entries(node.props || {})) {
      if (['model', 'modelvalue', 'v-model'].includes(key)) {
        if (typeof value !== 'string' || !modelKey.test(value)) issues.push('插件表单字段名无效');
      } else if (
        key.startsWith('on') ||
        key.includes('show') ||
        (typeof value === 'string' && value.includes('{{'))
      ) {
        issues.push('此插件的动态表单行为尚未移植');
      }
    }
    if ('html' in node || 'slots' in node) issues.push('此插件的富文本或插槽行为尚未移植');
    if (node.content) issues.push(...pluginFormIssues(node.content, depth + 1));
  }
  return [...new Set(issues)];
}

export function pluginFieldProps(props: Record<string, any>, model: Record<string, any>) {
  const bound: Record<string, any> = {};
  for (const [key, value] of Object.entries(props)) {
    if (['model', 'modelvalue', 'v-model'].includes(key)) {
      if (typeof value !== 'string' || !modelKey.test(value)) continue;
      const name = key === 'modelvalue' ? 'value' : 'modelValue';
      bound[name] = model[value];
      bound['onUpdate:' + name] = (updated: any) => {
        model[value] = updated;
      };
    } else if (!key.startsWith('on') && !key.includes('show')) {
      bound[key] = value;
    }
  }
  return bound;
}

export type PluginCard = {
  plugin_id: string;
  instance_id: string;
  name: string;
  description: string;
  version: string;
  author: string;
  author_url: string;
  installed: boolean;
  state: boolean;
};
