import type { PluginCard } from './plugin-forms';

export type PluginMarketFilter = {
  name: string;
  authors: string[];
  labels: string[];
  repositories: string[];
};
export type PluginMarketSort = 'count' | 'name' | 'author' | 'repo_url' | 'add_time';
export function pluginMarketLabels(card: PluginCard): string[] {
  return (card.plugin_label || '').split(',').filter(Boolean);
}
// Fixed search dialog compares each field separately. The market filter
// intentionally compares the joined name/description, as its own watcher does.
export function pluginSearchMatches(card: PluginCard, keyword: string): boolean {
  const value = keyword.toLowerCase();
  return card.name?.toLowerCase().includes(value) || card.description?.toLowerCase().includes(value) || false;
}
export function pluginMarketRepository(card: Pick<PluginCard, 'repo_url'>): string {
  const value = card.repo_url || '';
  if (value.startsWith('local://')) {
    try { return new URL(value).searchParams.get('path') || '本地'; }
    catch { return decodeURIComponent(value.match(/[?&]path=([^&]+)/)?.[1] || '') || '本地'; }
  }
  return value.replace('https://github.com/', '').replace('https://raw.githubusercontent.com/', '');
}
export function pluginMarketRepositoryOptions(cards: PluginCard[]): string[] {
  const options: string[] = [];
  for (const card of cards) {
    const value = pluginMarketRepository(card);
    if (!value || options.includes(value)) continue;
    if (card.repo_url?.startsWith('local://')) options.unshift(value);
    else options.push(value);
  }
  return options;
}
export function pluginMarketList(
  cards: PluginCard[],
  filter: PluginMarketFilter,
  sort: PluginMarketSort,
  statistics: Record<string, number>,
): PluginCard[] {
  const items = cards.filter(
    (card) =>
      (!filter.name ||
        `${card.name} ${card.description}`.toLowerCase().includes(filter.name.toLowerCase())) &&
      (!filter.authors.length || filter.authors.includes(card.author)) &&
      (!filter.labels.length ||
        pluginMarketLabels(card).some((label) => filter.labels.includes(label))) &&
      (!filter.repositories.length || filter.repositories.includes(pluginMarketRepository(card))),
  );
  // The fixed original only sorts when the statistics response is nonempty.
  if (Object.keys(statistics).length)
    items.sort((a, b) =>
      sort === 'count'
        ? (statistics[b.plugin_id] || 0) - (statistics[a.plugin_id] || 0)
        : (a[sort] ?? '') > (b[sort] ?? '')
          ? 1
          : -1,
    );
  return items;
}
