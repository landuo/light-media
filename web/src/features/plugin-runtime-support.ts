// Descriptive metadata only. Never use this notice to grant host capabilities.
export type PluginRuntimeSupport = { platforms?: string[]; storage_types?: string[] };
function identifiers(value: unknown): string[] {
  if (
    !Array.isArray(value) ||
    value.length > 16 ||
    value.some((v) => typeof v !== 'string' || !/^[a-z][a-z0-9_-]{0,31}$/.test(v)) ||
    new Set(value).size !== value.length
  )
    return [];
  return value;
}
export function pluginRuntimeSupportNotice(value: unknown): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  const support = value as PluginRuntimeSupport;
  const platforms = identifiers(support.platforms);
  const storage = identifiers(support.storage_types);
  const parts: string[] = [];
  if (platforms.length)
    parts.push('平台 ' + platforms.map((v) => (v === 'linux' ? 'Linux' : v)).join('、'));
  if (storage.length)
    parts.push('存储 ' + storage.map((v) => (v === 'local' ? '本地（local）' : v)).join('、'));
  if (!parts.length) return '';
  return (
    '当前构建声明的支持范围：' +
    parts.join('；') +
    '。以此范围为准，原版表单中的其他平台或存储示例不代表当前已支持。'
  );
}
