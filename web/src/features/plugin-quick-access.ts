import type { PluginCard } from './plugin-forms';
import { validPluginInstance } from './plugin-remote-contract.ts';

export const recentPluginKey = 'LIGHT_MEDIA_RECENT_PLUGINS';
export function pluginQuickAccessItems(cards: PluginCard[]) {
  return cards
    .filter((card) => card.has_page && validPluginInstance(card.instance_id))
    .sort((a, b) => (a.name || '').localeCompare(b.name || ''));
}
export function pluginRecentItems(raw: string | null): PluginCard[] {
  try {
    const parsed = JSON.parse(raw || '[]');
    return Array.isArray(parsed)
      ? parsed
          .filter((item) => item?.has_page && validPluginInstance(item.instance_id))
          .sort((a, b) => b.access_time - a.access_time)
      : [];
  } catch {
    return [];
  }
}
export function pluginRememberRecent(
  previous: PluginCard[],
  plugin: PluginCard,
  at = Date.now(),
): PluginCard[] {
  if (!plugin.has_page || !validPluginInstance(plugin.instance_id)) return previous;
  // UI metadata only: configuration and plugin data never enter localStorage.
  const { instance_id, plugin_id, name, icon, version, state, has_page } = plugin;
  const item = {
    instance_id,
    plugin_id,
    name,
    icon,
    version,
    state,
    has_page,
    installed: true,
    access_time: at,
  } as unknown as PluginCard;
  return [item, ...previous.filter((item) => item.instance_id !== plugin.instance_id)].slice(0, 3);
}
export function pluginPullDistance(startY: number, currentY: number) {
  const delta = currentY - startY;
  return delta > 20 ? Math.min(200, delta * 0.75) : 0;
}
