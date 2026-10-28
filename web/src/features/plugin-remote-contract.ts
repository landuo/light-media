// The server selects the installed package. No plugin name or caller-supplied
// URL can bypass this versioned contract or select a different module graph.
export const pluginFrontendContract = 'light-media.vue-federation/v1';
export const pluginAPIContract = 'light-media.plugin-api/v1';
export const pluginComponentNames = ['Config', 'Page', 'Dashboard', 'AppPage', 'AuthPage'] as const;
// Providers may select a custom component, but it must be a bounded name
// declared by the installed package, never an import path or a remote URL.
export type PluginComponentName = string;
export function validPluginComponent(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(value);
}
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
  return typeof value === 'string' && new TextEncoder().encode(value).length <= 255 && /^[\p{L}\p{N}][^/\\\p{Cc}\p{Cf}\p{Zl}\p{Zp}]*$/u.test(value);
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
      !Array.isArray(item.exposes) || item.exposes.length === 0 || item.exposes.length > 64 ||
      !item.exposes.every(validPluginComponent) || new Set(item.exposes).size !== item.exposes.length ||
      !stringSubset(item.shared, ['vue', 'vuetify']) || !item.shared.includes('vue'))
    throw new Error('插件界面契约不受支持');
  if (!validPluginComponent(name) || !item.exposes.includes(name))
    throw new Error('插件未声明此界面组件：' + name);
  if (expectedVersion !== undefined && item.version !== expectedVersion)
    throw new Error('插件版本已变化，请刷新后重试');
  let prefix = '';
  if (typeof item.entry_path === 'string') {
    const ownerPrefix = '/plugins/';
    const spelling = item.entry_path.startsWith(ownerPrefix) ? item.entry_path.slice(ownerPrefix.length).split('/')[0]! : '';
    try {
      // Go PathEscape and encodeURIComponent have different reserved-byte
      // spellings. Compare the exact decoded ID, while requiring a path-only
      // URL and the immutable package pin to remain unchanged.
      const parsed = new URL(item.entry_path, 'https://plugin.invalid');
      if (spelling && decodeURIComponent(spelling) === instance && parsed.pathname === item.entry_path && !parsed.search && !parsed.hash)
        prefix = ownerPrefix + spelling + '/frontend/' + item.package_sha256 + '/assets/';
    } catch { /* invalid URI is rejected below */ }
  }
  const relative = prefix && typeof item.entry_path === 'string' && item.entry_path.startsWith(prefix)
    ? item.entry_path.slice(prefix.length) : '';
  const parts = relative.split('/');
  if (!relative || parts.at(-1) !== 'remoteEntry.js' ||
      parts.some((part) => { try { const name = decodeURIComponent(part); return !/^[\p{L}\p{N}_][^/\\\p{Cc}\p{Cf}\p{Zl}\p{Zp}]*$/u.test(name) || new TextEncoder().encode(name).length > 255 || name === '.' || name === '..'; } catch { return true; } }))
    throw new Error('插件界面入口不属于当前安装包');
  return Object.freeze({ ...item, exposes: Object.freeze([...item.exposes]), shared: Object.freeze([...item.shared]) }) as unknown as PluginFrontendDescriptor;
}
