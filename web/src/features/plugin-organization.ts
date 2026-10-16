import type { PluginCard } from './plugin-forms';

export type FolderConfig = {
  plugins?: string[];
  order?: number;
  background?: string;
  icon?: string;
  color?: string;
  gradient?: string;
  showIcon?: boolean;
  [key: string]: unknown;
};
export type FolderValue = string[] | FolderConfig;
export type PluginOrder = { type: 'plugin' | 'folder'; id: string; order: number };
export type PluginOrganization = {
  revision: number;
  folders: Record<string, FolderValue>;
  order: (
    string | { id: string; type?: 'plugin' | 'folder'; order?: number; [key: string]: unknown }
  )[];
};
export type PluginGridItem =
  | { type: 'folder'; id: string; count: number; config: FolderConfig }
  | { type: 'plugin'; id: string; plugin: PluginCard };

export type InstalledPluginFilters = { running?: boolean; updates?: boolean };

// Match the reference view: either quick filter takes precedence over the name;
// when both quick filters are selected they are combined with AND.
export function installedPluginMatches(
  plugin: PluginCard,
  query: string,
  filters: InstalledPluginFilters = {},
): boolean {
  if (filters.updates && filters.running) return !!plugin.has_update && plugin.state;
  if (filters.updates) return !!plugin.has_update;
  if (filters.running) return plugin.state;
  return plugin.name.toLowerCase().includes(query.toLowerCase());
}

// Presets copied from the reference PluginFolderSettingsDialog, not redesigned.
export const folderIcons = [
  'mdi-folder',
  'mdi-folder-star',
  'mdi-folder-heart',
  'mdi-folder-cog',
  'mdi-folder-music',
  'mdi-folder-image',
  'mdi-folder-video',
  'mdi-folder-download',
  'mdi-folder-network',
  'mdi-folder-special',
];

export const folderColors = [
  '#2196F3',
  '#4CAF50',
  '#FF9800',
  '#9C27B0',
  '#F44336',
  '#607D8B',
  '#795548',
  '#E91E63',
];

export const folderGradients = [
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(33, 150, 243, 0.7) 0%, rgba(33, 150, 243, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(76, 175, 80, 0.7) 0%, rgba(76, 175, 80, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(255, 152, 0, 0.7) 0%, rgba(255, 152, 0, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(156, 39, 176, 0.7) 0%, rgba(156, 39, 176, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(244, 67, 54, 0.7) 0%, rgba(244, 67, 54, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(96, 125, 139, 0.7) 0%, rgba(96, 125, 139, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(233, 30, 99, 0.7) 0%, rgba(233, 30, 99, 0.8) 100%)',
  'linear-gradient(rgba(0, 0, 0, 0.6) 0%, rgba(0, 0, 0, 0.4) 100%), linear-gradient(135deg, rgba(63, 81, 181, 0.7) 0%, rgba(156, 39, 176, 0.8) 100%)',
];

export function folderMembers(value: FolderValue | undefined): string[] {
  return Array.isArray(value) ? value : value?.plugins || [];
}
export function folderAppearance(value: FolderValue | undefined): FolderConfig {
  return !value || Array.isArray(value) ? {} : value;
}
export function replaceMembers(value: FolderValue, members: string[]): FolderValue {
  return Array.isArray(value) ? members : { ...value, plugins: members };
}
export function copyOrganization(value: PluginOrganization): PluginOrganization {
  return JSON.parse(JSON.stringify(value));
}
export function normalizedPluginOrder(value: PluginOrganization): PluginOrder[] {
  const first = value.order[0];
  const modern = typeof first === 'object' && first !== null && 'type' in first;
  return value.order.map((item, index) =>
    typeof item === 'string'
      ? { type: 'plugin', id: item, order: index }
      : {
          ...item,
          type: modern ? item.type || 'plugin' : 'plugin',
          order: modern ? (item.order ?? index) : index,
        },
  );
}
export function organizationGrid(
  value: PluginOrganization,
  plugins: PluginCard[],
  folder: string,
  query: string,
  filters: InstalledPluginFilters = {},
): PluginGridItem[] {
  const byID = new Map(plugins.map((plugin) => [plugin.instance_id, plugin]));
  const match = (plugin: PluginCard) => installedPluginMatches(plugin, query, filters);
  const filtering = !!query || filters.running || filters.updates;
  if (folder)
    return folderMembers(value.folders[folder]).flatMap((id) => {
      const plugin = byID.get(id);
      return plugin && match(plugin) ? [{ type: 'plugin' as const, id, plugin }] : [];
    });
  const assigned = new Set(Object.values(value.folders).flatMap(folderMembers));
  const folders: PluginGridItem[] = Object.keys(value.folders)
    .map((id) => ({
      type: 'folder' as const,
      id,
      count: folderMembers(value.folders[id]).filter((id) => {
        const plugin = byID.get(id);
        return plugin && match(plugin);
      }).length,
      config: folderAppearance(value.folders[id]),
    }))
    .filter((item) => !filtering || item.count > 0);
  folders.sort(
    (a, b) =>
      (a.type === 'folder' ? (a.config.order ?? 999) : 999) -
      (b.type === 'folder' ? (b.config.order ?? 999) : 999),
  );
  const all = [
    ...folders,
    ...plugins
      .filter((plugin) => !assigned.has(plugin.instance_id) && match(plugin))
      .map((plugin) => ({ type: 'plugin' as const, id: plugin.instance_id, plugin })),
  ];
  const order = new Map(
    normalizedPluginOrder(value).map((item) => [item.type + ':' + item.id, item.order]),
  );
  return all.sort(
    (a, b) => (order.get(a.type + ':' + a.id) ?? 999) - (order.get(b.type + ':' + b.id) ?? 999),
  );
}
export function moveToFolder(
  value: PluginOrganization,
  pluginID: string,
  folder: string,
): PluginOrganization {
  const next = copyOrganization(value);
  if (!Object.hasOwn(next.folders, folder)) throw new Error('文件夹不存在');
  if (Object.hasOwn(next.folders, pluginID)) throw new Error('文件夹不能嵌套');
  if (folderMembers(next.folders[folder]).includes(pluginID)) throw new Error('插件已在此文件夹中');
  for (const name of Object.keys(next.folders))
    next.folders[name] = replaceMembers(
      next.folders[name],
      folderMembers(next.folders[name]).filter((id) => id !== pluginID),
    );
  next.folders[folder] = replaceMembers(next.folders[folder], [
    ...folderMembers(next.folders[folder]),
    pluginID,
  ]);

  return next;
}
export function safeFolderBackground(value: unknown): string {
  if (typeof value !== 'string' || !value) return '';
  try {
    const url = new URL(value);
    return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password
      ? url.href
      : '';
  } catch {
    return '';
  }
}
