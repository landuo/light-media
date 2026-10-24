// The server selects the installed package. No plugin name or caller-supplied
// URL can bypass this versioned contract or select a different module graph.
export const pluginFrontendContract = 'light-media.vue-federation/v1';
export const pluginAPIContract = 'light-media.plugin-api/v1';
export const pluginComponentNames = ['Config', 'Page', 'Dashboard', 'AppPage'] as const;
export type PluginComponentName = (typeof pluginComponentNames)[number];
export type PluginFrontendDescriptor = {
  instance_id: string;
  plugin_id: string;
  version: string;
  package_sha256: string;
  contract: typeof pluginFrontendContract;
  exposes: PluginComponentName[];
  shared: ('vue' | 'vuetify')[];
  api_contract: typeof pluginAPIContract;
  entry_path: string;
};
export function validPluginInstance(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/.test(value);
}
function stringSubset(value: unknown, allowed: readonly string[]) {
  return Array.isArray(value) && value.length > 0 &&
    value.every((item) => typeof item === 'string' && allowed.includes(item)) &&
    new Set(value).size === value.length;
}
export function parsePluginFrontendDescriptor(
  value: unknown,
  instance: string,
  name: PluginComponentName,
  expectedVersion?: string,
): PluginFrontendDescriptor {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('插件界面元数据无效');
  const item = value as PluginFrontendDescriptor;
  if (!validPluginInstance(instance) || item.instance_id !== instance ||
      !validPluginInstance(item.plugin_id) || typeof item.version !== 'string' ||
      !item.version.trim() || item.version.length > 128 ||
      typeof item.package_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(item.package_sha256))
    throw new Error('插件界面实例或版本信息无效');
  if (item.contract !== pluginFrontendContract || item.api_contract !== pluginAPIContract ||
      !stringSubset(item.exposes, pluginComponentNames) ||
      !stringSubset(item.shared, ['vue', 'vuetify']) || !item.shared.includes('vue'))
    throw new Error('插件界面契约不受支持');
  if (!pluginComponentNames.includes(name) || !item.exposes.includes(name))
    throw new Error('插件未声明此界面组件：' + name);
  if (expectedVersion !== undefined && item.version !== expectedVersion)
    throw new Error('插件版本已变化，请刷新后重试');
  const prefix = '/plugins/' + encodeURIComponent(instance) + '/frontend/' + item.package_sha256 + '/assets/';
  const relative = typeof item.entry_path === 'string' && item.entry_path.startsWith(prefix)
    ? item.entry_path.slice(prefix.length) : '';
  const parts = relative.split('/');
  if (!relative || parts.at(-1) !== 'remoteEntry.js' ||
      parts.some((part) => !/^[A-Za-z0-9_.-]+$/.test(part) || part === '.' || part === '..'))
    throw new Error('插件界面入口不属于当前安装包');
  return Object.freeze({ ...item, exposes: Object.freeze([...item.exposes]), shared: Object.freeze([...item.shared]) }) as unknown as PluginFrontendDescriptor;
}
