import { api } from '../api.ts';
import { pluginHostAPI } from './plugin-remote-api.ts';
import type { ExploreMedia, ExplorePage } from './explore-feed.ts';
import type { PluginFormNode } from './plugin-forms.ts';
import type { RecommendSource } from './recommend.ts';

export type ExtraRecommendSource = { name: string; api_path: string; type: string };
export type ExtraDiscoverSource = {
  name: string;
  mediaid_prefix: string;
  api_path: string;
  filter_params: Record<string, any> | null;
  filter_ui: PluginFormNode[];
  depends: Record<string, string[]> | null;
};
const categories: Record<string, string> = {
  电影: 'movie',
  电视剧: 'tv',
  动漫: 'anime',
  榜单: 'rankings',
};
// Fixed recommend.vue deduplicates API paths, rather than display titles.
export function appendExtraRecommendations(base: RecommendSource[], extra: ExtraRecommendSource[]) {
  const rows = [...base];
  const paths = new Set(rows.map((row) => row.api_path || 'recommend/' + row.id));
  for (const row of extra) {
    if (paths.has(row.api_path)) continue;
    paths.add(row.api_path);
    rows.push({
      id: row.api_path,
      title: row.name,
      category: categories[row.type] || row.type,
      api_path: row.api_path,
    });
  }
  return rows;
}
export function appendExtraDiscoverSources(base: string[], extra: ExtraDiscoverSource[]) {
  const prefixes = new Set(base);
  return extra.filter((row) => {
    if (prefixes.has(row.mediaid_prefix)) return false;
    prefixes.add(row.mediaid_prefix);
    return true;
  });
}
const dedupFields = [
  'source',
  'type',
  'season',
  'tmdb_id',
  'imdb_id',
  'tvdb_id',
  'douban_id',
  'bangumi_id',
  'mediaid_prefix',
  'media_id',
];
// Preserve the complete MediaInfo alongside the existing card projection. An
// opaque source identifier is never promoted to a TMDB identifier.
export function pluginCatalogMedia(row: Record<string, any>): ExploreMedia {
  const source = row.mediaid_prefix || row.source || 'themoviedb';
  const sourceID = row.media_id ?? row[source === 'themoviedb' ? 'tmdb_id' : source + '_id'];
  return {
    ...row,
    original_media: row,
    plugin_media_key: dedupFields.map((key) => String(row[key])).join('~'),
    source,
    source_id: sourceID,
    id: source === 'themoviedb' ? Number(row.tmdb_id) || 0 : 0,
    media_type:
      row.type === '电视剧' ? 'tv' : row.type === '电影' ? 'movie' : row.media_type || row.type,
    title: row.title || row.name || '',
    year: Number(row.year) || 0,
    vote_average: Number(row.vote_average) || 0,
  };
}
export async function requestPluginCatalogPage(
  path: string,
  page: number,
  params: Record<string, any> = {},
): Promise<ExplorePage> {
  // Source MediaCardListView deliberately lets explicit filter_params.page win.
  const rows = await pluginHostAPI.get<Record<string, any>[]>(path, {
    params: { page, ...params },
  });
  if (!Array.isArray(rows)) throw new Error('插件影视列表返回格式无效');
  return { results: rows.map(pluginCatalogMedia), has_more: rows.length > 0 };
}
export function requestRecommendPage(source: RecommendSource, page: number) {
  return source.api_path
    ? requestPluginCatalogPage(source.api_path, page)
    : api<ExplorePage>(
        '/catalog/recommend?' + new URLSearchParams({ id: source.id, page: String(page) }),
      );
}
export function requestRecommendBrowsePage(source: RecommendSource, page: number, title: string) {
  if (!source.api_path) return requestRecommendPage(source, page);
  // Fixed browse.vue passes the complete route.query (including title) into
  // MediaCardListView, with repeated query keys represented as arrays.
  const mark = source.api_path.indexOf('?');
  const path = mark < 0 ? source.api_path : source.api_path.slice(0, mark);
  const query = new URLSearchParams(mark < 0 ? '' : source.api_path.slice(mark + 1));
  query.append('title', title);
  const params: Record<string, string | string[]> = {};
  for (const key of new Set(query.keys())) {
    const values = query.getAll(key);
    params[key] = values.length === 1 ? values[0]! : values;
  }
  return requestPluginCatalogPage(path, page, params);
}
export function applyExtraSourceDependencies(
  current: Record<string, any>,
  previous: Record<string, any>,
  defaults: Record<string, any>,
  depends: ExtraDiscoverSource['depends'],
) {
  for (const key in current) {
    if (!current[key] && defaults[key]) current[key] = defaults[key];
    if (depends && current[key] !== previous[key]) {
      for (const dependent in depends)
        if (key !== dependent && depends[dependent]?.includes(key)) current[dependent] = null;
    }
  }
}
