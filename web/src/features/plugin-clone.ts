import { appBasePath } from '../base-path.ts';
import { validPluginInstance } from './plugin-remote-contract.ts';
import type { PluginCard } from './plugin-forms';

export type PluginCloneRequest = {
  suffix: string;
  name: string;
  description: string;
  version: string;
  icon: string;
};

export type PluginCloneResult = {
  created: true;
  instance_id: string;
  plugin_id: string;
  source_instance_id: string;
};

export function emptyPluginClone(): PluginCloneRequest {
  return { suffix: '', name: '', description: '', version: '', icon: '' };
}

// A clone of a clone keeps the complete source identity. The display version
// and implementation plugin_id must never be used to construct this ID.
export function pluginCloneInstanceId(source: Pick<PluginCard, 'instance_id'>, suffix: string) {
  return source.instance_id + suffix.toLowerCase();
}

export function safePluginIcon(value: unknown, pathname?: string): string {
  if (
    typeof value !== 'string' ||
    !value ||
    value.trim() !== value ||
    /[\u0000-\u001f\u007f-\u009f\\]/u.test(value) ||
    /%(?![0-9a-f]{2})/i.test(value)
  )
    return '';
  if (value.startsWith('//')) return '';
  try {
    if (/^[a-z][a-z0-9+.-]*:/i.test(value)) {
      if (!/^https?:\/\/[^/]/.test(value)) return '';
      // URL normalizes an empty userinfo (https://@host/) away. The server
      // rejects any userinfo, including empty values, so check the authority.
      const authority = value.match(/^https?:\/\/([^/?#]+)/)?.[1];
      if (!authority || authority.includes('@')) return '';
      const url = new URL(value);
      return url.hostname && !url.username && !url.password ? url.href : '';
    }
    const base = appBasePath(pathname);
    // Manifest icon filenames refer to the bundled plugin_icon directory.
    // Other relative paths resolve within the application's public mount.
    const path = /^[A-Za-z0-9_.-]+\.(png|jpe?g|gif|webp|svg|ico)$/i.test(value)
      ? '/plugin_icon/' + value
      : value;
    const mounted = base && (path === base || path.startsWith(base + '/'));
    const url = new URL(
      mounted ? path : base + '/' + path.replace(/^\//, ''),
      'https://plugin.invalid',
    );
    if (url.origin !== 'https://plugin.invalid' || url.username || url.password) return '';
    // Do not let a relative resource escape a reverse-proxy mount via ../.
    if (base && !url.pathname.startsWith(base + '/')) return '';
    return url.pathname + url.search + url.hash;
  } catch {
    return '';
  }
}

export function pluginCloneErrors(
  source: Pick<PluginCard, 'instance_id'>,
  value: PluginCloneRequest,
): Partial<Record<keyof PluginCloneRequest, string>> {
  const errors: Partial<Record<keyof PluginCloneRequest, string>> = {};
  if (!validPluginInstance(source.instance_id))
    errors.suffix = '来源实例编号无效，请刷新插件列表';
  else if (!/^[A-Za-z0-9]{1,20}$/.test(value.suffix))
    errors.suffix = '分身后缀须为 1 至 20 位字母或数字';
  else if (new TextEncoder().encode(pluginCloneInstanceId(source, value.suffix)).length > 255)
    errors.suffix = '完整分身编号不能超过 255 个 UTF-8 字节，请缩短后缀';
  // Four display fields are literal strings, not executable/version/URL syntax.
  // Resource use remains independently guarded by safePluginIcon above.
  return errors;
}

export function pluginCloneErrorHint(code: unknown): string {
  if (code === 'PLUGIN_CLONE_CONFLICT')
    return '分身编号重复，包括已卸载但仍保留数据的实例；请更换后缀。';
  if (code === 'PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED')
    return '来源实例的专用迁移引用尚未转换，完成引用转换后才能创建分身。';
  return '';
}
